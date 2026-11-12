package copyjob

import (
	"errors"
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
}

type rateMemo struct {
	t        time.Time
	src, dst int64
	sr, dr   int64
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
				r = &rateMemo{t: now, src: j.srcBytes.Load(), dst: j.dstBytes.Load()}
				memo[j] = r
			}
			if dt := now.Sub(r.t).Seconds(); dt >= 1 {
				s, d := j.srcBytes.Load(), j.dstBytes.Load()
				r.sr, r.dr = int64(float64(s-r.src)/dt), int64(float64(d-r.dst)/dt)
				r.t, r.src, r.dst = now, s, d
			}
			v.SrcRate, v.DstRate = r.sr, r.dr
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
		Proven: c.BytesProven, SameVolume: st.SameVolume, Scanned: l.scanned, ScannedB: l.scannedBytes}
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
