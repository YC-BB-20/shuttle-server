// Package speedtest measures how fast a disk writes and reads (v0.2.33), in
// the manner of Blackmagic's Disk Speed Test: a large test file is written
// and read back in turn, round after round, with the system cache bypassed,
// until the owner stops it (at most MaxDuration).
//
// It writes only its own test file, at the top of the disk, and removes it
// when it ends. A read-only disk or a camera card is never written: it can
// only have a read test, which reads files already there. Buffers come from
// the shared budget; all I/O goes through the disk's own slot and stall
// timer, like every job.
package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
	"github.com/YC-BB-20/shuttle-server/internal/storage"
)

// TestFile is the only file this package ever writes, at the top of a disk.
const TestFile = ".shuttle-speedtest.tmp"

// Limits of a test.
const (
	Block       = 16 << 20
	MaxDuration = 10 * time.Minute
	margin      = 1 << 30 // free space left over on top of the test file
	maxResults  = 50
	maxWalk     = 5000 // entries looked at to find files for a read test
)

// sizeUnit is a GB; tests make it smaller.
var sizeUnit int64 = 1 << 30

// Sizes the owner may pick, in GB.
var Sizes = []int{1, 2, 5, 10}

// Status is what the page shows while a test runs and after it ends.
type Status struct {
	Running   bool      `json:"running"`
	Path      string    `json:"path,omitempty"`
	Disk      string    `json:"disk,omitempty"`
	ReadOnly  bool      `json:"read_only,omitempty"` // a read test: nothing is written
	Phase     string    `json:"phase,omitempty"`     // write, flush, read, or empty
	Size      int64     `json:"size,omitempty"`      // bytes per round
	Done      int64     `json:"done,omitempty"`      // bytes of this phase so far
	Round     int       `json:"round,omitempty"`
	WriteNow  int64     `json:"write_now,omitempty"` // bytes a second, live
	ReadNow   int64     `json:"read_now,omitempty"`
	Write     int64     `json:"write,omitempty"` // last full round, flush included
	Read      int64     `json:"read,omitempty"`
	WriteBest int64     `json:"write_best,omitempty"`
	ReadBest  int64     `json:"read_best,omitempty"`
	Started   time.Time `json:"started,omitzero"`
	Error     string    `json:"error,omitempty"`
	Ended     string    `json:"ended,omitempty"` // why it stopped, in plain words
}

// Result is the last finished test of one disk, kept across restarts.
type Result struct {
	VolID    string    `json:"vol_id"`
	Disk     string    `json:"disk"`
	Path     string    `json:"path"`
	Write    int64     `json:"write,omitempty"`
	Read     int64     `json:"read"`
	ReadOnly bool      `json:"read_only,omitempty"`
	Size     int64     `json:"size"`
	At       time.Time `json:"at"`
}

// Tester runs one test at a time.
type Tester struct {
	Budget    *limits.Budget
	Disks     *diskio.Registry
	Accepting func() bool
	Busy      func(mount string) string // what job is using the disk now, if any
	StateFile string                    // results, written atomically
	Log       *slog.Logger

	mu      sync.Mutex
	st      Status
	cancel  context.CancelFunc
	results []Result
	loaded  bool
}

var errStopped = errors.New("stopped")

// Status returns the current or last test.
func (t *Tester) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

// Results lists the last test of every disk, newest first.
func (t *Tester) Results() []Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loadLocked()
	return append([]Result(nil), t.results...)
}

func (t *Tester) loadLocked() {
	if t.loaded || t.StateFile == "" {
		return
	}
	t.loaded = true
	if b, err := os.ReadFile(t.StateFile); err == nil {
		json.Unmarshal(b, &t.results)
	}
}

// Stop ends a running test; its file is removed.
func (t *Tester) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
}

// Start begins a test of the disk holding the folder dir (a disk's top, or
// the home folder for this computer's own disk). readOnly asks for a read
// test of files already there; it is forced for a read-only disk and for
// anything that looks like a camera card.
func (t *Tester) Start(dir string, sizeGB int, readOnly bool) error {
	size := int64(0)
	for _, s := range Sizes {
		if s == sizeGB {
			size = int64(s) * sizeUnit
		}
	}
	if size == 0 {
		return errors.New("choose a test size of 1, 2, 5 or 10 GB")
	}
	dir = filepath.Clean(dir)
	v, err := diskio.VolumeOf(dir)
	if fi, serr := os.Stat(dir); err != nil || serr != nil || !fi.IsDir() || !diskio.VolumeMounted(dir) {
		return errors.New("this disk is not connected")
	}
	if v.Network {
		return errors.New("network servers cannot be tested yet")
	}
	if t.Accepting != nil && !t.Accepting() {
		return errors.New("Shuttle is short of memory right now; try again in a minute")
	}
	if t.Busy != nil {
		if what := t.Busy(v.Mount); what != "" {
			return errors.New("this disk is in use (" + what + "); pause that job first, or the test would slow it down and measure less than the disk can do")
		}
	}
	readOnly = readOnly || v.ReadOnly || storage.CameraHint(v.Mount) != "" || storage.CameraHint(dir) != ""
	if !readOnly && v.Free < uint64(size)+uint64(min(margin, sizeUnit)) {
		return errors.New("not enough free space for the test file: choose a smaller size")
	}
	d, err := t.Disks.For(v.Mount)
	if err != nil {
		return errors.New("this disk is not connected")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Running {
		return errors.New("a speed test is already running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), MaxDuration)
	t.cancel = cancel
	t.st = Status{Running: true, Path: v.Mount, Disk: v.Name, ReadOnly: readOnly, Size: size, Started: time.Now()}
	go t.run(ctx, cancel, d, v, dir, size, readOnly)
	return nil
}

func (t *Tester) set(f func(*Status)) {
	t.mu.Lock()
	f(&t.st)
	t.mu.Unlock()
}

func (t *Tester) run(ctx context.Context, cancel context.CancelFunc, d *diskio.Disk, v diskio.Volume, dir string, size int64, readOnly bool) {
	defer cancel()
	lease, err := t.Budget.Acquire(ctx, Block)
	if err != nil {
		t.end("", "Shuttle's memory buffers were busy; try again")
		return
	}
	defer t.Budget.Release(lease)
	buf := lease.B
	var files []string
	if readOnly {
		files = bigFiles(ctx, dir, size)
		if len(files) == 0 {
			t.end("", "There are no large files on this disk to read, and a read-only disk or camera card is never written to. Nothing was tested.")
			return
		}
	} else {
		r := rand.New(rand.NewChaCha8([32]byte{'s', 'h', 'u', 't', 't', 'l', 'e'}))
		for i := 0; i+8 <= len(buf); i += 8 { // data that cannot be compressed
			v := r.Uint64()
			for k := 0; k < 8; k++ {
				buf[i+k] = byte(v >> (8 * k))
			}
		}
	}
	test := filepath.Join(dir, TestFile)
	if !readOnly {
		defer os.Remove(test) // our own file, always removed
	}
	for round := 1; ; round++ {
		t.set(func(s *Status) { s.Round = round })
		if !readOnly {
			w, err := writeRound(ctx, d, test, buf, size, t)
			if err != nil {
				t.finish(err)
				return
			}
			t.set(func(s *Status) { s.Write, s.WriteBest = w, max(s.WriteBest, w) })
			files = []string{test}
		}
		r, err := readRound(ctx, d, files, buf, size, t)
		if err != nil {
			t.finish(err)
			return
		}
		t.set(func(s *Status) { s.Read, s.ReadBest = r, max(s.ReadBest, r) })
		t.save(v, size, readOnly)
	}
}

// meter turns bytes done into a live rate, smoothed over about a second.
type meter struct {
	t0, last  time.Time
	lastBytes int64
	rate      float64
}

func (m *meter) add(done int64) (int64, bool) {
	now := time.Now()
	if m.last.IsZero() {
		m.t0, m.last = now, now
		return 0, false
	}
	dt := now.Sub(m.last).Seconds()
	if dt < 0.25 {
		return int64(m.rate), false
	}
	r := float64(done-m.lastBytes) / dt
	if m.rate == 0 {
		m.rate = r
	} else {
		m.rate = 0.6*m.rate + 0.4*r
	}
	m.last, m.lastBytes = now, done
	return int64(m.rate), true
}

func writeRound(ctx context.Context, d *diskio.Disk, path string, buf []byte, size int64, t *Tester) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	diskio.NoCache(f)
	t.set(func(s *Status) { s.Phase, s.Done, s.ReadNow = "write", 0, 0 })
	start := time.Now()
	var m meter
	m.add(0)
	for off := int64(0); off < size; off += int64(len(buf)) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		n := min(int64(len(buf)), size-off)
		if err := diskio.WriteChunk(ctx, d, f, buf[:n], off); err != nil {
			return 0, err
		}
		if r, ok := m.add(off + n); ok {
			t.set(func(s *Status) { s.Done, s.WriteNow = off+n, r })
		}
	}
	t.set(func(s *Status) { s.Phase, s.Done = "flush", size })
	if err := diskio.Flush(ctx, d, f); err != nil { // the drive really has it
		return 0, err
	}
	return int64(float64(size) / time.Since(start).Seconds()), nil
}

func readRound(ctx context.Context, d *diskio.Disk, files []string, buf []byte, size int64, t *Tester) (int64, error) {
	t.set(func(s *Status) { s.Phase, s.Done, s.WriteNow = "read", 0, 0 })
	start := time.Now()
	var m meter
	m.add(0)
	var done int64
	for _, p := range files {
		f, err := diskio.OpenRead(p)
		if err != nil {
			return 0, err
		}
		for off := int64(0); done < size; {
			if ctx.Err() != nil {
				f.Close()
				return 0, ctx.Err()
			}
			n, err := diskio.ReadChunk(ctx, d, f, buf[:min(int64(len(buf)), size-done)], off)
			if err != nil {
				f.Close()
				return 0, err
			}
			off += int64(n)
			done += int64(n)
			if r, ok := m.add(done); ok {
				t.set(func(s *Status) { s.Done, s.ReadNow = done, r })
			}
			if n < len(buf) && done < size {
				break // the end of this file
			}
		}
		f.Close()
		if done >= size {
			break
		}
	}
	if done == 0 {
		return 0, errors.New("nothing could be read")
	}
	return int64(float64(done) / time.Since(start).Seconds()), nil
}

// bigFiles picks the largest files near the top of a disk, enough to read
// size bytes, looking at no more than maxWalk entries. Only names and sizes
// are read here.
func bigFiles(ctx context.Context, root string, size int64) []string {
	type cand struct {
		p string
		s int64
	}
	var all []cand
	seen := 0
	filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		seen++
		if seen > maxWalk || ctx.Err() != nil {
			return fs.SkipAll
		}
		if err != nil {
			return nil // unreadable: WalkDir skips it
		}
		if e.IsDir() && p != root && len(e.Name()) > 0 && e.Name()[0] == '.' {
			return fs.SkipDir // system folders such as .Spotlight-V100
		}
		if e.Type().IsRegular() {
			if fi, err := e.Info(); err == nil && fi.Size() >= 64<<20 {
				all = append(all, cand{p, fi.Size()})
			}
		}
		return nil
	})
	sort.Slice(all, func(i, j int) bool { return all[i].s > all[j].s })
	var out []string
	var total int64
	for _, c := range all {
		if total >= size {
			break
		}
		out = append(out, c.p)
		total += c.s
	}
	return out
}

func (t *Tester) finish(err error) {
	switch {
	case errors.Is(err, context.Canceled):
		t.end("", "Stopped by you.")
	case errors.Is(err, context.DeadlineExceeded):
		t.end("", "Stopped after 10 minutes.")
	case errors.Is(err, diskio.ErrGone):
		t.end("The disk was disconnected during the test.", "")
	case errors.Is(err, diskio.ErrStalled):
		t.end("The disk stopped responding during the test.", "")
	default:
		t.end("The test could not go on: "+err.Error(), "")
	}
}

func (t *Tester) end(errText, ended string) {
	t.mu.Lock()
	t.st.Running, t.st.Phase, t.st.WriteNow, t.st.ReadNow, t.st.Error, t.st.Ended = false, "", 0, 0, errText, ended
	t.cancel = nil
	st := t.st
	t.mu.Unlock()
	if t.Log != nil {
		t.Log.Info("disk speed test ended", "disk", st.Disk, "write", st.Write, "read", st.Read, "rounds", st.Round, "error", errText, "why", ended)
	}
}

// save keeps the last finished round of this disk.
func (t *Tester) save(v diskio.Volume, size int64, readOnly bool) {
	t.mu.Lock()
	t.loadLocked()
	r := Result{VolID: v.ID, Disk: v.Name, Path: v.Mount, Write: t.st.Write, Read: t.st.Read, ReadOnly: readOnly, Size: size, At: time.Now()}
	out := []Result{r}
	for _, o := range t.results {
		if o.VolID != v.ID && len(out) < maxResults {
			out = append(out, o)
		}
	}
	t.results = out
	b, _ := json.MarshalIndent(out, "", "  ")
	t.mu.Unlock()
	if t.StateFile != "" {
		if err := fsx.WriteFileAtomic(t.StateFile, b, 0o600); err != nil && t.Log != nil {
			t.Log.Warn("could not save the speed test result", "err", err)
		}
	}
}
