package copyjob

// The Verify job (DESIGN.md sections 5 and 14): any destination folder or
// file that Shuttle copied to is read again in full, at any time, as its own
// queue item, and compared with the hash stored when it was copied. It only
// reads and reports; it never changes, renames or deletes anything.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/location"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// KindVerify marks a Verify job; an empty Kind is a copy.
const KindVerify = "verify"

func (s Spec) isVerify() bool { return s.Kind == KindVerify }

// addVerify queues a re-check of a folder or file at a destination.
func (m *Manager) addVerify(s Spec) (*Job, error) {
	p, err := filepath.Abs(strings.TrimSpace(s.Source))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("%q was not found; is its disk connected?", p)
	}
	root, file := p, ""
	if !fi.IsDir() {
		root, file = filepath.Dir(p), filepath.Base(p)
	}
	var from []string
	running := false
	m.mu.Lock()
	for _, j := range m.jobs {
		j.mu.Lock()
		st := j.st
		j.mu.Unlock()
		if st.Spec.isVerify() || !(inside(p, st.Spec.Dest) || inside(st.Spec.Dest, p)) {
			continue
		}
		if st.Phase != "done" {
			running = true
			continue
		}
		from = append(from, st.ID)
	}
	m.mu.Unlock()
	if len(s.From) > 0 { // asked from a row: only the copies that row stands for (v0.2.28)
		from = slices.DeleteFunc(from, func(id string) bool { return !slices.Contains(s.From, id) })
	}
	if len(from) == 0 {
		if running {
			return nil, errors.New("the copy to this place has not finished yet; verify it once it has")
		}
		return nil, fmt.Errorf("Shuttle has no stored hashes for %q: only places Shuttle copied to can be verified", p)
	}
	v, err := diskio.VolumeOf(root)
	if err != nil {
		return nil, err
	}
	if v.Network {
		return nil, errors.New("verifying on a network server is not supported yet")
	}
	src, srcVol := root, v.ID
	if s.S2 == S2After || s.S2 == S2Only { // the source read again, after the copies or alone
		if src, err = m.sourceOf(root, from); err != nil {
			return nil, err
		}
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("the source folder %q was not found; is its disk connected?", src)
		}
		sv, err := diskio.VolumeOf(src)
		if err != nil {
			return nil, err
		}
		if sv.Network {
			return nil, errors.New("reading a source on a network server is not supported yet")
		}
		srcVol = sv.ID
	}
	choice := "run"
	if s.Paused {
		choice = "paused"
	}
	s2 := ""
	if src != root {
		s2 = s.S2
	}
	cs := m.Settings()
	now := time.Now()
	id := now.Format("20060102-150405.000")
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Created: now, Choice: choice, Phase: "scan",
		Spec:      Spec{Kind: KindVerify, Source: src, Dest: root, S2: s2, File: file, From: from, SkipSystem: cs.SkipSystem, SkipAppleDouble: cs.SkipSystem && cs.SkipAppleDouble},
		SourceVol: srcVol, DestVol: v.ID, SameVolume: srcVol == v.ID && src != root}}
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return nil, err
	}
	if err := j.save(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	m.Log.Info("verify job added", "job", id, "place", p, "copies", len(from), "source_again_after", s2 != "", "paused", choice == "paused")
	m.poke()
	return j, nil
}

// verifyIDSuffix names the Verify job a copy queues for itself, so a crash
// between the two saves never queues it twice.
const verifyIDSuffix = "-verify"

// queueVerify adds the Verify job a copy asked for (Spec.Verify): the copy
// read back in full and, when the copy's S2 is "now", its source read again
// beside it. Since v0.2.16 it is added together with the copy, so it shows in
// the queue under it from the start, and waits for the copy to finish
// (After); it is added paused when the owner chose that. On start-up it is
// added for a copy saved without one (an older version, or a crash between
// the two saves) — never twice, thanks to its fixed id.
func (m *Manager) queueVerify(cj *Job) {
	cj.mu.Lock()
	st := cj.st
	cj.mu.Unlock()
	if st.Spec.isVerify() || st.Spec.Verify == "" || st.Choice == "stopped" || st.VerifyJob != "" {
		return
	}
	id := st.ID + verifyIDSuffix
	if m.find(id) == nil {
		choice := "run"
		if st.Spec.Verify == "paused" {
			choice = "paused"
		}
		src, srcVol := st.Spec.Dest, st.DestVol
		if st.Spec.S2 == "now" {
			src, srcVol = st.Spec.Source, st.SourceVol
		}
		after := ""
		if st.Phase != "done" {
			after = st.ID
		}
		j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Created: time.Now(), Choice: choice, Phase: "scan", After: after,
			Spec:      Spec{Kind: KindVerify, Source: src, Dest: st.Spec.Dest, S2: st.Spec.S2, From: []string{st.ID}, SkipSystem: st.Spec.SkipSystem, SkipAppleDouble: st.Spec.SkipAppleDouble},
			SourceVol: srcVol, DestVol: st.DestVol, SameVolume: srcVol == st.DestVol}}
		if err := os.MkdirAll(j.dir, 0o700); err != nil {
			m.Log.Error("could not queue the Verify job for a copy", "job", st.ID, "err", err)
			return
		}
		if err := j.save(); err != nil {
			m.Log.Error("could not queue the Verify job for a copy", "job", st.ID, "err", err)
			return
		}
		m.mu.Lock()
		m.jobs = append(m.jobs, j)
		m.mu.Unlock()
		m.Log.Info("verify job queued for a copy", "job", id, "waits_for_copy", after != "", "copy", st.ID, "paused", choice == "paused", "source_again", st.Spec.S2 == "now")
	}
	cj.mu.Lock()
	cj.st.VerifyJob = id
	cj.mu.Unlock()
	cj.save()
	m.poke()
}

// S2After (Verify jobs, v0.2.26): every copy is read back first, then the
// source is read again in a second pass over the whole job.
const S2After = "after"

// S2Only (Verify jobs, v0.2.27): only the source is read again and compared
// with the hash stored when it was copied; the copies are not read.
const S2Only = "only"

// readsSource: this Verify job reads the source again (S2), beside the copy
// ("now", a copy's own Verify item) or after all of it (S2After).
func (s Spec) readsSource() bool {
	return s.isVerify() && (s.S2 == "now" || s.S2 == S2After || s.S2 == S2Only) && s.Source != s.Dest
}

// sourceOf finds the source folder that matches place for the copies that
// wrote it. Every copy must have come from the same folder, and place must
// lie inside the copy's destination; otherwise the source cannot be named.
func (m *Manager) sourceOf(place string, from []string) (string, error) {
	src := ""
	for _, id := range from {
		j := m.find(id)
		if j == nil {
			continue
		}
		j.mu.Lock()
		sp := j.st.Spec
		j.mu.Unlock()
		rel, err := filepath.Rel(sp.Dest, place)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("this folder holds several copies; to read their sources again, verify each copy from its own row")
		}
		s := filepath.Join(sp.Source, rel)
		if src != "" && s != src {
			return "", errors.New("the files here were copied from different sources; verify the destination only, or each copy from its own row")
		}
		src = s
	}
	if src == "" {
		return "", errors.New("the source of this copy is not known")
	}
	return src, nil
}

// slot is one stored hash in the scan's lookup file: 32 hex characters at
// offset index*32, zeros where a file has no proven hash. The lookup file
// lives on disk, so memory does not grow with the number of files.
const slot = 32

// verifyScan builds this job's manifest from the copies it checks: every
// file in scope with its stored hash (H). Newest copy first; a file that
// several copies wrote is checked once, against the newest record.
func (j *Job) verifyScan(ctx context.Context) error {
	j.setLive(func(l *live) { l.stage = "Reading the stored hashes"; l.scanned, l.scannedBytes = 0, 0 })
	spec := j.st.Spec
	f, err := os.Create(j.path("manifest.jsonl")) // our own file, rebuilt from the start
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 256<<10)
	n, skipped, system := 0, 0, 0
	var total int64
	var seen []uint64 // fingerprints of paths already taken, 8 bytes a file, only during the scan
	for k := len(spec.From) - 1; k >= 0; k-- {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		dir := filepath.Join(filepath.Dir(j.dir), spec.From[k])
		cj, err := loadJob(dir)
		if err != nil {
			continue // that copy was removed or set aside
		}
		look := j.path("expect.tmp")
		lf, err := os.OpenFile(look, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		ReadJournal(cj.path("journal.log"), 0, func(r Rec) bool {
			if r.I < 0 || r.I >= cj.st.Files {
				return true
			}
			var b [slot]byte // a failure or an unchecked copy clears it
			switch r.St {
			case "verified", "identical", "strong", "s2ok", "s2changed", "s2unstable":
				if len(r.D1) == slot {
					copy(b[:], r.D1)
				}
			case "hashed": // copy only: the hash of what was read for the copy
				if len(r.C) == slot {
					copy(b[:], r.C)
				}
			}
			lf.WriteAt(b[:], int64(r.I)*slot)
			return true
		})
		var newSeen []uint64
		it, err := openManifest(cj.path("manifest.jsonl"))
		if err != nil {
			lf.Close()
			os.Remove(look)
			continue
		}
		for it.Next() {
			e := it.E
			full := filepath.Join(cj.st.Spec.Dest, filepath.FromSlash(e.P))
			if !inside(full, spec.Dest) {
				continue
			}
			rel, _ := filepath.Rel(spec.Dest, full)
			if spec.File != "" && rel != spec.File {
				continue
			}
			if spec.SkipSystem && location.IsSystemPath(filepath.ToSlash(rel), spec.SkipAppleDouble) {
				system++ // a system file: Finder may have changed it since
				continue
			}
			fp := pathPrint(rel)
			if _, dup := slices.BinarySearch(seen, fp); dup {
				continue
			}
			var b [slot]byte
			lf.ReadAt(b[:], int64(e.I)*slot)
			if b[0] == 0 {
				skipped++ // never proven by the copy: nothing to compare with
				continue
			}
			if len(spec.From) > 1 {
				newSeen = append(newSeen, fp)
			}
			out, _ := json.Marshal(entry{I: n, P: filepath.ToSlash(rel), S: e.S, M: e.M, O: e.O, H: string(b[:])})
			w.Write(append(out, '\n'))
			n++
			total += e.S
			if n%500 == 0 {
				j.setLive(func(l *live) { l.scanned, l.scannedBytes = n, total })
			}
		}
		it.Close()
		lf.Close()
		os.Remove(look) // our own temporary lookup file
		seen = append(seen, newSeen...)
		slices.Sort(seen)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	j.mu.Lock()
	j.st.Files, j.st.Bytes, j.st.Skipped, j.st.SystemSkipped, j.st.Phase = n, total, skipped, system, "verify"
	j.status = make([]byte, n)
	j.already = nil
	j.live.scanned, j.live.scannedBytes = n, total
	j.mu.Unlock()
	return j.save()
}

func pathPrint(rel string) uint64 {
	b := xxh3.Sum128([]byte(rel)).Bytes()
	var v uint64
	for _, x := range b[:8] {
		v = v<<8 | uint64(x)
	}
	return v
}

// verifyPhase reads every file not yet checked and compares it with its
// stored hash. A mismatch is read once more before it is believed.
func (j *Job) verifyPhase(ctx context.Context, e env, jr *Journal, accepting func() bool) error {
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return err
	}
	defer it.Close()
	for it.Next() {
		en := it.E
		j.mu.Lock()
		done := en.I < len(j.status) && j.status[en.I] != stPending
		j.mu.Unlock()
		if done {
			continue
		}
		for !accepting() {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-time.After(time.Second):
			}
		}
		p := filepath.Join(j.st.Spec.Dest, filepath.FromSlash(en.P))
		j.setLive(func(l *live) { l.file, l.stage, l.done, l.size = en.P, "Reading back the destination", 0, en.S })
		start := time.Now()
		rec := Rec{I: en.I, N: en.S, C: en.H, T: time.Now().Unix()}
		var extra Rec // the source read again, when this job does that
		fail := func(why string) { rec.St, rec.P, rec.Err = "failed", en.P, why }
		size, _, serr := statSM(p)
		switch {
		case serr != nil && j.gone(e.dst, false):
			return diskio.ErrGone
		case serr != nil:
			fail("missing: the file is no longer at the destination")
		case size != en.S:
			fail(fmt.Sprintf("wrong size: %d bytes instead of %d", size, en.S))
		default:
			withS2 := j.st.Spec.readsSource() && j.st.Spec.S2 == "now"
			s2ch := make(chan hashRes, 1)
			if withS2 {
				sp := filepath.Join(j.st.Spec.Source, filepath.FromSlash(en.P))
				go func() {
					h, err := j.hashFileQuiet(ctx, e, e.src, sp, en.I, "s2")
					s2ch <- hashRes{h, err}
				}()
				j.setLive(func(l *live) { l.stage = "Reading back the destination and the source" })
			}
			h, err := j.hashFile(ctx, e, e.dst, p, en.I, "v1", false)
			var s2 hashRes
			if withS2 {
				s2 = <-s2ch // always wait: the source read must not outlive this file
				if s2.err != nil && !isFileErr(s2.err) {
					return s2.err
				}
			}
			if err != nil && !isFileErr(err) {
				return err
			}
			if err == nil && h.String() != en.H {
				j.dropCheckpoint(en.I, "v1")
				j.setLive(func(l *live) { l.stage = "Reading back the destination again to confirm a difference" })
				h, err = j.hashFile(ctx, e, e.dst, p, en.I, "v2", false)
				if err != nil && !isFileErr(err) {
					return err
				}
			}
			rec.D1 = h.String()
			switch {
			case err != nil:
				fail(err.Error())
				rec.D1 = ""
			case rec.D1 == en.H:
				rec.St = "match"
			default:
				fail("differs: the copy read twice does not match the hash stored when it was copied")
			}
			if withS2 && rec.St == "match" {
				extra = s2Verdict(en, filepath.Join(j.st.Spec.Source, filepath.FromSlash(en.P)), s2)
				if extra.St == "failed" {
					rec, extra = extra, Rec{}
					rec.C, rec.D1, rec.T = en.H, h.String(), time.Now().Unix()
				}
			}
		}
		rec.Ms = time.Since(start).Milliseconds()
		for _, out := range []Rec{rec, extra} {
			if out.St == "" {
				continue
			}
			jr.Append(out)
			j.mu.Lock()
			j.apply(out)
			j.mu.Unlock()
		}
		extra = Rec{}
		j.dropCheckpoint(en.I, "v1")
		j.dropCheckpoint(en.I, "v2")
		j.dropCheckpoint(en.I, "s2")
	}
	return nil
}

// s2Verdict compares the source, read again, with the hash stored when the
// copy was made (the copy itself already matches it). Nothing is changed.
// A source that changed since it was listed is a warning; a source that
// reads differently without having changed means the copy may hold a bad
// read, so the file fails.
func s2Verdict(en entry, srcPath string, s2 hashRes) Rec {
	out := Rec{I: en.I, N: en.S, S2: s2.h.String(), D1: en.H}
	if s2.err == nil && s2.h.String() == en.H {
		out.St = "s2ok"
		return out
	}
	out.P = en.P
	if sz, m, err := statSM(srcPath); err != nil || sz != en.S || m != en.M {
		out.St, out.Err = "s2changed", "the source changed after the copy, or is no longer there (not a copy error)"
		return out
	}
	out.St, out.Err = "failed", "the source read now differs from what was copied; the copy may hold a bad read from the card or cable: copy this file again"
	if s2.err != nil {
		out.S2 = ""
		out.Err = "the source could not be read again: " + s2.err.Error()
	}
	return out
}

// runVerify carries a Verify job forward from wherever it stands.
func (j *Job) runVerify(ctx context.Context, e env, accepting func() bool) error {
	if j.st.Phase == "scan" {
		if err := j.verifyScan(ctx); err != nil {
			return err
		}
	}
	j.mu.Lock()
	j.status = make([]byte, j.st.Files)
	j.already = nil
	j.c = counts{}
	j.mu.Unlock()
	jr, _, err := OpenJournal(j.path("journal.log"), func(r Rec) { j.mu.Lock(); j.apply(r); j.mu.Unlock() })
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	defer jr.Close()
	if j.st.Phase == "verify" && j.st.Spec.S2 == S2Only && j.st.Spec.readsSource() {
		j.mu.Lock()
		j.st.Phase = "source" // nothing to read at the destination
		j.mu.Unlock()
		j.save()
	}
	if j.st.Phase == "verify" {
		if err := j.verifyPhase(ctx, e, jr, accepting); err != nil {
			return err
		}
		jr.Flush()
		if j.st.Spec.S2 == S2After && j.st.Spec.readsSource() {
			j.mu.Lock()
			j.st.Phase = "source"
			j.mu.Unlock()
			j.save()
		}
	}
	if j.st.Phase == "source" {
		if err := j.verifySourcePhase(ctx, e, jr, accepting); err != nil {
			return err
		}
		jr.Flush()
	}
	j.mu.Lock()
	j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
	j.live = live{}
	j.mu.Unlock()
	return j.save()
}

// verifySourcePhase reads the source again for every file whose copy
// matched, after the whole job's copies were read back (S2After), or for
// every file when only the source is read (S2Only). A file already settled
// (source read, failed, warning) is skipped, so a resumed job carries on
// where it stopped.
func (j *Job) verifySourcePhase(ctx context.Context, e env, jr *Journal, accepting func() bool) error {
	want := stVerified
	if j.st.Spec.S2 == S2Only {
		want = stPending
	}
	j.mu.Lock()
	todo := 0
	for _, st := range j.status {
		if st == want {
			todo++
		}
	}
	j.live.s2total, j.live.s2done = todo, 0
	j.mu.Unlock()
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return err
	}
	defer it.Close()
	for it.Next() {
		en := it.E
		j.mu.Lock()
		ok := en.I < len(j.status) && j.status[en.I] == want
		j.mu.Unlock()
		if !ok {
			continue
		}
		for !accepting() {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-time.After(time.Second):
			}
		}
		sp := filepath.Join(j.st.Spec.Source, filepath.FromSlash(en.P))
		j.setLive(func(l *live) { l.file, l.stage, l.done, l.size = en.P, "Reading the source again", 0, en.S })
		start := time.Now()
		var res hashRes
		if _, _, serr := statSM(sp); serr != nil && j.gone(e.src, true) {
			return diskio.ErrGone
		}
		res.h, res.err = j.hashFile(ctx, e, e.src, sp, en.I, "s2", true)
		if res.err != nil && !isFileErr(res.err) {
			return res.err
		}
		out := s2Verdict(en, sp, res)
		if out.St == "failed" {
			out.C, out.D1 = en.H, en.H
		}
		if want == stPending { // source only: the copy was not read
			out.D1, out.C = "", en.H
			if out.St == "s2ok" {
				out.St = "srcok"
			}
		}
		out.T, out.Ms = time.Now().Unix(), time.Since(start).Milliseconds()
		jr.Append(out)
		j.mu.Lock()
		j.apply(out)
		j.live.s2done++
		j.mu.Unlock()
		j.dropCheckpoint(en.I, "s2")
	}
	return nil
}
