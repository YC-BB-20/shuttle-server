package copyjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
	"github.com/YC-BB-20/shuttle-server/internal/location"
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
	// Floor is kept free on volume FloorVol (the internal disk with the data
	// folder) when checking a destination's space.
	Floor    uint64
	FloorVol string
	FreeHook func(dst string) uint64 // test hook: free space on dst's disk
	// formatHook (tests): the format of the disk holding a folder.
	formatHook func(dir string) destFormat
	// shareHook (tests): the share kind of a new copy's destination.
	shareHook func(dir string) string
	// Secrets is the folder for link downloads' links (secrets/links, 0700).
	Secrets string
	// Lucid asks LucidLink's client what is left to upload (v0.2.98); nil:
	// a check on LucidLink waits and says why. LucidMax is how much may wait
	// to upload before copying to LucidLink pauses (0: no limit).
	Lucid    LucidClient
	LucidMax int64

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
			j.already = nil
			ReadJournal(j.path("journal.log"), 0, func(r Rec) bool { j.apply(r); return true })
		}
		m.jobs = append(m.jobs, j)
	}
	sortQueue(m.jobs)
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
func (m *Manager) Add(s Spec) (*Job, error) { return m.add(s, nil) }

// AddDraft adds a copy with the list the picker's count made (v0.2.72). A
// draft made for another folder or other rules is ignored and the copy lists
// its folder itself.
func (m *Manager) AddDraft(s Spec, d *Draft) (*Job, error) { return m.add(s, d) }

func (m *Manager) add(s Spec, d *Draft) (*Job, error) {
	if s.Kind == KindVerify {
		return m.addVerify(s)
	}
	s.Kind, s.File, s.From, s.Paused = "", "", nil, false
	s.EstBytes, s.EstFiles = max(0, s.EstBytes), max(0, s.EstFiles)
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
	if s.Wrap {
		// v0.2.81: the copy lands in D/<wrapper>/<source folder name>; the job's
		// destination is that media folder, the wrapper holds only Shuttle's own files.
		name := strings.TrimSpace(s.WrapName)
		if name == "" {
			name = filepath.Base(src)
			if m.Settings().WrapNameFrom == "volume" {
				if v, err := diskio.VolumeOf(src); err == nil && v.Name != "" {
					name = v.Name
				}
			}
		}
		if name == "." || name == ".." || name == "" || strings.ContainsAny(name, "/\\") {
			return nil, fmt.Errorf("%q cannot be used as the wrapper folder's name", name)
		}
		s.WrapName, s.WrapDir = name, filepath.Join(dst, name)
		dst = filepath.Join(s.WrapDir, filepath.Base(src))
	} else {
		s.WrapName, s.WrapHash, s.WrapDir = "", false, ""
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
	if len(only) > 0 {
		dirs := 0
		for _, n := range only {
			if fi, err := os.Stat(filepath.Join(src, n)); err == nil && fi.IsDir() {
				dirs++
			}
		}
		s.OnlyDirs = &dirs
	}
	cs := m.Settings()
	s.SkipSystem, s.SkipAppleDouble = cs.SkipSystem, cs.SkipSystem && cs.SkipAppleDouble
	s.Algo = normAlgo(cs.Hash)
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
	if !sv.CopyOK() || !dv.CopyOK() {
		return nil, errors.New("Shuttle copies only with SMB network shares and the LucidLink mount; connect other disks by cable")
	}
	// Free space: this copy plus the copies ahead of it to the same disk.
	ignoreSpace, ignoreFormat := s.IgnoreSpace, s.IgnoreFormat
	s.IgnoreSpace, s.IgnoreFormat = false, false
	var sizeAtAdd *int64
	if !d.fits(src, s.Only, s.SkipSystem, s.SkipAppleDouble) {
		d = nil
	}
	mctx, mcancel := context.WithTimeout(context.Background(), SizeTimeout)
	var need int64
	var merr error
	var fc *fitCheck
	if !ignoreFormat {
		fc = m.newFitCheck(src, dst)
	}
	if d != nil {
		need, merr = measureManifest(mctx, filepath.Join(d.Dir, "manifest.jsonl"), dst, fc)
	} else {
		need, merr = measure(mctx, location.Folder{Dir: src, Only: s.Only, System: s.SkipSystem, AppleDouble: s.SkipAppleDouble}, dst, fc)
	}
	mcancel()
	if merr == nil {
		sizeAtAdd = &need
		// v0.2.92: files the destination's format may not take. Asked
		// before space, so "copy the rest" is answered first.
		disk := dv.Name
		if n := diskName(dst); n != "" {
			disk = n
		}
		if fs := fc.short(disk); fs != nil {
			m.Log.Info("some files may not fit the destination's format; asking the owner", "source", src, "destination", dst,
				"format", fs.Format, "too_big", fs.TooBig, "names", fs.Names, "case_clashes", fs.Clashes)
			return nil, &FormatError{*fs}
		}
		if !ignoreSpace {
			if short, err := m.spaceFor(dst, need, nil); err == nil && short != nil {
				m.Log.Info("a new copy may not fit on its destination; asking the owner", "source", src, "destination", dst,
					"need", short.Need, "queued", short.Queued, "free", short.Free)
				return nil, &SpaceError{*short}
			}
		}
	} else {
		m.Log.Info("the size of a new copy is counted by its scan; space is checked when it starts", "source", src, "err", merr)
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
	if m.shareHook != nil {
		dv.Share = m.shareHook(dst)
	}
	s.SourceShare, s.DestShare = sv.Share, dv.Share
	s.Lucid = dv.Share == diskio.ShareLucid
	if s.Lucid {
		// v0.2.98: a copy on LucidLink is read back only after everything is
		// uploaded, so the whole folder is copied first and checked after.
		if s.Check == "" {
			s.Hash = HashCheckLater
		}
	}
	s.Source, s.Dest = src, dst
	now := time.Now()
	// Each job gets its own folder. Two jobs added in the same millisecond
	// (several drives for one card) once shared an id and one job.json; the
	// folder is now made with Mkdir, which fails if it exists, and the id
	// gets -2, -3… (v0.2.85).
	id, err := m.newJobDir(now.Format("20060102-150405.000"))
	if err != nil {
		return nil, err
	}
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Spec: s, Created: now,
		Choice: "run", Phase: "scan", SourceVol: sv.ID, DestVol: dv.ID, SameVolume: sv.ID == dv.ID, SizeAtAdd: sizeAtAdd, SpaceOK: ignoreSpace}}
	if d != nil && !j.adopt(d) {
		m.Log.Info("the list made while choosing could not be used; the copy lists its folder itself", "source", src)
	}
	if err := j.save(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	m.Log.Info("copy job added", "job", id, "source", src, "destination", dst, "hash", s.Hash, "algo", s.Algo, "s2", s.S2, "check", s.Check, "verify", s.Verify, "same_disk", j.st.SameVolume)
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

// newJobDir makes a job's own folder under m.Dir and returns its id: base,
// or base-2, base-3… when that name is taken.
func (m *Manager) newJobDir(base string) (string, error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return "", err
	}
	for n := 1; n < 1000; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		err := os.Mkdir(filepath.Join(m.Dir, id), 0o700)
		if err == nil {
			return id, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", errors.New("could not find a free name for the job's folder")
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

// SetHidden hides a finished or stopped entry from the history, or shows it
// again. It changes only how the page lists it: the job, its records and
// the copied files stay exactly as they are.
func (m *Manager) SetHidden(id string, hidden bool) error {
	j := m.find(id)
	if j == nil {
		return errors.New("no such job")
	}
	j.mu.Lock()
	if j.st.Phase != "done" {
		j.mu.Unlock()
		return errors.New("only finished or stopped entries can be hidden")
	}
	j.st.Hidden = hidden
	j.mu.Unlock()
	if err := j.save(); err != nil {
		return err
	}
	m.Log.Info("history entry hidden or shown", "job", id, "hidden", hidden)
	return nil
}

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
	j.st.SpaceAsk = nil // answered: Resume checks the space again, Stop cancels
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

// unfinishedLocked: the job id exists and has not finished, or finished
// with failed files and waits for the owner's answer (v0.2.99), so its
// Verify item waits with it. m.mu is held.
func (m *Manager) unfinishedLocked(id string) bool {
	for _, o := range m.jobs {
		if o.st.ID == id {
			o.mu.Lock()
			defer o.mu.Unlock()
			return o.st.Phase != "done" || o.st.ErrorsAsk != nil
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
	if j.st.Spec.isLink() {
		m.runLink(ctx, j)
		return
	}
	sd, err1 := m.Disks.For(j.st.Spec.Source)
	dd, err2 := m.Disks.For(j.st.Spec.Dest)
	if which := m.missing(j); which != "" {
		m.waitMissing(ctx, j, which)
		return
	}
	if err1 != nil || err2 != nil {
		which := "both"
		if err1 == nil {
			which = "destination"
		} else if err2 == nil {
			which = "source"
		}
		m.waitMissing(ctx, j, which)
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
	j.startWork(time.Now())
	err := j.run(jctx, env{format: m.formatFor(j.st.Spec.Dest), budget: m.Budget, src: sd, dst: dd, afterCopy: m.AfterCopy, copyRead: m.CopyRead,
		space: func() error { return m.checkSpaceAtStart(j) }, wrapHash: func() { m.writeWrapperHashFile(j) },
		lucid: m.Lucid, lucidMax: m.LucidMax, log: m.Log}, m.Accepting)
	cancel(nil)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()
	j.mu.Lock()
	worked := j.endWorkLocked(time.Now()) // paused, waiting or stopped: keep this run's time
	j.mu.Unlock()
	if worked {
		j.save()
	}

	cause := context.Cause(jctx)
	j.mu.Lock()
	stopped := j.st.Choice == "stopped" // also when Stop came while a pause was settling
	j.mu.Unlock()
	var short errSpace
	switch {
	case stopped:
		m.finishStopped(j)
	case errors.As(err, &short):
		m.holdForSpace(j, short.short)
	case err == nil:
		j.mu.Lock()
		c, took := j.c, time.Duration(j.st.WorkNs).Round(time.Second)
		j.mu.Unlock()
		m.Log.Info("copy job finished", "job", j.st.ID, "verified", c.Verified, "already_there", c.Identical,
			"failed", c.Failed, "s2_ok", c.Strong, "s2_warnings", c.S2Warn, "copied_only", c.Hashed,
			"took", took.String())
		m.askAboutErrors(j)
		m.queueVerify(j)
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	case errors.Is(err, diskio.ErrGone), errors.Is(cause, diskio.ErrGone):
		which := m.missing(j)
		if which == "" {
			which = "both" // back already, or the check could not tell
		}
		if !j.st.Spec.isVerify() {
			m.Log.Warn("a disk was disconnected during a copy", "job", j.st.ID, "missing", which)
		}
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
	s, d := j.st.Spec.isLink() || ok(j.st.Spec.Source, j.st.SourceVol), ok(j.st.Spec.Dest, j.st.DestVol) // a link has no source disk
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
	if j.st.Spec.isVerify() {
		m.failMissing(j, which)
		return
	}
	m.rest(ctx, j, MissingReason(which, j.st.Spec.Source, j.st.Spec.Dest), which)
}

// VerifyMissingReason names the folders a Verify job could not find.
func VerifyMissingReason(which, src, dst string) string {
	switch which {
	case "source":
		return "Source folder missing: " + src
	case "destination":
		return "Destination folder missing: " + dst
	}
	return "Source folder missing: " + src + "; destination folder missing: " + dst
}

// failMissing ends a Verify job whose source or destination folder is
// missing (owner, 13 November 2026): unlike a copy it does not wait. It
// changes nothing on either side; what was checked keeps its result, and
// the copy can be verified again once the folder is back.
func (m *Manager) failMissing(j *Job, which string) {
	j.mu.Lock()
	if !j.st.Spec.readsSource() || which == "" {
		which = "destination" // source and destination are the same folder
	}
	why := VerifyMissingReason(which, j.st.Spec.Source, j.st.Spec.Dest) + ". The check stopped; nothing was changed. Connect it and verify again."
	j.st.Error, j.st.Phase, j.st.Finished, j.st.Checkpoints = why, "done", time.Now(), nil
	j.live = live{}
	j.mu.Unlock()
	j.save()
	m.Log.Warn("verify job stopped: a folder is missing", "job", j.st.ID, "missing", which)
	m.poke()
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
