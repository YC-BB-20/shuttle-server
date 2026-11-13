package copyjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

var (
	errOwnerPause = errors.New("paused by you")
	errOwnerStop  = errors.New("stopped by you")
	errShutdown   = errors.New("Shuttle Server is stopping")
)

// Manager runs copy jobs one at a time, in the order they were added.
type Manager struct {
	Dir       string // state/jobs
	Budget    *limits.Budget
	Disks     *diskio.Registry
	Accepting func() bool
	Log       *slog.Logger
	AfterCopy func(tmp string) // test hook
	CopyRead  func(b []byte)   // test hook: sees (and may damage) each chunk read for the copy

	mu      sync.Mutex
	jobs    []*Job
	running *Job
	cancel  context.CancelCauseFunc
	wake    chan struct{}

	rechecks []*recheckReq // files to read again, first is the one being read

	setMu sync.Mutex
	set   *Settings // read from settings.json on first use
}

// Load reads the jobs left from earlier runs. A job that was running when
// Shuttle stopped carries on from its journal and checkpoints. A damaged
// job folder is set aside, never deleted.
func (m *Manager) Load() error {
	m.wake = make(chan struct{}, 1)
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	ents, err := os.ReadDir(m.Dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() || strings.Contains(e.Name(), ".damaged") {
			continue
		}
		j, err := loadJob(filepath.Join(m.Dir, e.Name()))
		if err != nil {
			m.Log.Error("a copy job could not be read and was set aside", "job", e.Name(), "err", err)
			os.Rename(filepath.Join(m.Dir, e.Name()), filepath.Join(m.Dir, e.Name()+".damaged"))
			continue
		}
		if j.st.Phase != "scan" {
			j.status = make([]byte, j.st.Files)
			ReadJournal(j.path("journal.log"), 0, func(r Rec) bool { j.apply(r); return true })
		}
		m.jobs = append(m.jobs, j)
	}
	sort.Slice(m.jobs, func(a, b int) bool { return m.jobs[a].st.Created.Before(m.jobs[b].st.Created) })
	for _, j := range append([]*Job(nil), m.jobs...) {
		m.queueVerify(j) // a copy that finished just before a crash still gets its Verify job
	}
	return nil
}

func inside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Add checks a request and queues it.
func (m *Manager) Add(s Spec) (*Job, error) {
	if s.Kind == KindVerify {
		return m.addVerify(s)
	}
	s.Kind, s.File, s.From = "", "", nil
	src, err := filepath.Abs(strings.TrimSpace(s.Source))
	if err != nil {
		return nil, err
	}
	dst, err := filepath.Abs(strings.TrimSpace(s.Dest))
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the source folder %q was not found", src)
	}
	if len(s.Only) > MaxOnly {
		return nil, fmt.Errorf("pick at most %d files and folders one by one, or choose their folder", MaxOnly)
	}
	seen := map[string]bool{}
	var only []string
	for _, n := range s.Only {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\") {
			return nil, fmt.Errorf("%q is not a name inside the source folder", n)
		}
		if _, err := os.Lstat(filepath.Join(src, n)); err != nil {
			return nil, fmt.Errorf("%q was not found in the source folder", n)
		}
		if !seen[n] {
			seen[n] = true
			only = append(only, n)
		}
	}
	s.Only = only
	cs := m.Settings()
	s.SkipSystem, s.SkipAppleDouble = cs.SkipSystem, cs.SkipSystem && cs.SkipAppleDouble
	if inside(dst, src) || inside(src, dst) {
		return nil, errors.New("the source and destination cannot be inside each other")
	}
	if fi, err := os.Stat(dst); err == nil && !fi.IsDir() {
		return nil, errors.New("the destination is a file, not a folder")
	}
	if !diskio.VolumeMounted(dst) {
		return nil, fmt.Errorf("the destination disk for %q is not connected", dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, fmt.Errorf("Shuttle cannot create the destination folder (%v)", err)
	}
	parent := dst
	probe := filepath.Join(parent, ".shuttle-write-test.shuttle-part")
	if f, err := os.Create(probe); err != nil {
		return nil, fmt.Errorf("Shuttle cannot write to the destination (%v); macOS cannot write to NTFS disks, for example", err)
	} else {
		f.Close()
		os.Remove(probe) // our own probe file
	}
	sv, err := diskio.VolumeOf(src)
	if err != nil {
		return nil, err
	}
	dv, err := diskio.VolumeOf(parent)
	if err != nil {
		return nil, err
	}
	if sv.Network || dv.Network {
		return nil, errors.New("copies to or from a network server are not supported yet; connect the disk by cable")
	}
	if s.Hash != HashFirst && s.Hash != HashCheckLater {
		s.Hash = HashDuringCopy
	}
	if s.S2 != "skip" {
		s.S2 = "now"
	}
	switch {
	case s.Check != "none":
		s.Check, s.Verify = "", ""
	case s.Verify != "run" && s.Verify != "paused":
		s.Hash, s.Verify = HashDuringCopy, "" // copy only, nothing read back
	default:
		s.Hash = HashDuringCopy // copied now, checked by its own Verify job
	}
	s.Source, s.Dest = src, dst
	now := time.Now()
	id := now.Format("20060102-150405.000")
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Spec: s, Created: now,
		Choice: "run", Phase: "scan", SourceVol: sv.ID, DestVol: dv.ID, SameVolume: sv.ID == dv.ID}}
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return nil, err
	}
	if err := j.save(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	m.Log.Info("copy job added", "job", id, "source", src, "destination", dst, "hash", s.Hash, "s2", s.S2, "check", s.Check, "verify", s.Verify, "same_disk", j.st.SameVolume)
	m.queueVerify(j) // its Verify item is in the queue from the start, under it
	m.poke()
	return j, nil
}

// Busy reports whether a copy is running right now.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running != nil
}

func (m *Manager) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) find(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.st.ID == id {
			return j
		}
	}
	return nil
}

// Pause stops a job at once: a copy keeps what it wrote, a hash keeps its
// place. Nothing proven is lost.
func (m *Manager) Pause(id string) error { return m.choose(id, "paused", errOwnerPause) }

// Resume lets a paused job run again.
func (m *Manager) Resume(id string) error { return m.choose(id, "run", nil) }

// Stop ends a job. Files already copied and proven stay; the partial copy
// in progress (our own temporary file) is removed.
func (m *Manager) Stop(id string) error { return m.choose(id, "stopped", errOwnerStop) }

func (m *Manager) choose(id, choice string, cause error) error {
	j := m.find(id)
	if j == nil {
		return errors.New("no such job")
	}
	j.mu.Lock()
	if j.st.Phase == "done" {
		j.mu.Unlock()
		return errors.New("this job has already finished")
	}
	j.st.Choice = choice
	j.mu.Unlock()
	if err := j.save(); err != nil {
		return err
	}
	m.mu.Lock()
	running := m.running == j
	if running && cause != nil {
		m.cancel(cause)
	}
	m.mu.Unlock()
	if choice == "stopped" && !running {
		// Paused, waiting or queued: nothing is running to finish the stop.
		m.finishStopped(j)
	}
	if choice == "stopped" {
		m.stopFollower(j)
	}
	m.Log.Info("owner changed a copy job", "job", id, "to", choice)
	m.poke()
	return nil
}

// unfinishedLocked: the job id exists and has not finished. m.mu is held.
func (m *Manager) unfinishedLocked(id string) bool {
	for _, o := range m.jobs {
		if o.st.ID == id {
			o.mu.Lock()
			defer o.mu.Unlock()
			return o.st.Phase != "done"
		}
	}
	return false
}

// stopFollower stops the Verify item queued with a copy the owner stopped
// (owner, 12 November 2026). It has read nothing yet, so nothing is lost.
func (m *Manager) stopFollower(cj *Job) {
	cj.mu.Lock()
	id, isCopy := cj.st.VerifyJob, !cj.st.Spec.isVerify()
	cj.mu.Unlock()
	if id == "" || !isCopy {
		return
	}
	if f := m.find(id); f != nil {
		f.mu.Lock()
		waiting := f.st.Phase != "done"
		f.mu.Unlock()
		if waiting {
			m.choose(id, "stopped", errOwnerStop)
			m.Log.Info("the Verify item of a stopped copy was stopped too", "job", id)
		}
	}
}

func (m *Manager) pick() *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.mu.Lock()
		ok := j.st.Choice == "run" && j.st.Phase != "done"
		after := j.st.After
		j.mu.Unlock()
		if ok && after != "" {
			ok = !m.unfinishedLocked(after) // a Verify item waits for its copy
		}
		if ok {
			return j
		}
	}
	return nil
}

// Run works through the queue until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		m.runRechecks(ctx) // between copies, never beside one
		if ctx.Err() != nil {
			return
		}
		j := m.pick()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
			case <-time.After(5 * time.Second):
			}
			continue
		}
		m.runOne(ctx, j)
		if ctx.Err() != nil {
			return
		}
	}
}

func (m *Manager) runOne(ctx context.Context, j *Job) {
	sd, err1 := m.Disks.For(j.st.Spec.Source)
	dd, err2 := m.Disks.For(j.st.Spec.Dest)
	if which := m.missing(j); which != "" {
		m.waitMissing(ctx, j, which)
		return
	}
	if err1 != nil || err2 != nil {
		m.waitMissing(ctx, j, "both")
		return
	}
	if sd.Stalled() || dd.Stalled() {
		m.wait(ctx, j, "A disk is not responding; waiting for it")
		return
	}
	jctx, cancel := context.WithCancelCause(ctx)
	m.mu.Lock()
	m.running, m.cancel = j, cancel
	m.mu.Unlock()
	j.mu.Lock()
	switch j.st.Choice { // changed between pick and here
	case "stopped":
		cancel(errOwnerStop)
	case "paused":
		cancel(errOwnerPause)
	}
	j.mu.Unlock()
	j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	// Notice an unplugged disk within seconds, even when no read or write
	// is failing at that moment (a hash pass between files, a flush).
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
				if !diskio.Present(sd) || !diskio.Present(dd) {
					cancel(diskio.ErrGone)
					return
				}
			}
		}
	}()
	err := j.run(jctx, env{budget: m.Budget, src: sd, dst: dd, afterCopy: m.AfterCopy, copyRead: m.CopyRead}, m.Accepting)
	cancel(nil)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()

	cause := context.Cause(jctx)
	j.mu.Lock()
	stopped := j.st.Choice == "stopped" // also when Stop came while a pause was settling
	j.mu.Unlock()
	switch {
	case stopped:
		m.finishStopped(j)
	case err == nil:
		j.mu.Lock()
		c := j.c
		j.mu.Unlock()
		m.Log.Info("copy job finished", "job", j.st.ID, "verified", c.Verified, "already_there", c.Identical,
			"failed", c.Failed, "s2_ok", c.Strong, "s2_warnings", c.S2Warn, "copied_only", c.Hashed)
		m.queueVerify(j)
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	case errors.Is(err, diskio.ErrGone), errors.Is(cause, diskio.ErrGone):
		which := m.missing(j)
		if which == "" {
			which = "both" // back already, or the check could not tell
		}
		m.Log.Warn("a disk was disconnected during a copy", "job", j.st.ID, "missing", which)
		m.waitMissing(ctx, j, which)
	case errors.Is(err, diskio.ErrStalled):
		m.wait(ctx, j, "A disk is not responding; waiting for it")
	default:
		var w waitErr
		if errors.As(err, &w) {
			m.wait(ctx, j, w.reason)
			return
		}
		m.Log.Error("copy job hit a problem; it will try again", "job", j.st.ID, "err", err)
		m.wait(ctx, j, "Problem: "+err.Error()+" — trying again shortly")
	}
}

// finishStopped ends a stopped job: proven files stay, the partial copy of
// the file in progress (our own temporary file) is removed.
func (m *Manager) finishStopped(j *Job) {
	j.mu.Lock()
	if j.st.Phase == "done" {
		j.mu.Unlock()
		return
	}
	temps := map[string]bool{}
	if j.live.temp != "" {
		temps[j.live.temp] = true
	}
	idx := map[int]bool{}
	for _, c := range j.st.Checkpoints {
		if c.Stage == "copy" {
			idx[c.Index] = true
		}
	}
	dst, files, later := j.st.Spec.Dest, j.st.Files, j.st.Spec.checkLater()
	j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
	j.live = live{}
	j.mu.Unlock()
	var left []byte
	if later && files > 0 {
		// Copy-then-check: copies that never reached their check are our own
		// unproven temporary files. A byte per file, like the status table.
		left = make([]byte, files)
		ReadJournal(j.path("journal.log"), 0, func(r Rec) bool {
			if r.I >= 0 && r.I < files {
				left[r.I] = 0
				if r.St == "copied" {
					left[r.I] = 1
				}
			}
			return true
		})
	}
	if len(idx) > 0 || left != nil { // after a restart the partial copy is known only from its checkpoint
		if it, err := openManifest(j.path("manifest.jsonl")); err == nil {
			for it.Next() {
				final := filepath.Join(dst, filepath.FromSlash(it.E.P))
				for _, t := range []string{tempName(final), oldTempName(final)} {
					if idx[it.E.I] {
						temps[t] = true
					} else if it.E.I < len(left) && left[it.E.I] == 1 && strings.HasSuffix(t, ".shuttle-part") {
						os.Remove(t) // our own copy that was never checked
					}
				}
			}
			it.Close()
		}
	}
	for t := range temps {
		if strings.HasSuffix(t, ".shuttle-part") {
			os.Remove(t) // our own partial copy
		}
	}
	j.save()
}

// missing says which of a job's disks is not connected as the same volume:
// "source", "destination", "both" or "".
func (m *Manager) missing(j *Job) string {
	ok := func(path, id string) bool {
		if !diskio.VolumeMounted(path) {
			return false
		}
		if _, err := os.Stat(path); err != nil {
			return false // the folder is gone with its disk
		}
		d, err := m.Disks.For(path)
		return err == nil && d.ID == id && diskio.Present(d)
	}
	s, d := ok(j.st.Spec.Source, j.st.SourceVol), ok(j.st.Spec.Dest, j.st.DestVol)
	switch {
	case !s && !d:
		return "both"
	case !s:
		return "source"
	case !d:
		return "destination"
	}
	return ""
}

// diskName is the disk's name as the owner knows it, from the path.
func diskName(path string) string {
	clean := filepath.Clean(path)
	for _, base := range []string{"/Volumes/", "/media/"} {
		if rest, ok := strings.CutPrefix(clean, base); ok {
			parts := strings.Split(rest, "/")
			if base == "/media/" && len(parts) > 1 {
				return parts[1]
			}
			return parts[0]
		}
	}
	return ""
}

// MissingReason is the message for a job whose disk is not connected.
func MissingReason(which, src, dst string) string {
	one := func(kind, path string) string {
		if n := diskName(path); n != "" {
			return kind + " disk not connected: " + n
		}
		return kind + " folder not found: " + path // on the computer's own disk
	}
	const tail = ". Once it is back, the copy continues by itself; nothing verified is lost."
	switch which {
	case "source":
		return one("Source", src) + tail
	case "destination":
		return one("Destination", dst) + tail
	}
	return one("Source", src) + "; " + strings.ToLower(one("Destination", dst)[:1]) + one("Destination", dst)[1:] + ". Once they are back, the copy continues by itself."
}

func (m *Manager) waitMissing(ctx context.Context, j *Job, which string) {
	reason := MissingReason(which, j.st.Spec.Source, j.st.Spec.Dest)
	if j.st.Spec.isVerify() {
		which = "destination"
		reason = strings.Replace(MissingReason(which, "", j.st.Spec.Dest), "the copy continues", "the check continues", 1)
	}
	m.rest(ctx, j, reason, which)
}

// wait shows why a job is resting, then lets the queue try again. Waiting
// costs no retries.
func (m *Manager) wait(ctx context.Context, j *Job, reason string) { m.rest(ctx, j, reason, "") }

func (m *Manager) rest(ctx context.Context, j *Job, reason, missing string) {
	j.setLive(func(l *live) { l.reason, l.missing = reason, missing })
	select {
	case <-ctx.Done():
	case <-m.wake:
	case <-time.After(5 * time.Second):
	}
}
