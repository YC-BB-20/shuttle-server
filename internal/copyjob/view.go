package copyjob

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// View is what the page shows for a job.
type View struct {
	ID         string      `json:"id"`
	Source     string      `json:"source"`
	Dest       string      `json:"dest"`
	S2         string      `json:"s2"`
	Hash       string      `json:"hash"`
	Only       int         `json:"only,omitempty"` // how many files and folders were picked one by one; 0 means the whole folder
	Kind       string      `json:"kind,omitempty"` // "verify" for a Verify job
	VerifyFile string      `json:"verify_file,omitempty"`
	Created    time.Time   `json:"created"`
	Finished   time.Time   `json:"finished,omitzero"`
	State      string      `json:"state"` // running, waiting, paused, queued, finished, stopped
	Reason     string      `json:"reason,omitempty"`
	Missing    string      `json:"missing,omitempty"` // source, destination or both: a disk is not connected
	Phase      string      `json:"phase"`
	Stage      string      `json:"stage,omitempty"`
	File       string      `json:"file,omitempty"`
	FileDone   int64       `json:"file_done"`
	FileSize   int64       `json:"file_size"`
	Files      int         `json:"files"`
	Bytes      int64       `json:"bytes"`
	Scanned    int         `json:"scanned"`
	ScannedB   int64       `json:"scanned_bytes"`
	Verified   int         `json:"verified"`
	Identical  int         `json:"identical"`
	Failed     int         `json:"failed"`
	Strong     int         `json:"strong"`
	S2Warn     int         `json:"s2_warnings"`
	Proven     int64       `json:"bytes_proven"`
	Copied     int         `json:"copied,omitempty"`      // copy-then-check: copied, waiting for the check pass
	Hashed     int         `json:"hashed"`                // copy only: copied and hashed, not read back
	Check      string      `json:"check,omitempty"`       // "none": copy only in this job
	VerifyNext string      `json:"verify_next,omitempty"` // "run" or "paused": a Verify job follows this copy
	After      string      `json:"after,omitempty"`       // a Verify job that starts when this copy has finished
	VerifyJob  string      `json:"verify_job,omitempty"`
	Skipped    int         `json:"skipped"`
	System     int         `json:"system_skipped"`
	SameVolume bool        `json:"same_volume"`
	SrcRate    int64       `json:"source_rate"`
	DstRate    int64       `json:"dest_rate"`
	Progress   float64     `json:"progress"`              // 0 to 1, all reads and writes the job needs
	CopiedPart float64     `json:"copied_part"`           // a copy: 0 to 1 of its bytes written so far (the purple layer)
	ProvenPart float64     `json:"proven_part"`           // a copy: 0 to 1 of its bytes read back and matching (the pink layer)
	Rate       int64       `json:"rate,omitempty"`        // overall bytes of the job finished per second, smoothed
	CopyRate   int64       `json:"copy_rate,omitempty"`   // average speed of all copying in this run, 0 until measured
	WorkMs     int64       `json:"work_ms,omitempty"`     // time spent running so far, all runs; 0 in jobs finished before v0.2.56
	ETA        int64       `json:"eta_seconds,omitempty"` // time left, when it can be estimated
	Space      *SpaceShort `json:"space,omitempty"`       // paused because it may not fit: the question for the owner
	Error      string      `json:"error,omitempty"`       // a Verify job stopped on its own: a folder is missing
	Checks     *Checks     `json:"checks,omitempty"`      // a finished copy: what has been read back since
	From       []string    `json:"from,omitempty"`        // a Verify job: the copies it compares with
	Hidden     bool        `json:"hidden,omitempty"`      // hidden from the history by the owner
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
		return float64(c.Verified+c.Identical+c.Failed+c.Hashed) / float64(st.Files)
	}
	size, done := float64(l.size), float64(l.done)
	if st.Spec.isVerify() {
		f := min(1, (float64(c.BytesProven+c.BytesFailed)+done)/float64(st.Bytes))
		if st.Spec.S2 == S2Only && st.Spec.readsSource() {
			if st.Phase != "source" {
				return 0
			}
			part := 0.0
			if size > 0 {
				part = done / size
			}
			return min(1, (float64(l.s2done)+part)/float64(max(1, l.s2total)))
		}
		if st.Spec.S2 != S2After || !st.Spec.readsSource() {
			return f
		}
		// Copies read back first (half the bar), then the source read again.
		if st.Phase != "source" {
			return f / 2
		}
		part := 0.0
		if size > 0 {
			part = done / size
		}
		return 0.5 + 0.5*min(1, (float64(l.s2done)+part)/float64(max(1, l.s2total)))
	}
	has := func(p string) bool { return strings.HasPrefix(l.stage, p) }
	var part float64
	if st.Spec.copyOnly() {
		if has("Copying") {
			part = done
		}
		return min(1, (float64(c.BytesCopied)+part)/float64(st.Bytes))
	}
	if st.Spec.checkLater() {
		// The copy pass is the first half of the work, the check pass the second.
		if has("Copying") || has("Reading the source before copying") || has("Reading back") || has("Checking") {
			part = done / 2
		}
		return min(1, (float64(c.BytesCopied)+float64(c.BytesProven))/2/float64(st.Bytes)+part/float64(st.Bytes))
	}
	if st.Spec.hashDuringCopy() {
		switch {
		case has("Copying"), has("Reading the source before copying"):
			part = done / 2
		case has("Reading back"), has("Checking"):
			part = size/2 + done/2
		}
	} else {
		switch {
		case has("Reading the source before copying"):
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

// layers is a copy's bar in two parts (v0.2.54): how much of it is written
// and how much is read back and matching, both from 0 to 1 of the job's bytes.
// The file being worked on counts as it goes: while it is copied it grows the
// written part, while it is read back it grows the proven part.
func layers(st state, c counts, l live) (copied, proven float64) {
	if st.Spec.isVerify() || st.Phase == "scan" {
		return 0, 0
	}
	if st.Bytes <= 0 {
		if st.Phase == "done" {
			return 1, 1
		}
		return 0, 0
	}
	b := float64(st.Bytes)
	cb, pb := float64(max(c.BytesCopied, c.BytesProven)), float64(c.BytesProven)
	has := func(p string) bool { return strings.HasPrefix(l.stage, p) }
	switch {
	case st.Phase == "done" && st.Choice != "stopped":
		cb = b
	case has("Copying"):
		cb += float64(l.done)
	case has("Reading back"), has("Checking"):
		if !st.Spec.checkLater() { // copy-then-check already counted it in the copy pass
			cb += float64(l.size)
		}
		pb += float64(l.done)
	case st.Phase == "s2", st.Phase == "source":
		cb = b // the earlier scheme's second source read: every file is written
	}
	if st.Spec.copyOnly() {
		pb = 0
	}
	return min(1, cb/b), min(1, pb/b, cb/b)
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
		if v.After != "" {
			m.mu.Lock()
			if !m.unfinishedLocked(v.After) {
				v.After = "" // its copy has finished: nothing to wait for
			}
			m.mu.Unlock()
		}
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
			v.CopyRate = copyAverage(j.copyClock(now))
			if r.rate > 0 && st.Phase != "scan" {
				v.ETA = int64((1 - v.Progress) * float64(st.Bytes) / r.rate)
			}
			m.mu.Unlock()
		case l.reason != "":
			v.State, v.Reason, v.Missing = "waiting", l.reason, l.missing
		default:
			v.State = "queued"
		}
		if v.State == "finished" || v.State == "stopped" {
			if finished++; finished > 20 {
				continue
			}
			withChecks(jobs, running, j, &v)
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
		Kind: st.Spec.Kind, VerifyFile: st.Spec.File,
		Created: st.Created, Finished: st.Finished, CopyRate: st.CopyRate, Phase: st.Phase, Files: st.Files, Bytes: st.Bytes, Skipped: st.Skipped, System: st.SystemSkipped,
		Verified: c.Verified, Identical: c.Identical, Failed: c.Failed, Strong: c.Strong, S2Warn: c.S2Warn, Copied: c.Copied, Hashed: c.Hashed,
		Check: st.Spec.Check, VerifyNext: st.Spec.Verify, VerifyJob: st.VerifyJob, After: st.After,
		Proven: c.BytesProven, SameVolume: st.SameVolume, Scanned: l.scanned, ScannedB: l.scannedBytes,
		Progress: progress(st, c, l), Space: st.SpaceAsk, Error: st.Error, From: st.Spec.From, Hidden: st.Hidden}
	v.CopiedPart, v.ProvenPart = layers(st, c, l)
	v.WorkMs = workClock(st, j.workSince.Load(), time.Now()) / 1e6
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

// Checks says what has been read back for a finished copy, by the copy
// itself or by Verify jobs over it since (v0.2.27): files whose copy was
// read back and matched (Dest) and files whose source was read again and
// matched (Source), each with when, out of Of files.
type Checks struct {
	Of       int       `json:"of"`
	Dest     int       `json:"dest"`
	DestAt   time.Time `json:"dest_at,omitzero"`
	Source   int       `json:"source"`
	SourceAt time.Time `json:"source_at,omitzero"`
	Queued   bool      `json:"queued,omitempty"` // a Verify of this copy has not finished yet
	// Checking is set while a Verify of this copy is running: how many files
	// it has done so far and how many it checks in all (v0.2.39).
	Checking   bool `json:"checking,omitempty"`
	CheckDone  int  `json:"check_done,omitempty"`
	CheckMatch int  `json:"check_match,omitempty"`
	CheckOf    int  `json:"check_of,omitempty"`
	CheckSrc   bool `json:"check_src,omitempty"` // it also reads the source again
}

// checksOf builds a copy's Checks from its own counts and from every Verify
// job that compared with its stored hashes. Counts are the best single run,
// never added up, and never more than the copy's files.
func checksOf(jobs []*Job, running *Job, st state, c counts) *Checks {
	k := &Checks{Of: st.Files}
	if n := c.Verified + c.Identical; n > 0 {
		k.Dest, k.DestAt = n, st.Finished
	}
	if c.Strong > 0 {
		k.Source, k.SourceAt = c.Strong, st.Finished
	}
	for _, o := range jobs {
		o.mu.Lock()
		ost, oc := o.st, o.c
		o.mu.Unlock()
		if !ost.Spec.isVerify() || !slices.Contains(ost.Spec.From, st.ID) || ost.Choice == "stopped" && ost.Phase == "done" && oc.Verified+oc.Strong == 0 {
			continue
		}
		if ost.Phase != "done" {
			if o == running && ost.Phase != "scan" {
				k.Checking, k.CheckOf, k.CheckSrc = true, ost.Files, ost.Spec.S2 == "now" || ost.Spec.S2 == "after" || ost.Spec.S2 == "only"
				k.CheckDone, k.CheckMatch = oc.Verified+oc.Failed, oc.Verified // a match on the destination counts in Verified
				if ost.Spec.S2 == "only" {
					k.CheckDone, k.CheckMatch = oc.Strong+oc.Failed, oc.Strong
				}
			} else {
				k.Queued = true
			}
			continue
		}
		if n := min(oc.Verified, k.Of); n > k.Dest || n == k.Dest && n > 0 && ost.Finished.After(k.DestAt) {
			k.Dest, k.DestAt = n, ost.Finished
		}
		if n := min(oc.Strong, k.Of); n > k.Source || n == k.Source && n > 0 && ost.Finished.After(k.SourceAt) {
			k.Source, k.SourceAt = n, ost.Finished
		}
	}
	return k
}

// withChecks adds Checks to the view of a finished or stopped copy.
func withChecks(jobs []*Job, running *Job, j *Job, v *View) {
	if v.Kind == KindVerify || (v.State != "finished" && v.State != "stopped") {
		return
	}
	j.mu.Lock()
	st, c := j.st, j.c
	j.mu.Unlock()
	v.Checks = checksOf(jobs, running, st, c)
}

// History lists finished and stopped copies, newest first, skipping the
// newest skip of them.
func (m *Manager) History(skip, limit int) []View {
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	running := m.running
	m.mu.Unlock()
	out := []View{}
	for i := len(jobs) - 1; i >= 0 && len(out) < limit; i-- {
		v, _, _ := baseView(jobs[i])
		if v.State == "" {
			continue
		}
		withChecks(jobs, running, jobs[i], &v)
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
	// Counts holds how many files each filter shows: all, todo, done (verified),
	// unverified (copied, not read back), problems.
	Counts map[string]int `json:"counts"`
	// Folders are the folders directly inside the folder asked for, when the
	// list is shown as folders (v0.2.46). Rows then hold only the files
	// directly inside it. Only on the first page.
	Folders []FolderRow `json:"folders,omitempty"`
	// MoreFolders: there were more folders than MaxFolders; the rest are left out.
	MoreFolders bool `json:"more_folders,omitempty"`
}

// FolderRow is one folder of a job's file list, with how its files stand,
// counting every file inside it at any depth.
type FolderRow struct {
	Name     string `json:"name"`
	Path     string `json:"path"`  // relative to the job's top, with "/"
	Files    int    `json:"files"` // every file inside, at any depth
	Match    int    `json:"match"` // of those, the ones the chosen filter shows
	Strong   int    `json:"strong"`
	Dest     int    `json:"dest"`   // destination read back, source not read again
	Source   int    `json:"source"` // only the source read again
	Todo     int    `json:"todo"`
	Hashed   int    `json:"hashed"` // copied, never read back
	Problems int    `json:"problems"`
}

// MaxFolders bounds how many folders one level of the list returns, so a
// folder holding thousands of folders cannot make a page grow without end.
const MaxFolders = 500

// add counts one file of status in a folder row.
func (f *FolderRow) add(status string, match bool) {
	f.Files++
	if match {
		f.Match++
	}
	switch status {
	case "strong", "already2":
		f.Strong++
	case "proven", "already":
		f.Dest++
	case "source":
		f.Source++
	case "hashed":
		f.Hashed++
	case "failed", "warning":
		f.Problems++
	default:
		f.Todo++
	}
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
	case stCopied:
		return "copied"
	case stHashed:
		return "hashed"
	case stSrcOK:
		return "source"
	case stCheckWait:
		return "checkwait"
	}
	return "waiting"
}

// alreadyStatus turns a verified file that was found at the destination,
// compared and not copied into "already" (or "already2" once the source was
// also read a second time), so the page can say so. A later failure wins.
func alreadyStatus(status string, already bool) string {
	if already && status == "proven" {
		return "already"
	}
	if already && status == "strong" {
		return "already2"
	}
	return status
}

func filterOK(filter, status string) bool {
	switch filter {
	case "todo":
		return status == "waiting" || status == "working" || status == "copied" || status == "checkwait"
	case "done": // verified: the copy was read back (or, in a source-only Verify, the source matched)
		return status == "proven" || status == "strong" || status == "source" || status == "already" || status == "already2"
	case "unverified": // copied and hashed, never read back
		return status == "hashed"
	case "problems":
		return status == "failed" || status == "warning"
	}
	return true
}

// Files lists up to limit files of a job from index from on, matching filter
// (all, todo, done, unverified, problems). It reads the manifest from disk and holds only
// the page in memory.
func (m *Manager) Files(id, filter string, from, limit int) (FilePage, error) {
	return m.FilesIn(id, filter, false, "", from, limit)
}

// FilesIn is Files, optionally as folders: with tree set, only the files
// directly inside dir (relative, "" for the top) are listed, and the first
// page also lists the folders directly inside dir with counts of every file
// within them. The manifest is read once; the folders held are bounded by
// MaxFolders.
func (m *Manager) FilesIn(id, filter string, tree bool, dir string, from, limit int) (FilePage, error) {
	page := FilePage{Rows: []FileRow{}, Next: -1}
	dir = strings.Trim(dir, "/")
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	var folders map[string]*FolderRow
	if tree && from == 0 {
		folders = map[string]*FolderRow{}
	}
	j := m.find(id)
	if j == nil {
		return page, errors.New("this copy no longer exists")
	}
	if limit <= 0 || limit > MaxFilePage {
		limit = MaxFilePage
	}
	listMu.Lock()
	defer listMu.Unlock()
	table, _ := m.mergedStatus(j)
	vfile, vstage, vdone := m.verifyLive(j)
	j.mu.Lock()
	phase, l, already := j.st.Phase, j.live, slices.Clone(j.already)
	j.mu.Unlock()
	page.Counts = map[string]int{"all": len(table), "todo": 0, "done": 0, "unverified": 0, "problems": 0}
	for i, st := range table { // the compact table: a byte per file
		for _, f := range []string{"todo", "done", "unverified", "problems"} {
			if filterOK(f, alreadyStatus(rowStatus(st), isAlready(already, i))) {
				page.Counts[f]++
			}
		}
	}
	if phase == "scan" {
		page.Note = "The source is still being scanned; the file list appears when the scan finishes."
		return page, nil
	}
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
		st := stPending
		if e.I < len(table) {
			st = table[e.I]
		}
		r := FileRow{I: e.I, Path: e.P, Size: e.S, Status: alreadyStatus(rowStatus(st), isAlready(already, e.I))}
		if l.file == e.P && l.stage != "" && phase != "done" {
			r.Status, r.Stage, r.Done = "working", l.stage, l.done
		} else if vfile == e.P && vstage != "" {
			r.Status, r.Stage, r.Done = "working", vstage, vdone // a Verify of this copy is reading it now
		}
		if tree {
			if !strings.HasPrefix(e.P, prefix) {
				continue
			}
			if k := strings.IndexByte(e.P[len(prefix):], '/'); k >= 0 {
				if folders != nil {
					name := e.P[len(prefix) : len(prefix)+k]
					fr := folders[name]
					if fr == nil && len(folders) < MaxFolders {
						fr = &FolderRow{Name: name, Path: prefix + name}
						folders[name] = fr
					} else if fr == nil {
						page.MoreFolders = true
					}
					if fr != nil {
						fr.add(r.Status, filterOK(filter, r.Status))
					}
				}
				continue
			}
		}
		if !filterOK(filter, r.Status) {
			continue
		}
		if len(page.Rows) == limit && folders != nil {
			if page.Next < 0 {
				page.Next = e.I
			}
			continue // keep counting the folders
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
	for _, fr := range folders {
		page.Folders = append(page.Folders, *fr)
	}
	slices.SortFunc(page.Folders, func(a, b FolderRow) int { return strings.Compare(a.Name, b.Name) })
	if len(need) > 0 {
		ReadJournal(j.path("journal.log"), 0, func(rec Rec) bool {
			if k, ok := need[rec.I]; ok && rec.Err != "" {
				page.Rows[k].Why = rec.Err
			}
			return true
		})
		for _, k := range need {
			if page.Rows[k].Why == "" {
				page.Rows[k].Why = "a later Verify of this copy found a problem with this file; open that Verify in the history to see why"
			}
		}
	}
	return page, nil
}

// verifyLive says which of copy j's files a running Verify of that copy is
// reading now, as a path relative to the copy's destination, with its stage
// and bytes done. Empty when no such Verify is running.
func (m *Manager) verifyLive(j *Job) (file, stage string, done int64) {
	m.mu.Lock()
	o := m.running
	m.mu.Unlock()
	if o == nil || o == j {
		return "", "", 0
	}
	j.mu.Lock()
	id, dest := j.st.ID, j.st.Spec.Dest
	j.mu.Unlock()
	o.mu.Lock()
	ost, l := o.st, o.live
	o.mu.Unlock()
	if !ost.Spec.isVerify() || len(ost.Spec.From) != 1 || ost.Spec.From[0] != id || l.file == "" || l.stage == "" {
		return "", "", 0
	}
	rel, err := filepath.Rel(dest, filepath.Join(ost.Spec.Dest, filepath.FromSlash(l.file)))
	if err != nil {
		return "", "", 0
	}
	return filepath.ToSlash(rel), l.stage, l.done
}

// Flags of one file in a merged table: what has been proven about it.
const (
	mfDest    byte = 1 << iota // the copy was read back and matches
	mfSrc                      // the source was read a second time and matches
	mfFailed                   // the latest check of it failed
	mfWarn                     // the source changed after the copy
	mfTouched                  // a later Verify job checked it
	mfPending                  // a Verify of this copy that has not finished will check it
)

// stCheckWait only appears in merged tables: a copied file that a queued or
// running Verify of its copy has still to check.
const stCheckWait byte = 9

// mergedStatus is a copy's file table with the results of every later
// Verify job of that copy laid on top (v0.2.31), so a copy made without
// checks shows "Verified" once a Verify read its destination back, and
// "Verified · read twice" once another read its source again. Verify jobs
// are taken oldest first, so the latest result of each check wins. Only
// Verify jobs of this one copy count; their manifests list a subset of the
// copy's files in the same order, so both are walked side by side and
// nothing held grows beyond the copy's own byte-per-file table. Later says
// whether any Verify job contributed. The caller holds listMu.
func (m *Manager) mergedStatus(j *Job) ([]byte, bool) {
	j.mu.Lock()
	table := slices.Clone(j.status)
	id, dest, isVer := j.st.ID, j.st.Spec.Dest, j.st.Spec.isVerify()
	j.mu.Unlock()
	if isVer || len(table) == 0 {
		return table, false
	}
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	var flags []byte
	for _, o := range jobs {
		o.mu.Lock()
		ost, ostatus := o.st, slices.Clone(o.status)
		o.mu.Unlock()
		if !ost.Spec.isVerify() || len(ost.Spec.From) != 1 || ost.Spec.From[0] != id || ost.Phase == "scan" || len(ostatus) == 0 {
			continue
		}
		if flags == nil {
			flags = make([]byte, len(table))
			for i, st := range table {
				switch st {
				case stVerified:
					flags[i] = mfDest
				case stStrong:
					flags[i] = mfDest | mfSrc
				case stSrcOK:
					flags[i] = mfSrc
				case stFailed:
					flags[i] = mfFailed
				case stS2Warn:
					flags[i] = mfDest | mfWarn
				}
			}
		}
		layVerify(j.path("manifest.jsonl"), o.path("manifest.jsonl"), dest, ost.Spec.Dest, ostatus, flags, ost.Phase != "done" && ost.Choice != "stopped")
	}
	if flags == nil {
		return table, false
	}
	later := false
	for i, f := range flags {
		if f&mfTouched == 0 {
			if f&mfPending != 0 && (table[i] == stHashed || table[i] == stCopied) {
				table[i], later = stCheckWait, true
			}
			continue
		}
		later = true
		switch {
		case f&mfFailed != 0:
			table[i] = stFailed
		case f&mfWarn != 0:
			table[i] = stS2Warn
		case f&(mfDest|mfSrc) == mfDest|mfSrc:
			table[i] = stStrong
		case f&mfDest != 0:
			table[i] = stVerified
		case f&mfSrc != 0 && table[i] != stVerified:
			table[i] = stSrcOK
		}
	}
	return table, later
}

// layVerify walks one Verify job's manifest beside its copy's and adds what
// the Verify found for each file to flags, indexed by the copy's files.
func layVerify(copyManifest, verManifest, copyDest, verDest string, vstatus, flags []byte, unfinished bool) {
	vit, err := openManifest(verManifest)
	if err != nil {
		return
	}
	defer vit.Close()
	cit, err := openManifest(copyManifest)
	if err != nil {
		return
	}
	defer cit.Close()
	for vit.Next() {
		ve := vit.E
		if ve.I < 0 || ve.I >= len(vstatus) {
			continue
		}
		ci := -1
		for cit.Next() {
			rel, err := filepath.Rel(verDest, filepath.Join(copyDest, filepath.FromSlash(cit.E.P)))
			if err == nil && filepath.ToSlash(rel) == ve.P {
				ci = cit.E.I
				break
			}
		}
		if ci < 0 {
			return // the copy's list ran out: nothing more lines up
		}
		if ci >= len(flags) {
			continue
		}
		f := flags[ci]
		switch vstatus[ve.I] {
		case stPending:
			if unfinished {
				flags[ci] |= mfPending
			}
			continue
		case stVerified:
			f = f&^mfFailed | mfDest
		case stStrong:
			f = f&^(mfFailed|mfWarn) | mfDest | mfSrc
		case stSrcOK:
			f = f&^mfFailed | mfSrc
		case stFailed:
			f |= mfFailed
		case stS2Warn:
			f = f&^mfFailed | mfDest | mfWarn
		default:
			continue
		}
		flags[ci] = f | mfTouched
	}
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
	Mtime      time.Time    `json:"mtime"`
	Source     string       `json:"source"`
	Dest       string       `json:"dest"`
	DestExists bool         `json:"dest_exists"`
	DestSize   int64        `json:"dest_size,omitempty"`
	Proof      string       `json:"proof"`
	Kind       string       `json:"kind,omitempty"`    // "verify" when the file belongs to a Verify job
	Already    bool         `json:"already,omitempty"` // it was already at the destination: compared, not copied
	Hash       Hashes       `json:"hash"`
	Events     []Event      `json:"events"`
	Rechecks   []Recheck    `json:"rechecks"`       // "Read again" results, oldest first
	RecheckNow []RecheckNow `json:"recheck_now"`    // waiting or being read
	Busy       bool         `json:"busy,omitempty"` // a copy is running, so a recheck waits for it
	// Verifies are the Verify runs over this copy that reached this file,
	// oldest first, with the hashes they read (v0.2.44).
	Verifies []FileVerify `json:"verifies"`
	// VerifyQueued: a Verify of this copy is waiting or running.
	VerifyQueued bool `json:"verify_queued,omitempty"`
	// JobDone: the copy has finished or stopped, so a Verify can be started.
	JobDone bool `json:"job_done,omitempty"`
	// JobDest is the copy's destination folder, what Verify files checks.
	JobDest string `json:"job_dest"`
	// From: for a file of a Verify, the copies that Verify checks, so a new
	// Verify set up from here checks the same ones (v0.2.46).
	From []string `json:"from,omitempty"`
}

// FileVerify is what one Verify run read for one file.
type FileVerify struct {
	Time time.Time `json:"time"`
	D1   string    `json:"d1,omitempty"` // the copy read in full
	S2   string    `json:"s2,omitempty"` // the source read again
	Err  string    `json:"err,omitempty"`
}

// maxVerifies bounds how many Verify runs a file's info lists.
const maxVerifies = 20

// verifiesOf reads, from every Verify job over copy id, what it found for
// the file at dest. Called with listMu held. Each Verify's manifest is read
// from disk, one at a time; nothing is kept.
func verifiesOf(jobs []*Job, id, dest string) (out []FileVerify, queued bool) {
	for _, o := range jobs {
		o.mu.Lock()
		ost := o.st
		o.mu.Unlock()
		if !ost.Spec.isVerify() || !slices.Contains(ost.Spec.From, id) {
			continue
		}
		if ost.Phase != "done" {
			queued = true
		}
		if ost.Phase == "scan" || len(out) >= maxVerifies {
			continue
		}
		it, err := openManifest(o.path("manifest.jsonl"))
		if err != nil {
			continue
		}
		idx := -1
		for it.Next() {
			if filepath.Join(ost.Spec.Dest, filepath.FromSlash(it.E.P)) == dest {
				idx = it.E.I
				break
			}
		}
		it.Close()
		if idx < 0 {
			continue
		}
		var fv FileVerify
		ReadJournal(o.path("journal.log"), 0, func(r Rec) bool {
			if r.I != idx {
				return true
			}
			fv.Time = time.Unix(r.T, 0)
			if r.D1 != "" && r.St != "s2ok" && r.St != "s2changed" {
				fv.D1 = r.D1
			}
			if r.S2 != "" {
				fv.S2 = r.S2
			}
			if r.Err != "" {
				fv.Err = r.Err
			}
			return true
		})
		if !fv.Time.IsZero() {
			out = append(out, fv)
		}
	}
	return out, queued
}

// maxEvents bounds a file's story; a file has a handful of records at most.
const maxEvents = 50

var eventText = map[string]string{
	"strong":     "Copied and verified: the copy read back and the source read a second time both match",
	"verified":   "Copied and verified: the copy read back matches the source",
	"identical":  "Not copied: already at the destination. Both files read in full, hashes match; left untouched",
	"failed":     "Failed",
	"match":      "Verified again: the copy was read in full and matches the hash stored when it was copied",
	"copied":     "Copied and hashed as it was read, and given its real name; the check (copy read back, source read again) comes later",
	"hashed":     "Copied and hashed as it was read, and given its real name; the copy was not read back",
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
	if !spec.isVerify() {
		// With what later Verify jobs of this copy found, as in the file list (v0.2.46).
		if table, _ := m.mergedStatus(j); i >= 0 && i < len(table) {
			st = table[i]
		}
	}
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
		if r.St == "identical" {
			fi.Already = true
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
	fi.Kind = spec.Kind
	fi.Rechecks = j.rechecksOf(i)
	if fi.Rechecks == nil {
		fi.Rechecks = []Recheck{}
	}
	if !spec.isVerify() {
		fi.Status = alreadyStatus(fi.Status, fi.Already)
	}
	fi.RecheckNow = m.recheckFor(id, i)
	m.mu.Lock()
	fi.Busy = m.running != nil
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	fi.JobDone, fi.JobDest, fi.From = phase == "done", spec.Dest, spec.From
	fi.Verifies = []FileVerify{}
	if !spec.isVerify() {
		fi.Verifies, fi.VerifyQueued = verifiesOf(jobs, id, fi.Dest)
		if fi.Verifies == nil {
			fi.Verifies = []FileVerify{}
		}
	}
	if spec.isVerify() {
		switch fi.Status {
		case "strong":
			fi.Proof = "Verified: the copy read in full and the source read again both match the hash stored when it was copied"
		case "warning":
			fi.Proof = "The copy matches the hash stored when it was copied; the source has changed since"
		case "proven":
			fi.Proof = "Verified again: the copy read in full matches the hash stored when it was copied"
		case "source":
			fi.Proof = "The source read again matches the hash stored when it was copied; this check did not read the copy"
		case "failed":
			fi.Proof = "Does not match: see the problem below"
		default:
			fi.Proof = "Not checked yet"
		}
		return fi, nil
	}
	switch fi.Status {
	case "already":
		fi.Proof = "Not copied: a file with this name was already at the destination. It and the source were both read in full and their hashes match, so it was left untouched"
	case "already2":
		fi.Proof = "Not copied: a file with this name was already at the destination. It was read in full and the source was read twice; all hashes match, so it was left untouched"
	case "strong":
		fi.Proof = "Strong: two separate reads of the source match what the destination holds"
	case "proven":
		fi.Proof = "Verified: the copy read back matches the source"
		if spec.S2 == "now" && phase != "done" {
			fi.Proof += "; the second source read is still to come"
		}
	case "failed":
		fi.Proof = "None: this file is not verified"
	case "warning":
		fi.Proof = "Verified against what was read, with a warning about the source"
	case "copied":
		fi.Proof = "Not yet: copied, waiting for the check pass; it keeps a temporary name until then"
	case "source":
		fi.Proof = "The source read a second time matches; the copy has not been read back yet"
	case "checkwait":
		fi.Proof = "Not yet: copied and hashed; a Verify of this copy in the queue will check it"
	case "hashed":
		fi.Proof = "Not verified: copied and hashed as it was read, but the copy was not read back"
		if spec.Verify != "" {
			fi.Proof += "; its own Verify item in the queue checks it"
		}
	default:
		fi.Proof = "Not yet: this file has not been copied"
	}
	return fi, nil
}

// copyMeasure is how long a job must have spent copying before its average
// copy speed is shown; until then the page says "measuring".
const copyMeasure = 2 * time.Second

// copyAverage is the copy speed averaged over all the copying this run has
// done: bytes written by copy passes divided by the time spent in them. Time
// spent reading back, re-reading or comparing files already there is left
// out, so it neither drags the average down nor resets it.
func copyAverage(bytes, ns int64) int64 {
	if ns < int64(copyMeasure) || bytes <= 0 {
		return 0
	}
	return int64(float64(bytes) / (float64(ns) / 1e9))
}

// IsCheckStage says whether a stage reads back or compares rather than
// copies: the destination read back, the source read again, or a file
// already at the destination compared in full. The page and the headline
// say "Verifying" for these (v0.2.44).
func IsCheckStage(stage string) bool {
	for _, p := range []string{"Reading back", "Checking", "Already at the destination", "Reading the source again"} {
		if strings.HasPrefix(stage, p) {
			return true
		}
	}
	return false
}
