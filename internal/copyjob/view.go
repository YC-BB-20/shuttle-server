package copyjob

import (
	"time"
)

// View is what the page shows for a job.
type View struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Dest       string    `json:"dest"`
	S2         string    `json:"s2"`
	Hash       string    `json:"hash"`
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
		j.mu.Lock()
		st, c, l := j.st, j.c, j.live
		j.mu.Unlock()
		v := View{ID: st.ID, Source: st.Spec.Source, Dest: st.Spec.Dest, S2: st.Spec.S2, Hash: st.Spec.Hash, Created: st.Created,
			Finished: st.Finished, Phase: st.Phase, Files: st.Files, Bytes: st.Bytes, Skipped: st.Skipped,
			Verified: c.Verified, Identical: c.Identical, Failed: c.Failed, Strong: c.Strong, S2Warn: c.S2Warn,
			Proven: c.BytesProven, SameVolume: st.SameVolume, Scanned: l.scanned, ScannedB: l.scannedBytes}
		switch {
		case st.Phase == "done" && st.Choice == "stopped":
			v.State = "stopped"
		case st.Phase == "done":
			v.State = "finished"
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
