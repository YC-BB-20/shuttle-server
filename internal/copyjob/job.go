// Package copyjob is the disk-to-disk copy of DESIGN.md section 15: the
// source is read and hashed (S1), copied with no hashing in the copy path,
// the copy is flushed and read back (D1), and S1 must equal D1 before the
// file gets its real name. Optionally the source is read again (S2).
package copyjob

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// FormatVersion of job.json, manifest.jsonl and journal.log.
const FormatVersion = 1

// File statuses in the in-memory table: one byte per file, nothing more.
const (
	stPending  byte = 0
	stVerified byte = 1 // S1 equals D1
	stFailed   byte = 2
	stStrong   byte = 3 // and S2 equals D1
	stS2Warn   byte = 4 // S2 differed: source changed or unstable
)

// Spec is what the owner asked for.
type Spec struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
	S2     string `json:"s2"` // "now" or "skip"
}

// Checkpoint is where a paused or interrupted step stopped.
type Checkpoint struct {
	Index  int    `json:"i"`
	Stage  string `json:"stage"` // s1, copy, d1, s2
	Offset int64  `json:"offset"`
	State  []byte `json:"state,omitempty"` // saved hasher
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
}

// state is job.json, always written atomically.
type state struct {
	FormatVersion int                   `json:"format_version"`
	ID            string                `json:"id"`
	Spec          Spec                  `json:"spec"`
	Created       time.Time             `json:"created"`
	Choice        string                `json:"choice"` // run, paused, stopped: the owner's choice
	Phase         string                `json:"phase"`  // scan, copy, s2, done
	Files         int                   `json:"files"`
	Bytes         int64                 `json:"bytes"`
	Skipped       int                   `json:"skipped"`
	SourceVol     string                `json:"source_volume"`
	DestVol       string                `json:"dest_volume"`
	SameVolume    bool                  `json:"same_volume"`
	S2JournalEnd  int64                 `json:"s2_journal_end,omitempty"`
	Checkpoints   map[string]Checkpoint `json:"checkpoints,omitempty"`
	Finished      time.Time             `json:"finished,omitzero"`
}

// Job is one copy job.
type Job struct {
	dir string

	mu     sync.Mutex
	st     state
	status []byte
	c      counts
	live   live

	srcBytes, dstBytes atomic.Int64 // for speed
}

type counts struct {
	Verified, Identical, Failed, Strong, S2Warn int
	BytesProven                                 int64
}

type live struct {
	reason       string // why it is waiting, or empty
	file         string
	stage        string
	done         int64
	size         int64
	temp         string // current partial file, removed on Stop
	scanned      int
	scannedBytes int64
}

func (j *Job) path(name string) string { return filepath.Join(j.dir, name) }

func (j *Job) save() error {
	j.mu.Lock()
	b, err := json.MarshalIndent(j.st, "", "  ")
	j.mu.Unlock()
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(j.path("job.json"), b, 0o600)
}

func loadJob(dir string) (*Job, error) {
	b, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if err != nil {
		return nil, err
	}
	j := &Job{dir: dir}
	if err := json.Unmarshal(b, &j.st); err != nil {
		return nil, fmt.Errorf("job.json damaged: %w", err)
	}
	if j.st.FormatVersion > FormatVersion {
		return nil, errors.New("written by a newer Shuttle Server")
	}
	return j, nil
}

// apply updates the table and counters from a journal record.
func (j *Job) apply(r Rec) {
	if r.I < 0 || r.I >= len(j.status) {
		return
	}
	switch r.St {
	case "verified", "identical":
		if j.status[r.I] == stPending || j.status[r.I] == stFailed {
			j.c.BytesProven += r.N
		}
		j.status[r.I] = stVerified
		if r.St == "identical" {
			j.c.Identical++
		} else {
			j.c.Verified++
		}
	case "failed":
		j.status[r.I] = stFailed
		j.c.Failed++
	case "s2ok":
		j.status[r.I] = stStrong
		j.c.Strong++
	case "s2changed", "s2unstable":
		j.status[r.I] = stS2Warn
		j.c.S2Warn++
	}
}

func (j *Job) setLive(f func(*live)) {
	j.mu.Lock()
	f(&j.live)
	j.mu.Unlock()
}

func ckKey(i int, stage string) string { return fmt.Sprintf("%d:%s", i, stage) }

func (j *Job) checkpoint(i int, stage string) (Checkpoint, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	c, ok := j.st.Checkpoints[ckKey(i, stage)]
	return c, ok
}

func (j *Job) saveCheckpoint(c Checkpoint) error {
	j.mu.Lock()
	if j.st.Checkpoints == nil {
		j.st.Checkpoints = map[string]Checkpoint{}
	}
	j.st.Checkpoints[ckKey(c.Index, c.Stage)] = c
	j.mu.Unlock()
	return j.save()
}

func (j *Job) dropCheckpoints(i int) {
	j.mu.Lock()
	for _, s := range []string{"s1", "copy", "d1", "s2"} {
		delete(j.st.Checkpoints, ckKey(i, s))
	}
	j.mu.Unlock()
}
