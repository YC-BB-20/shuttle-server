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
	return nil
}

func inside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Add checks a request and queues it.
func (m *Manager) Add(s Spec) (*Job, error) {
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
	if s.Hash != HashFirst {
		s.Hash = HashDuringCopy // hashing while copying needs the second source read to be proof
	}
	if s.S2 != "skip" || s.Hash == HashDuringCopy {
		s.S2 = "now"
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
	m.Log.Info("copy job added", "job", id, "source", src, "destination", dst, "hash", s.Hash, "s2", s.S2, "same_disk", j.st.SameVolume)
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
	if m.running == j && cause != nil {
		m.cancel(cause)
	}
	m.mu.Unlock()
	m.Log.Info("owner changed a copy job", "job", id, "to", choice)
	m.poke()
	return nil
}

func (m *Manager) pick() *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.mu.Lock()
		ok := j.st.Choice == "run" && j.st.Phase != "done"
		j.mu.Unlock()
		if ok {
			return j
		}
	}
	return nil
}

// Run works through the queue until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
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
	if err1 != nil || err2 != nil || sd.ID != j.st.SourceVol || dd.ID != j.st.DestVol {
		m.wait(ctx, j, "Waiting for a disk: connect the source and destination disks")
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
	j.setLive(func(l *live) { l.reason = "" })
	err := j.run(jctx, env{budget: m.Budget, src: sd, dst: dd, afterCopy: m.AfterCopy, copyRead: m.CopyRead}, m.Accepting)
	cancel(nil)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()

	cause := context.Cause(jctx)
	switch {
	case err == nil:
		j.mu.Lock()
		c := j.c
		j.mu.Unlock()
		m.Log.Info("copy job finished", "job", j.st.ID, "verified", c.Verified, "already_there", c.Identical,
			"failed", c.Failed, "s2_ok", c.Strong, "s2_warnings", c.S2Warn)
	case errors.Is(cause, errOwnerStop):
		j.mu.Lock()
		tmp := j.live.temp
		j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
		j.live = live{}
		j.mu.Unlock()
		if tmp != "" {
			os.Remove(tmp) // our own partial copy
		}
		j.save()
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason = "" })
	case errors.Is(err, diskio.ErrGone):
		m.wait(ctx, j, "Waiting for a disk: one of the disks was disconnected")
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

// wait shows why a job is resting, then lets the queue try again. Waiting
// costs no retries.
func (m *Manager) wait(ctx context.Context, j *Job, reason string) {
	j.setLive(func(l *live) { l.reason = reason })
	select {
	case <-ctx.Done():
	case <-m.wake:
	case <-time.After(5 * time.Second):
	}
}
