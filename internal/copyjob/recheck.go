package copyjob

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// A recheck reads one file of a copy again in full, the source or the copy
// at the destination, and records its fingerprint with the date. It is the
// file info's "Read again" button. It only reads: nothing is changed.
//
// Rechecks run in the queue's own loop, one at a time and never beside a
// copy, so a disk still has one operation in flight. Waiting rechecks are
// held in memory, at most maxRechecksWaiting, and are lost if Shuttle stops.
type Recheck struct {
	I       int       `json:"i"`
	Which   string    `json:"which"` // "source" or "dest"
	Time    time.Time `json:"time"`
	Hash    string    `json:"hash,omitempty"`
	Error   string    `json:"error,omitempty"`
	Changed bool      `json:"changed,omitempty"` // the source's size or modified time is not what was copied
}

// RecheckNow is a recheck waiting or being read, as file info shows it.
type RecheckNow struct {
	Which string `json:"which"`
	State string `json:"state"` // "waiting" or "reading"
	Done  int64  `json:"done"`
	Size  int64  `json:"size"`
}

const (
	maxRechecksWaiting = 20
	maxRechecksKept    = 500 // per copy, oldest dropped first
)

type recheckReq struct {
	id, which string
	i         int
	reading   bool
	start     int64 // byte counter when reading began, for progress
	size      int64
	j         *Job
}

var errRecheckFull = errors.New("too many files are already waiting to be read again; try once some have finished")

// AskRecheck queues file i of copy id to be read again: which is "source"
// or "dest".
func (m *Manager) AskRecheck(id string, i int, which string) error {
	if which != "source" && which != "dest" {
		return errors.New("choose the source or the copy")
	}
	j := m.find(id)
	if j == nil {
		return errors.New("this copy no longer exists")
	}
	// A Verify item knows the source only when it reads it again itself.
	if j.st.Spec.isVerify() && which == "source" && !j.st.Spec.readsSource() {
		return errors.New("this Verify item checks the copy only, so it has no source to read")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rechecks {
		if r.id == id && r.i == i && r.which == which {
			return nil // already asked
		}
	}
	if len(m.rechecks) >= maxRechecksWaiting {
		return errRecheckFull
	}
	m.rechecks = append(m.rechecks, &recheckReq{id: id, i: i, which: which, j: j})
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

// recheckFor lists what is waiting or being read for file i of job id.
func (m *Manager) recheckFor(id string, i int) []RecheckNow {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RecheckNow
	for _, r := range m.rechecks {
		if r.id != id || r.i != i {
			continue
		}
		n := RecheckNow{Which: r.which, State: "waiting"}
		if r.reading {
			n.State, n.Size = "reading", r.size
			n.Done = min(r.size, max(0, r.counter()-r.start))
		}
		out = append(out, n)
	}
	return out
}

func (r *recheckReq) counter() int64 {
	if r.which == "source" {
		return r.j.srcBytes.Load()
	}
	return r.j.dstBytes.Load()
}

// runRechecks does every waiting recheck, one at a time. Called from the
// queue loop, so no copy runs meanwhile.
func (m *Manager) runRechecks(ctx context.Context) {
	for ctx.Err() == nil {
		m.mu.Lock()
		if len(m.rechecks) == 0 {
			m.mu.Unlock()
			return
		}
		r := m.rechecks[0]
		m.mu.Unlock()
		res := m.recheck(ctx, r)
		m.mu.Lock()
		m.rechecks = m.rechecks[1:]
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		r.j.addRecheck(res)
	}
}

func (m *Manager) recheck(ctx context.Context, r *recheckReq) Recheck {
	res := Recheck{I: r.i, Which: r.which, Time: time.Now()}
	e, ok := m.entry(r.j, r.i)
	if !ok {
		res.Error = "this file is not in the copy's list"
		return res
	}
	path := r.j.source().Path(e.P)
	if r.which == "dest" {
		path = filepath.Join(r.j.st.Spec.Dest, filepath.FromSlash(e.P))
	}
	d, err := m.Disks.For(path)
	if err != nil {
		res.Error = "the disk is not connected"
		return res
	}
	size, mtime, err := statSM(path)
	if err != nil {
		res.Error = "the file is not there"
		if r.which == "dest" {
			res.Error = "the copy is not at the destination under its real name"
		}
		return res
	}
	res.Changed = r.which == "source" && (size != e.S || mtime != e.M)
	m.mu.Lock()
	r.reading, r.size, r.start = true, size, r.counter()
	m.mu.Unlock()
	stage := "recheck-" + r.which
	h, err := r.j.hashFileOpt(ctx, env{budget: m.Budget, src: d, dst: d}, d, path, r.i, stage, r.which == "source", false)
	r.j.dropCheckpoint(r.i, stage)
	if err != nil {
		res.Error = "could not be read in full: " + err.Error()
		return res
	}
	res.Hash = h.String()
	return res
}

// entry finds file i in a job's manifest.
func (m *Manager) entry(j *Job, i int) (entry, bool) {
	listMu.Lock()
	defer listMu.Unlock()
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return entry{}, false
	}
	defer it.Close()
	for it.Next() {
		if it.E.I == i {
			return it.E, true
		}
	}
	return entry{}, false
}

func (j *Job) loadRechecks() []Recheck {
	var out []Recheck
	if b, err := os.ReadFile(j.path("rechecks.json")); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (j *Job) addRecheck(r Recheck) {
	j.recheckMu.Lock()
	defer j.recheckMu.Unlock()
	all := append(j.loadRechecks(), r)
	if len(all) > maxRechecksKept {
		all = all[len(all)-maxRechecksKept:]
	}
	if b, err := json.Marshal(all); err == nil {
		fsx.WriteFileAtomic(j.path("rechecks.json"), b, 0o600)
	}
}

// rechecksOf lists the recorded rechecks of file i, oldest first.
func (j *Job) rechecksOf(i int) []Recheck {
	j.recheckMu.Lock()
	defer j.recheckMu.Unlock()
	var out []Recheck
	for _, r := range j.loadRechecks() {
		if r.I == i {
			out = append(out, r)
		}
	}
	return out
}
