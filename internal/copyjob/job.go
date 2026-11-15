// Package copyjob is the disk-to-disk copy of DESIGN.md section 15. By
// default the source is hashed as it is read for the copy (C), the copy is
// flushed, then read back (D1) while the source is read a second time (S2),
// one read on each disk at once; the file gets its real name only when C, D1
// and S2 agree. The older scheme ("first") hashes the source in its own read
// before the copy (S1), compares S1 with D1, and optionally reads it again (S2).
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
	"github.com/YC-BB-20/shuttle-server/internal/location"
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
	stCopied   byte = 5 // copied and hashed (C), still under its temporary name, check pass to come
	stHashed   byte = 6 // copy only: copied, hashed (C) and given its real name, never read back
	stSrcOK    byte = 7 // a source-only Verify (S2Only): the source read again matches C; the copy was not read
)

// Spec is what the owner asked for.
type Spec struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
	S2     string `json:"s2"` // "now" or "skip" (since 0.2.15 also with Hash "copy" and "later"); a Verify job: "now" also reads the source again
	// Check (added in 0.2.15): "" reads the copy back in this job (D1, and S2
	// unless skipped); "none" copies and hashes only, the file gets its real
	// name after the copy. Verify, only with "none": "run" or "paused" queues
	// a Verify job for the copied files when this job finishes.
	Check  string `json:"check,omitempty"`
	Verify string `json:"verify,omitempty"`
	Hash   string `json:"hash,omitempty"` // "copy" (hash while copying), "later" (hash while copying, check everything in a second pass) or "first" (own read before); empty in jobs from before 0.2.3 means "first"
	// Only lists the chosen files and folders inside Source, by name; empty
	// means the whole folder.
	Only []string `json:"only,omitempty"`
	// Verify jobs (added in 0.2.9): Kind "verify", Source and Dest are the
	// folder checked, File one file in it (empty: the whole folder), From the
	// copy jobs whose stored hashes it compares with, oldest first.
	// SkipSystem (added in 0.2.22) leaves out system files such as
	// .DS_Store; SkipAppleDouble also "._" files. Copied from Settings when
	// the job is added, so a job keeps its rule after a restart.
	SkipSystem bool `json:"skip_system,omitempty"`
	// Algo (added in 0.2.58) is which hashes every read of this job makes:
	// "xxh128" (also "", every job before it), "xxh64" or "both". Copied from
	// Settings when the job is added.
	Algo            string   `json:"algo,omitempty"`
	SkipAppleDouble bool     `json:"skip_appledouble,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	File            string   `json:"file,omitempty"`
	From            []string `json:"from,omitempty"`
	// IgnoreSpace (request only, never saved): the owner chose "continue
	// anyway" after being told the copy may not fit.
	IgnoreSpace bool `json:"ignore_space,omitempty"`
	// Paused (request only, Verify jobs, v0.2.26): add the job paused.
	Paused bool `json:"paused,omitempty"`
}

// MaxOnly bounds how many files and folders can be picked one by one.
const MaxOnly = 1000

func (j *Job) source() location.Folder {
	return location.Folder{Dir: j.st.Spec.Source, Only: j.st.Spec.Only, System: j.st.Spec.SkipSystem, AppleDouble: j.st.Spec.SkipAppleDouble}
}

// Hash schemes.
const (
	HashDuringCopy = "copy"
	HashCheckLater = "later" // added in 0.2.8
	HashFirst      = "first"
)

// hashDuringCopy: the bytes read for the copy are hashed (C), in both the
// per-file scheme and the copy-then-check scheme.
func (s Spec) hashDuringCopy() bool { return s.Hash == HashDuringCopy || s.Hash == HashCheckLater }

// copyOnly: no read-back in this job; any check is a separate Verify job.
func (s Spec) copyOnly() bool { return s.Check == "none" }

// checkLater: every file is copied first; D1 and S2 run in a second pass.
func (s Spec) checkLater() bool { return s.Hash == HashCheckLater }

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
	Phase         string                `json:"phase"`  // scan, copy, check (copy-then-check only), s2, done; a Verify job: scan, verify, source (S2After only), done
	Files         int                   `json:"files"`
	Bytes         int64                 `json:"bytes"`
	Skipped       int                   `json:"skipped"`
	SystemSkipped int                   `json:"system_skipped,omitempty"` // system files left out (Settings)
	SourceVol     string                `json:"source_volume"`
	DestVol       string                `json:"dest_volume"`
	SameVolume    bool                  `json:"same_volume"`
	S2JournalEnd  int64                 `json:"s2_journal_end,omitempty"`
	CheckEnd      int64                 `json:"check_journal_end,omitempty"` // journal size when the check pass began
	Checkpoints   map[string]Checkpoint `json:"checkpoints,omitempty"`
	Finished      time.Time             `json:"finished,omitzero"`
	CopyRate      int64                 `json:"copy_rate,omitempty"`  // average while copying in the last run, kept for history (v0.2.55)
	WorkNs        int64                 `json:"work_ns,omitempty"`    // time spent running, all runs added up: not paused, waiting or queued (v0.2.56)
	VerifyJob     string                `json:"verify_job,omitempty"` // the Verify job that follows this copy
	After         string                `json:"after,omitempty"`      // a Verify job: the copy it waits for (added with it, v0.2.16)
	// Free space (added in 0.2.24): the bytes to write counted when the job
	// was added (nil: not known), the owner's "continue anyway", and the
	// question waiting for an answer when the copy no longer fits.
	SizeAtAdd *int64      `json:"size_at_add,omitempty"`
	SpaceOK   bool        `json:"space_ok,omitempty"`
	SpaceAsk  *SpaceShort `json:"space_ask,omitempty"`
	// Error (added in 0.2.26): why a Verify job stopped on its own, such as
	// a folder that is missing. The job is done and shows as failed.
	Error string `json:"error,omitempty"`
	// Hidden (added in 0.2.30): the owner hid this finished entry from the
	// history. Only the page's view changes; nothing on disk is touched.
	Hidden bool `json:"hidden,omitempty"`
}

// Job is one copy job.
type Job struct {
	dir string

	mu     sync.Mutex
	saveMu sync.Mutex // orders writes of job.json
	st     state
	status []byte
	// already marks files that were found at the destination and compared,
	// not copied: one bit per file, made on the first such file.
	already []uint64
	c       counts
	live    live

	srcBytes, dstBytes atomic.Int64 // for speed
	recheckMu          sync.Mutex   // rechecks.json
	// Copy speed: bytes written by the copy pass and the time spent in it, so
	// the copy's own speed is not mixed with the read-back and re-read.
	copyBytes, copyNs, copySince atomic.Int64 // copySince: unix ns the current copy pass began, 0 if none
	workSince                    atomic.Int64 // unix ns this run began, 0 when not running
}

// startWork starts the job's work clock for a run.
func (j *Job) startWork(now time.Time) { j.workSince.Store(now.UnixNano()) }

// endWorkLocked adds the running time of this run to WorkNs and stops the
// clock; it says whether there was any. The caller holds j.mu.
func (j *Job) endWorkLocked(now time.Time) bool {
	s := j.workSince.Swap(0)
	if s == 0 {
		return false
	}
	j.st.WorkNs += max(0, now.UnixNano()-s)
	return true
}

// workClock is the time the job has spent running, with the run going now.
func workClock(st state, since int64, now time.Time) int64 {
	if since != 0 {
		return st.WorkNs + max(0, now.UnixNano()-since)
	}
	return st.WorkNs
}

// copyClock is the bytes written by copy passes and the time spent in them,
// including the copy pass running now.
func (j *Job) copyClock(now time.Time) (int64, int64) {
	ns := j.copyNs.Load()
	if s := j.copySince.Load(); s != 0 {
		ns += now.UnixNano() - s
	}
	return j.copyBytes.Load(), ns
}

type counts struct {
	Verified, Identical, Failed, Strong, S2Warn int
	BytesProven                                 int64
	BytesFailed                                 int64 // Verify jobs: bytes of files that did not match, for the bar
	Copied                                      int   // copy-then-check: copied, waiting for the check pass
	Hashed                                      int   // copy only: copied and hashed, not read back
	BytesCopied                                 int64 // copy-then-check: bytes through the copy pass
}

type live struct {
	reason       string // why it is waiting, or empty
	missing      string // "source", "destination" or "both" when a disk is not connected
	file         string
	stage        string
	done         int64
	size         int64
	temp         string // current partial file, removed on Stop
	scanned      int
	scannedBytes int64
	// A Verify job's pass over the source after its copies (S2After).
	s2done, s2total int
}

func (j *Job) path(name string) string { return filepath.Join(j.dir, name) }

func (j *Job) save() error {
	// One save at a time, snapshot and write together, so an older snapshot
	// can never land after a newer one (a Hide right after a finish was lost).
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
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
// setAlready marks file i as found at the destination and compared, not copied.
func (j *Job) setAlready(i int) {
	if j.already == nil {
		j.already = make([]uint64, (len(j.status)+63)/64)
	}
	if i/64 < len(j.already) {
		j.already[i/64] |= 1 << (i % 64)
	}
}

// isAlready reports whether bits mark file i as already at the destination.
func isAlready(bits []uint64, i int) bool {
	return i >= 0 && i/64 < len(bits) && bits[i/64]&(1<<(i%64)) != 0
}

func (j *Job) apply(r Rec) {
	if r.I < 0 || r.I >= len(j.status) {
		return
	}
	unproven := j.status[r.I] == stPending || j.status[r.I] == stFailed || j.status[r.I] == stCopied
	if j.status[r.I] == stCopied && r.St != "copied" {
		j.c.Copied-- // its check is done, one way or the other
	}
	switch r.St {
	case "copied": // copy-then-check: copied and hashed, not yet proven
		if j.status[r.I] == stPending || j.status[r.I] == stFailed {
			j.c.BytesCopied += r.N
		}
		j.status[r.I] = stCopied
		j.c.Copied++
	case "verified", "identical", "match": // match: a Verify job read the copy again and it matches
		if unproven {
			j.c.BytesProven += r.N
			if (j.st.Spec.checkLater() || j.st.Spec.copyOnly()) && j.status[r.I] != stCopied {
				j.c.BytesCopied += r.N
			}
		}
		j.status[r.I] = stVerified
		if r.St == "identical" {
			j.setAlready(r.I)
			j.c.Identical++
		} else {
			j.c.Verified++
		}
	case "strong": // hashed while copying: C, D1 and S2 agree, in one record
		if unproven {
			j.c.BytesProven += r.N
			if j.st.Spec.checkLater() && j.status[r.I] != stCopied {
				j.c.BytesCopied += r.N
			}
		}
		j.status[r.I] = stStrong
		j.c.Verified++
		j.c.Strong++
	case "hashed": // copy only: named after the copy, never read back
		if unproven {
			j.c.BytesCopied += r.N
		}
		j.status[r.I] = stHashed
		j.c.Hashed++
	case "failed":
		j.status[r.I] = stFailed
		j.c.Failed++
		if j.st.Spec.isVerify() {
			j.c.BytesFailed += r.N
		}
	case "s2ok":
		j.status[r.I] = stStrong
		j.c.Strong++
	case "srcok": // a source-only Verify job (S2Only), v0.2.27
		j.status[r.I] = stSrcOK
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

func (j *Job) dropCheckpoint(i int, stage string) {
	j.mu.Lock()
	delete(j.st.Checkpoints, ckKey(i, stage))
	j.mu.Unlock()
}

func (j *Job) dropCheckpoints(i int) {
	j.mu.Lock()
	for _, s := range []string{"s1", "copy", "d1", "s2"} {
		delete(j.st.Checkpoints, ckKey(i, s))
	}
	j.mu.Unlock()
}
