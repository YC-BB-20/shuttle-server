package copyjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/location"
)

// Free space on the destination (owner, 13 November 2026). Before a copy is
// added, and again when it starts, Shuttle adds up what it still has to
// write and what every unfinished copy ahead of it in the queue to the same
// disk still has to write, and compares that with the disk's free space. If
// it does not fit, the owner chooses: continue anyway, or cancel the job.

// SizeTimeout bounds the size count when a job is added, so adding a huge
// folder never hangs the page; past it the size is counted by the scan and
// the space is checked when the copy starts.
var SizeTimeout = 8 * time.Second

// SpaceShort is a copy that may not fit on its destination disk.
type SpaceShort struct {
	Disk       string `json:"disk"`
	Need       int64  `json:"need"`        // this copy, still to write
	Queued     int64  `json:"queued"`      // still to write for the copies ahead of it to the same disk
	QueuedJobs int    `json:"queued_jobs"` // how many copies that is
	Unknown    int    `json:"unknown"`     // copies ahead whose size is not known yet (not counted)
	Free       int64  `json:"free"`
	Margin     int64  `json:"margin"` // kept free on this disk (the free-space floor, internal disk only)
	FitsAlone  bool   `json:"fits_alone"`
	Message    string `json:"message"`
}

// SpaceError is returned by Add when the copy does not fit; adding it again
// with IgnoreSpace set is "continue anyway".
type SpaceError struct{ Short SpaceShort }

func (e *SpaceError) Error() string { return e.Short.Message }

// errSpace stops a copy at its start when it no longer fits.
type errSpace struct{ short SpaceShort }

func (e errSpace) Error() string { return e.short.Message }

// measure counts the bytes a copy will write: the source's files under the
// job's rules, leaving out those already at the destination with the same
// size (they are compared, not written). Only counters are held.
func measure(ctx context.Context, src location.Folder, dst string) (int64, error) {
	var total int64
	err := src.Walk(ctx, func(e location.Entry) error {
		if fi, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(e.Rel))); err == nil && fi.Mode().IsRegular() && fi.Size() == e.Size {
			return nil
		}
		total += e.Size
		return nil
	}, func(location.Skipped) {})
	return total, err
}

// remaining is what a copy job still has to write, and whether it is known.
func remaining(j *Job) (int64, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.st.Phase == "scan" {
		if j.st.SizeAtAdd == nil {
			return 0, false
		}
		return *j.st.SizeAtAdd, true
	}
	left := j.st.Bytes - max(j.c.BytesProven, j.c.BytesCopied)
	if j.st.SizeAtAdd != nil && *j.st.SizeAtAdd < left {
		left = *j.st.SizeAtAdd // files already at the destination are not written
	}
	return max(0, left), true
}

// queuedTo adds up the copies to destination volume vol that are unfinished
// and ahead of job self (all of them when self is nil): running, waiting,
// queued or paused, not stopped.
func (m *Manager) queuedTo(vol string, self *Job) (bytes int64, jobs, unknown int) {
	m.mu.Lock()
	list := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	for _, o := range list {
		if o == self {
			break
		}
		o.mu.Lock()
		count := !o.st.Spec.isVerify() && o.st.Phase != "done" && o.st.Choice != "stopped" && o.st.DestVol == vol
		o.mu.Unlock()
		if !count {
			continue
		}
		jobs++
		if n, ok := remaining(o); ok {
			bytes += n
		} else {
			unknown++
		}
	}
	return
}

// spaceFor compares need with the free space on dst's disk, counting the
// copies ahead of self. It returns nil when everything fits.
func (m *Manager) spaceFor(dst string, need int64, self *Job) (*SpaceShort, error) {
	v, err := diskio.VolumeOf(dst)
	if err != nil {
		return nil, err
	}
	s := SpaceShort{Disk: v.Name, Need: need, Free: int64(v.Free)}
	if m.FreeHook != nil {
		s.Free = int64(m.FreeHook(dst))
	}
	if s.Disk == "" {
		s.Disk = diskName(dst)
	}
	if s.Disk == "" {
		s.Disk = "the destination disk"
	}
	if m.FloorVol != "" && v.ID == m.FloorVol {
		s.Margin = int64(m.Floor)
	}
	s.Queued, s.QueuedJobs, s.Unknown = m.queuedTo(v.ID, self)
	s.FitsAlone = need+s.Margin <= s.Free
	if s.FitsAlone && need+s.Queued+s.Margin <= s.Free {
		return nil, nil
	}
	s.Message = spaceMessage(s)
	return &s, nil
}

func spaceMessage(s SpaceShort) string {
	keep := ""
	if s.Margin > 0 {
		keep = fmt.Sprintf(", keeping %s free", Size(s.Margin))
	}
	if !s.FitsAlone {
		msg := fmt.Sprintf("Not enough space on %s: this copy needs %s and %s is free%s.", s.Disk, Size(s.Need), Size(s.Free), keep)
		if s.QueuedJobs > 0 {
			msg += fmt.Sprintf(" %s already queued there still %s %s to copy.", plural(s.QueuedJobs, "copy", "copies"), have(s.QueuedJobs), Size(s.Queued))
		}
		return msg
	}
	return fmt.Sprintf("This copy fits on %s on its own, but not together with the %s already queued there (%s still to copy): %s needed, %s free%s.",
		s.Disk, plural(s.QueuedJobs, "copy", "copies"), Size(s.Queued), Size(s.Need+s.Queued), Size(s.Free), keep)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func have(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// Size is a byte count written as the page writes it (1024-based).
func Size(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f, u := float64(n), 0
	for f >= 1024 && u < len(units)-1 {
		f /= 1024
		u++
	}
	if u > 2 {
		return fmt.Sprintf("%.1f %s", f, units[u])
	}
	return fmt.Sprintf("%.0f %s", f, units[u])
}

// checkSpaceAtStart runs when a copy starts or resumes its copy pass. A copy
// the owner already let continue anyway is not asked about again.
func (m *Manager) checkSpaceAtStart(j *Job) error {
	j.mu.Lock()
	skip := j.st.SpaceOK || j.st.Spec.isVerify()
	j.mu.Unlock()
	if skip {
		return nil
	}
	need, _ := remaining(j)
	s, err := m.spaceFor(j.st.Spec.Dest, need, j)
	if err != nil || s == nil {
		return nil // free space unreadable: the copy's own errors still stop a full disk
	}
	return errSpace{*s}
}

// holdForSpace pauses a copy that no longer fits and asks the owner.
func (m *Manager) holdForSpace(j *Job, s SpaceShort) {
	j.mu.Lock()
	j.st.Choice, j.st.SpaceAsk = "paused", &s
	j.mu.Unlock()
	j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	j.save()
	m.Log.Warn("a copy does not fit on its destination; waiting for the owner", "job", j.st.ID, "disk", s.Disk,
		"need", s.Need, "queued", s.Queued, "free", s.Free)
}

// ContinueAnyway answers the space question: the copy runs although it may
// not fit. A full disk still stops it with its own error, losing nothing.
func (m *Manager) ContinueAnyway(id string) error {
	j := m.find(id)
	if j == nil {
		return errors.New("no such job")
	}
	j.mu.Lock()
	j.st.SpaceOK = true
	j.mu.Unlock()
	m.Log.Info("owner let a copy continue although it may not fit", "job", id)
	return m.Resume(id)
}
