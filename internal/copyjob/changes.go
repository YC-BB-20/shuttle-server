package copyjob

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/location"
)

// v0.2.82: a copy copies exactly the list made when it was created. Two
// things can have moved since, and neither stops the job: a file is looked at
// again when its turn comes (turnCheck), and after the copy the source is
// listed again for files that are new (checkSource).

// chg says how a source file differed from the list when its turn came.
type chg struct{ path, msg string }

// mark puts the flag on a record, so the count and the Problems list have it.
func (c *chg) mark(r *Rec) {
	if c != nil {
		r.Chg, r.P, r.Err = true, c.path, c.msg
	}
}

// goneMsg is the plain-words line for a file deleted or moved before its turn.
const goneMsg = "was in the list when this copy was created but is no longer in the source; not copied"

func human(t int64) string { return time.Unix(0, t).Local().Format("2 Jan 2006 15:04:05") }

// turnCheck looks at a source file when its turn comes. A file that is gone
// (the source disk itself still there) is recorded and skipped: skip is true.
// A file whose size or modified time differs from the list is copied as it is
// now: en is updated to what was found and the old and new values are
// returned, to be put on the file's first record. Anything else about the
// file (a disk that went away, an unreadable file) is left to the copy.
func (j *Job) turnCheck(e env, en *entry, srcPath string, jr *Journal) (skip bool, c *chg) {
	size, mtime, err := statSM(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !j.gone(e.src, true) {
			r := Rec{I: en.I, St: "gone", N: en.S, P: en.P, Err: goneMsg}
			jr.Append(r)
			j.mu.Lock()
			j.apply(r)
			j.mu.Unlock()
			j.dropCheckpoints(en.I)
			return true, nil
		}
		return false, nil
	}
	if size == en.S && mtime == en.M {
		return false, nil
	}
	c = &chg{path: en.P, msg: fmt.Sprintf("changed since it was listed; copied as it is now. Listed: %s, %s. Found: %s, %s",
		size64(en.S), human(en.M), size64(size), human(mtime))}
	en.S, en.M = size, mtime
	return false, c
}

func size64(n int64) string { return fmt.Sprintf("%d bytes", n) }

// hashPath is a path's place in the list the source check compares with.
func hashPath(p string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(p))
	return h.Sum64()
}

// AddedFile is one file the source check found that the copy's list lacks.
type AddedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// checkSource lists the source again and writes the files that are in it now
// but not in the copy's list to added.jsonl (in the manifest's own format, so
// "Copy these too" can adopt it as a list). It reads names, sizes and times,
// never file data, and changes nothing in the source or the destination. It
// holds 8 bytes per listed file (a hash of each path) to compare with.
// A source that cannot be listed is a note, not an error; only the owner
// pausing or stopping, or Shuttle stopping, is returned.
func (j *Job) checkSource(ctx context.Context, e env) error {
	note := func(s string) {
		j.mu.Lock()
		j.st.CheckNote = s
		j.mu.Unlock()
		j.save()
	}
	if j.gone(e.src, true) || !diskio.VolumeMounted(j.st.Spec.Source) {
		note("Source not checked: the source disk is not connected.")
		return nil
	}
	j.setLive(func(l *live) { l.stage = "Looking at the source again for new files" })
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		note("Source not checked: " + err.Error())
		return nil
	}
	known := make([]uint64, 0, j.st.Files)
	for it.Next() {
		known = append(known, hashPath(it.E.P))
	}
	it.Close()
	slices.Sort(known)
	tmp := j.path("added.jsonl.part")
	f, err := os.Create(tmp)
	if err != nil {
		note("Source not checked: " + err.Error())
		return nil
	}
	w := bufio.NewWriterSize(f, 128<<10)
	n, total := 0, int64(0)
	werr := j.source().Walk(ctx, func(en location.Entry) error {
		h := hashPath(en.Rel)
		if i := sort.Search(len(known), func(i int) bool { return known[i] >= h }); i < len(known) && known[i] == h {
			return nil
		}
		b, _ := json.Marshal(entry{I: n, P: en.Rel, S: en.Size, M: en.Mtime, O: uint32(en.Mode)})
		w.Write(append(b, '\n'))
		n++
		total += en.Size
		return nil
	}, func(location.Skipped) {})
	if werr == nil {
		werr = w.Flush()
	}
	if werr == nil {
		werr = f.Sync()
	}
	f.Close()
	if werr != nil {
		os.Remove(tmp) // our own partial list
		if ctx.Err() != nil && !errors.Is(context.Cause(ctx), diskio.ErrGone) {
			return context.Cause(ctx)
		}
		if errors.Is(context.Cause(ctx), diskio.ErrGone) || j.gone(e.src, true) {
			note("Source not checked: the source disk is not connected.")
		} else {
			note("Source not checked: " + werr.Error())
		}
		return nil
	}
	if err := os.Rename(tmp, j.path("added.jsonl")); err != nil {
		note("Source not checked: " + err.Error())
		return nil
	}
	j.mu.Lock()
	j.st.Added, j.st.AddedBytes, j.st.CheckedAt, j.st.CheckNote = n, total, time.Now(), ""
	j.mu.Unlock()
	j.save()
	return nil
}

// AddedList reads one page of the files found added to the source, from
// index from, at most limit of them. Next is -1 at the end.
func (m *Manager) AddedList(id string, from, limit int) ([]AddedFile, int, error) {
	j := m.find(id)
	if j == nil {
		return nil, -1, errors.New("no such copy")
	}
	limit = min(max(limit, 1), MaxFilePage)
	it, err := openManifest(j.path("added.jsonl"))
	if err != nil {
		return []AddedFile{}, -1, nil
	}
	defer it.Close()
	out := []AddedFile{}
	for it.Next() {
		if it.E.I < from {
			continue
		}
		if len(out) == limit {
			return out, it.E.I, nil
		}
		out = append(out, AddedFile{it.E.P, it.E.S})
	}
	return out, -1, nil
}

// sourceCheckMu lets one "Check source again" list a source at a time.
var sourceCheckMu sync.Mutex

// CheckSourceAgain lists the source of a finished copy again, in the
// background, and keeps what it finds (Added, and the list for "Copy these
// too"). It reads names only and changes nothing. A source that is not
// connected is a note on the copy, not an error.
func (m *Manager) CheckSourceAgain(id string) error {
	j := m.find(id)
	if j == nil {
		return errors.New("no such copy")
	}
	j.mu.Lock()
	ok := j.st.Phase == "done" && !j.st.Spec.isVerify() && j.st.Choice != "stopped"
	j.mu.Unlock()
	if !ok {
		return errors.New("the source can be checked again only on a finished copy")
	}
	if !sourceCheckMu.TryLock() {
		return errors.New("another source check is running; try again when it has finished")
	}
	if !j.checking.CompareAndSwap(false, true) {
		sourceCheckMu.Unlock()
		return errors.New("this copy's source is being checked now")
	}
	go func() {
		defer sourceCheckMu.Unlock()
		defer j.checking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		sd, err := m.Disks.For(j.st.Spec.Source)
		if err != nil {
			j.mu.Lock()
			j.st.CheckNote = "Source not checked: the source disk is not connected."
			j.mu.Unlock()
			j.save()
			return
		}
		j.checkSource(ctx, env{src: sd})
		j.setLive(func(l *live) { l.stage = "" })
		j.mu.Lock()
		added, note := j.st.Added, j.st.CheckNote
		j.mu.Unlock()
		m.Log.Info("source checked again", "job", id, "added", added, "note", note)
	}()
	return nil
}

// CopyAdded makes a new copy of the files the source check found added to
// the source, with the same destination and settings as the copy it follows.
// It is a separate copy in the queue; nothing starts by itself beyond that
// and the owner can pause or stop it like any other.
func (m *Manager) CopyAdded(id string) (string, error) {
	j := m.find(id)
	if j == nil {
		return "", errors.New("no such copy")
	}
	j.mu.Lock()
	st := j.st
	j.mu.Unlock()
	if st.Phase != "done" || st.Spec.isVerify() || st.Added == 0 {
		return "", errors.New("this copy has no added files to copy")
	}
	dir := filepath.Join(m.DraftDir(), "added-"+id)
	os.RemoveAll(dir) // our own earlier draft of this
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	defer os.RemoveAll(dir) // the new copy has its own link to the list
	if err := linkOrCopy(j.path("added.jsonl"), filepath.Join(dir, "manifest.jsonl")); err != nil {
		return "", errors.New("the list of added files is not there; check the source again first")
	}
	if f, err := os.Create(filepath.Join(dir, "skipped.jsonl")); err == nil {
		f.Close()
	}
	cs := m.Settings()
	d := &Draft{Dir: dir, Source: st.Spec.Source, System: cs.SkipSystem, AppleDouble: cs.SkipSystem && cs.SkipAppleDouble,
		Files: int64(st.Added), Bytes: st.AddedBytes, Listed: time.Now()}
	s := Spec{Source: st.Spec.Source, Dest: st.Spec.Dest, S2: st.Spec.S2, Check: st.Spec.Check, Verify: st.Spec.Verify, Hash: st.Spec.Hash,
		EstBytes: st.AddedBytes, EstFiles: int64(st.Added)}
	nj, err := m.AddDraft(s, d)
	if err != nil {
		return "", err
	}
	m.Log.Info("a copy of files added to the source was made", "from", id, "job", nj.st.ID, "files", st.Added)
	return nj.st.ID, nil
}
