package copyjob

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// View is what the page shows for a job.
type View struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Dest       string    `json:"dest"`
	S2         string    `json:"s2"`
	Hash       string    `json:"hash"`
	Only       int       `json:"only,omitempty"` // how many files and folders were picked one by one; 0 means the whole folder
	Created    time.Time `json:"created"`
	Finished   time.Time `json:"finished,omitzero"`
	State      string    `json:"state"` // running, waiting, paused, queued, finished, stopped
	Reason     string    `json:"reason,omitempty"`
	Phase      string    `json:"phase"`
	Stage      string    `json:"stage,omitempty"`
	File       string    `json:"file,omitempty"`
	FileDone   int64     `json:"file_done"`
	FileSize   int64     `json:"file_size"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
	Scanned    int       `json:"scanned"`
	ScannedB   int64     `json:"scanned_bytes"`
	Verified   int       `json:"verified"`
	Identical  int       `json:"identical"`
	Failed     int       `json:"failed"`
	Strong     int       `json:"strong"`
	S2Warn     int       `json:"s2_warnings"`
	Proven     int64     `json:"bytes_proven"`
	Skipped    int       `json:"skipped"`
	SameVolume bool      `json:"same_volume"`
	SrcRate    int64     `json:"source_rate"`
	DstRate    int64     `json:"dest_rate"`
	Progress   float64   `json:"progress"`              // 0 to 1, all reads and writes the job needs
	Rate       int64     `json:"rate,omitempty"`        // overall bytes of the job finished per second, smoothed
	ETA        int64     `json:"eta_seconds,omitempty"` // time left, when it can be estimated
}

type rateMemo struct {
	t        time.Time
	src, dst int64
	sr, dr   int64
	work     float64 // progress in bytes at t
	rate     float64 // smoothed
}

// progress is how far a job is, 0 to 1, counting the passes it makes: with
// hashing while copying, the copy and then D1 and S2 together; with the
// earlier scheme S1, the copy and D1, then S2 at the end. The stage names
// are the ones run.go sets.
func progress(st state, c counts, l live) float64 {
	switch {
	case st.Phase == "scan":
		return 0
	case st.Phase == "done":
		return 1
	case st.Bytes <= 0:
		if st.Files == 0 {
			return 1
		}
		return float64(c.Verified+c.Identical+c.Failed) / float64(st.Files)
	}
	size, done := float64(l.size), float64(l.done)
	has := func(p string) bool { return strings.HasPrefix(l.stage, p) }
	var part float64
	if st.Spec.hashDuringCopy() {
		switch {
		case has("Copying"), has("Reading the source (S1)"):
			part = done / 2
		case has("Reading back"), has("Checking"):
			part = size/2 + done/2
		}
	} else {
		switch {
		case has("Reading the source (S1)"):
			part = done / 3
		case has("Copying"):
			part = size/3 + done/3
		case has("Reading back"), has("Checking"):
			part = 2*size/3 + done/3
		}
	}
	f := min(1, (float64(c.BytesProven)+part)/float64(st.Bytes))
	if st.Spec.hashDuringCopy() || st.Spec.S2 != "now" {
		return f
	}
	if st.Phase != "s2" {
		return 0.75 * f
	}
	return 0.75 + 0.25*min(1, float64(c.Strong+c.S2Warn)/float64(max(1, c.Verified+c.Identical)))
}

var memo = map[*Job]*rateMemo{}

// Views lists jobs, newest first: everything unfinished plus the last 20
// finished.
func (m *Manager) Views() []View {
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	running := m.running
	m.mu.Unlock()
	var out []View
	finished := 0
	now := time.Now()
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		v, st, l := baseView(j)
		switch {
		case v.State != "":
		case st.Choice == "paused":
			v.State = "paused"
		case j == running:
			v.State, v.Stage, v.File, v.FileDone, v.FileSize = "running", l.stage, l.file, l.done, l.size
			m.mu.Lock()
			r := memo[j]
			if r == nil {
				r = &rateMemo{t: now, src: j.srcBytes.Load(), dst: j.dstBytes.Load(), work: v.Progress * float64(st.Bytes)}
				memo[j] = r
			}
			work := v.Progress * float64(st.Bytes)
			if dt := now.Sub(r.t).Seconds(); dt >= 1 {
				s, d := j.srcBytes.Load(), j.dstBytes.Load()
				r.sr, r.dr = int64(float64(s-r.src)/dt), int64(float64(d-r.dst)/dt)
				if w := max(0, work-r.work) / dt; r.rate == 0 || dt > 10 { // fresh, or back from a pause
					r.rate = w
				} else {
					r.rate = 0.85*r.rate + 0.15*w // smooth: big files finish in jumps
				}
				r.t, r.src, r.dst, r.work = now, s, d, work
			}
			v.SrcRate, v.DstRate, v.Rate = r.sr, r.dr, int64(r.rate)
			if r.rate > 0 && st.Phase != "scan" {
				v.ETA = int64((1 - v.Progress) * float64(st.Bytes) / r.rate)
			}
			m.mu.Unlock()
		case l.reason != "":
			v.State, v.Reason = "waiting", l.reason
		default:
			v.State = "queued"
		}
		if v.State == "finished" || v.State == "stopped" {
			if finished++; finished > 20 {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

// baseView is a job's view without what only a running job has. State is
// set only for finished and stopped jobs.
func baseView(j *Job) (View, state, live) {
	j.mu.Lock()
	st, c, l := j.st, j.c, j.live
	j.mu.Unlock()
	v := View{ID: st.ID, Source: st.Spec.Source, Dest: st.Spec.Dest, S2: st.Spec.S2, Hash: st.Spec.Hash, Only: len(st.Spec.Only),
		Created: st.Created, Finished: st.Finished, Phase: st.Phase, Files: st.Files, Bytes: st.Bytes, Skipped: st.Skipped,
		Verified: c.Verified, Identical: c.Identical, Failed: c.Failed, Strong: c.Strong, S2Warn: c.S2Warn,
		Proven: c.BytesProven, SameVolume: st.SameVolume, Scanned: l.scanned, ScannedB: l.scannedBytes,
		Progress: progress(st, c, l)}
	if st.Phase == "done" && st.Choice == "stopped" {
		v.Progress = 0 // a stopped copy shows what is proven, not a full bar
		if st.Bytes > 0 {
			v.Progress = float64(c.BytesProven) / float64(st.Bytes)
		}
	}
	switch {
	case st.Phase == "done" && st.Choice == "stopped":
		v.State = "stopped"
	case st.Phase == "done":
		v.State = "finished"
	}
	return v, st, l
}

// History lists finished and stopped copies, newest first, skipping the
// newest skip of them.
func (m *Manager) History(skip, limit int) []View {
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	out := []View{}
	for i := len(jobs) - 1; i >= 0 && len(out) < limit; i-- {
		v, _, _ := baseView(jobs[i])
		if v.State == "" {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		out = append(out, v)
	}
	return out
}

// Problem is a failed file or an S2 warning.
type Problem struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Why  string `json:"why"`
}

// Problems lists up to limit failures and warnings, read from the journal.
func (m *Manager) Problems(id string, limit int) []Problem {
	j := m.find(id)
	if j == nil {
		return nil
	}
	var out []Problem
	ReadJournal(j.path("journal.log"), 0, func(r Rec) bool {
		if r.St == "failed" || r.St == "s2changed" || r.St == "s2unstable" {
			out = append(out, Problem{r.P, r.St, r.Err})
		}
		return len(out) < limit
	})
	return out
}

// FileRow is one file of a job, for the page's file list.
type FileRow struct {
	I      int    `json:"i"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Status string `json:"status"` // waiting, working, proven, strong, failed, warning
	Stage  string `json:"stage,omitempty"`
	Done   int64  `json:"done,omitempty"`
	Why    string `json:"why,omitempty"`
}

// FilePage is one page of a job's files. Next is where the following page
// starts, or -1 when there is none.
type FilePage struct {
	Rows []FileRow `json:"rows"`
	Next int       `json:"next"`
	Note string    `json:"note,omitempty"`
}

// MaxFilePage bounds one page of the file list.
const MaxFilePage = 200

// listMu lets one file list be read at a time, so a page cannot make many
// passes over a large manifest at once.
var listMu sync.Mutex

func rowStatus(st byte) string {
	switch st {
	case stVerified:
		return "proven"
	case stStrong:
		return "strong"
	case stFailed:
		return "failed"
	case stS2Warn:
		return "warning"
	}
	return "waiting"
}

func filterOK(filter, status string) bool {
	switch filter {
	case "todo":
		return status == "waiting" || status == "working"
	case "done":
		return status == "proven" || status == "strong"
	case "problems":
		return status == "failed" || status == "warning"
	}
	return true
}

// Files lists up to limit files of a job from index from on, matching filter
// (all, todo, done, problems). It reads the manifest from disk and holds only
// the page in memory.
func (m *Manager) Files(id, filter string, from, limit int) (FilePage, error) {
	page := FilePage{Rows: []FileRow{}, Next: -1}
	j := m.find(id)
	if j == nil {
		return page, errors.New("this copy no longer exists")
	}
	if limit <= 0 || limit > MaxFilePage {
		limit = MaxFilePage
	}
	j.mu.Lock()
	phase, l := j.st.Phase, j.live
	j.mu.Unlock()
	if phase == "scan" {
		page.Note = "The source is still being scanned; the file list appears when the scan finishes."
		return page, nil
	}
	listMu.Lock()
	defer listMu.Unlock()
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return page, errors.New("the file list cannot be read")
	}
	defer it.Close()
	need := map[int]int{} // index -> row, for failure reasons
	for it.Next() {
		e := it.E
		if e.I < from {
			continue
		}
		j.mu.Lock()
		st := stPending
		if e.I < len(j.status) {
			st = j.status[e.I]
		}
		j.mu.Unlock()
		r := FileRow{I: e.I, Path: e.P, Size: e.S, Status: rowStatus(st)}
		if l.file == e.P && l.stage != "" && phase != "done" {
			r.Status, r.Stage, r.Done = "working", l.stage, l.done
		}
		if !filterOK(filter, r.Status) {
			continue
		}
		if len(page.Rows) == limit {
			page.Next = e.I
			break
		}
		if r.Status == "failed" || r.Status == "warning" {
			need[e.I] = len(page.Rows)
		}
		page.Rows = append(page.Rows, r)
	}
	if len(need) > 0 {
		ReadJournal(j.path("journal.log"), 0, func(rec Rec) bool {
			if k, ok := need[rec.I]; ok && rec.Err != "" {
				page.Rows[k].Why = rec.Err
			}
			return true
		})
	}
	return page, nil
}

// Event is one step in a file's story, from the journal.
type Event struct {
	Time     time.Time `json:"time"`
	What     string    `json:"what"`
	Why      string    `json:"why,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	Ms       int64     `json:"ms,omitempty"`
}

// Hashes are the file's XXH128 hashes; any can be empty.
type Hashes struct {
	C  string `json:"c,omitempty"`  // bytes read for the copy
	S1 string `json:"s1,omitempty"` // source read on its own first
	D1 string `json:"d1,omitempty"` // copy read back
	S2 string `json:"s2,omitempty"` // source read again
}

// FileInfo is everything known about one file of a job.
type FileInfo struct {
	FileRow
	Mtime      time.Time `json:"mtime"`
	Source     string    `json:"source"`
	Dest       string    `json:"dest"`
	DestExists bool      `json:"dest_exists"`
	DestSize   int64     `json:"dest_size,omitempty"`
	Proof      string    `json:"proof"`
	Hash       Hashes    `json:"hash"`
	Events     []Event   `json:"events"`
}

// maxEvents bounds a file's story; a file has a handful of records at most.
const maxEvents = 50

var eventText = map[string]string{
	"strong":     "Copied and proven: the copy read back and the source read a second time both match",
	"verified":   "Copied and proven: the copy read back matches the source",
	"identical":  "Already at the destination; compared and identical, left untouched",
	"failed":     "Failed",
	"s2ok":       "Source read a second time: matches",
	"s2changed":  "Source read a second time: the source had changed after the copy",
	"s2unstable": "Source read a second time: it read differently",
}

// FileInfo describes file i of a job: what it is, where it went, every hash
// and every step recorded for it. Reads the manifest and journal from disk.
func (m *Manager) FileInfo(id string, i int) (FileInfo, error) {
	var fi FileInfo
	j := m.find(id)
	if j == nil {
		return fi, errors.New("this copy no longer exists")
	}
	j.mu.Lock()
	phase, l, spec := j.st.Phase, j.live, j.st.Spec
	st := stPending
	if i >= 0 && i < len(j.status) {
		st = j.status[i]
	}
	j.mu.Unlock()
	if phase == "scan" {
		return fi, errors.New("the source is still being scanned")
	}
	listMu.Lock()
	defer listMu.Unlock()
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return fi, errors.New("the file list cannot be read")
	}
	found := false
	for it.Next() {
		if it.E.I == i {
			found = true
			break
		}
	}
	e := it.E
	it.Close()
	if !found {
		return fi, errors.New("no such file in this copy")
	}
	fi.FileRow = FileRow{I: e.I, Path: e.P, Size: e.S, Status: rowStatus(st)}
	if l.file == e.P && l.stage != "" && phase != "done" {
		fi.Status, fi.Stage, fi.Done = "working", l.stage, l.done
	}
	fi.Mtime = time.Unix(0, e.M)
	fi.Source = j.source().Path(e.P)
	fi.Dest = filepath.Join(spec.Dest, filepath.FromSlash(e.P))
	if d, err := os.Stat(fi.Dest); err == nil {
		fi.DestExists, fi.DestSize = true, d.Size()
	}
	fi.Events = []Event{}
	ReadJournal(j.path("journal.log"), 0, func(r Rec) bool {
		if r.I != i {
			return true
		}
		for _, h := range []struct {
			dst *string
			v   string
		}{{&fi.Hash.C, r.C}, {&fi.Hash.S1, r.S1}, {&fi.Hash.D1, r.D1}, {&fi.Hash.S2, r.S2}} {
			if h.v != "" {
				*h.dst = h.v
			}
		}
		if len(fi.Events) < maxEvents {
			ev := Event{Time: time.Unix(r.T, 0), What: eventText[r.St], Why: r.Err, Attempts: r.A, Ms: r.Ms}
			if ev.What == "" {
				ev.What = r.St
			}
			if r.Err != "" {
				fi.Why = r.Err
			}
			fi.Events = append(fi.Events, ev)
		}
		return true
	})
	switch fi.Status {
	case "strong":
		fi.Proof = "Strong: two separate reads of the source match what the destination holds"
	case "proven":
		fi.Proof = "Verified: the copy read back matches the source"
		if spec.S2 == "now" && phase != "done" {
			fi.Proof += "; the second source read is still to come"
		}
	case "failed":
		fi.Proof = "None: this file is not proven"
	case "warning":
		fi.Proof = "Verified against what was read, with a warning about the source"
	default:
		fi.Proof = "Not yet: this file has not been copied"
	}
	return fi, nil
}
