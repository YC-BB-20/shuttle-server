package copyjob

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/location"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// entry is one manifest line.
type entry struct {
	I int    `json:"i"`
	P string `json:"p"`
	S int64  `json:"s"`
	M int64  `json:"m"`
	O uint32 `json:"o"`
	H string `json:"h,omitempty"` // Verify jobs: the stored hash to compare with
}

// waitErr is a problem shared by every file — a disk gone, no space, too
// much memory in use. The job rests and resumes by itself once it clears.
type waitErr struct{ reason string }

func (w waitErr) Error() string { return w.reason }

type manifestIter struct {
	f  *os.File
	sc *bufio.Scanner
	E  entry
}

func openManifest(p string) (*manifestIter, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return &manifestIter{f: f, sc: sc}, nil
}

func (m *manifestIter) Next() bool {
	for m.sc.Scan() {
		if json.Unmarshal(m.sc.Bytes(), &m.E) == nil {
			return true
		}
	}
	return false
}

func (m *manifestIter) Close() { m.f.Close() }

// scan walks the source and writes the manifest. Memory holds only counters.
func (j *Job) scan(ctx context.Context, src location.Source) error {
	j.setLive(func(l *live) { l.stage = "Scanning the source"; l.scanned, l.scannedBytes = 0, 0 })
	f, err := os.Create(j.path("manifest.jsonl")) // our own file, rebuilt from the start
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 256<<10)
	sk, _ := os.Create(j.path("skipped.jsonl"))
	defer sk.Close()
	n, skipped := 0, 0
	var total int64
	err = src.Walk(ctx, func(e location.Entry) error {
		b, _ := json.Marshal(entry{I: n, P: e.Rel, S: e.Size, M: e.Mtime, O: uint32(e.Mode)})
		w.Write(append(b, '\n'))
		n++
		total += e.Size
		if n%500 == 0 {
			j.setLive(func(l *live) { l.scanned, l.scannedBytes = n, total })
		}
		return nil
	}, func(s location.Skipped) {
		skipped++
		b, _ := json.Marshal(s)
		sk.Write(append(b, '\n'))
	})
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if !diskio.Present(mustDisk(src.Root())) {
			return diskio.ErrGone
		}
		return err
	}
	j.mu.Lock()
	j.st.Files, j.st.Bytes, j.st.Skipped, j.st.Phase = n, total, skipped, "copy"
	j.status = make([]byte, n)
	j.live.scanned, j.live.scannedBytes = n, total
	j.mu.Unlock()
	return j.save()
}

func mustDisk(p string) *diskio.Disk {
	v, err := diskio.VolumeOf(p)
	if err != nil {
		return diskio.NewDisk("", p, "missing", 1, time.Minute)
	}
	return diskio.NewDisk(v.Name, p, v.ID, 1, time.Minute)
}

type hashRes struct {
	h   xxh3.Hash
	err error
}

func isFileErr(err error) bool { var fe fileError; return errors.As(err, &fe) }

// copyPhase copies every pending file. While the destination reads back
// file N, the source disk already reads file N+1, so neither disk idles.
func (j *Job) copyPhase(ctx context.Context, e env, jr *Journal, accepting func() bool) error {
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return err
	}
	defer it.Close()
	src, dst := j.source(), j.st.Spec.Dest
	nextPending := func() *entry {
		for it.Next() {
			if j.status[it.E.I] == stPending {
				en := it.E
				return &en
			}
		}
		return nil
	}
	fail := func(en *entry, err error) {
		r := Rec{I: en.I, St: "failed", N: en.S, P: en.P, Err: err.Error()}
		jr.Append(r)
		j.mu.Lock()
		j.apply(r)
		j.mu.Unlock()
		j.dropCheckpoints(en.I)
	}

	if j.st.Spec.checkLater() || j.st.Spec.copyOnly() {
		for cur := nextPending(); cur != nil; cur = nextPending() {
			if !accepting() {
				return waitErr{"Waiting for memory to free up"}
			}
			j.setLive(func(l *live) { l.file = cur.P })
			began := time.Now()
			recs, err := j.copyOnly(ctx, e, cur, src.Path(cur.P), filepath.Join(dst, filepath.FromSlash(cur.P)))
			if err == nil && j.st.Spec.copyOnly() && len(recs) == 1 && recs[0].St == "copied" {
				recs[0].St = "hashed" // no check in this job
			}
			if err != nil {
				if !isFileErr(err) {
					return err
				}
				fail(cur, err)
				continue
			}
			if len(recs) > 0 {
				recs[0].Ms = time.Since(began).Milliseconds()
			}
			for _, r := range recs {
				jr.Append(r)
				j.mu.Lock()
				j.apply(r)
				j.mu.Unlock()
			}
			j.dropCheckpoints(cur.I)
		}
		return nil
	}

	if j.st.Spec.hashDuringCopy() {
		for cur := nextPending(); cur != nil; cur = nextPending() {
			if !accepting() {
				return waitErr{"Waiting for memory to free up"}
			}
			j.setLive(func(l *live) { l.file = cur.P })
			began := time.Now()
			recs, err := j.oneFileHashed(ctx, e, cur, src.Path(cur.P), filepath.Join(dst, filepath.FromSlash(cur.P)), "")
			if err != nil {
				if !isFileErr(err) {
					return err
				}
				fail(cur, err)
				continue
			}
			if len(recs) > 0 {
				recs[0].Ms = time.Since(began).Milliseconds()
			}
			for _, r := range recs {
				jr.Append(r)
				j.mu.Lock()
				j.apply(r)
				j.mu.Unlock()
			}
			j.dropCheckpoints(cur.I)
		}
		return nil
	}

	cur := nextPending()
	var pre *hashRes // S1 of cur, if already done in parallel
	for cur != nil {
		if !accepting() {
			return waitErr{"Waiting for memory to free up"}
		}
		srcPath := src.Path(cur.P)
		final := filepath.Join(dst, filepath.FromSlash(cur.P))
		var s1 xxh3.Hash
		if pre != nil {
			s1, err = pre.h, pre.err
		} else {
			j.setLive(func(l *live) { l.file, l.stage = cur.P, "Reading the source (S1)" })
			s1, err = j.hashFile(ctx, e, e.src, srcPath, cur.I, "s1", true)
		}
		pre = nil
		if err != nil {
			if !isFileErr(err) {
				return err
			}
			fail(cur, err)
			cur = nextPending()
			continue
		}

		// Read the next source file while this one is read back.
		next := nextPending()
		var nextRes chan hashRes
		sctx, cancelNext := context.WithCancel(ctx)
		startNext := func() {
			if next == nil || nextRes != nil {
				return
			}
			nextRes = make(chan hashRes, 1)
			go func(en entry) {
				h, err := j.hashFileQuiet(sctx, e, e.src, src.Path(en.P), en.I, "s1")
				nextRes <- hashRes{h, err}
			}(*next)
		}
		finish := func() {
			if nextRes != nil {
				r := <-nextRes
				pre = &r
			}
			cancelNext()
		}

		began := time.Now()
		rec, err := j.oneFile(ctx, e, cur, srcPath, final, s1, startNext)
		rec.Ms = time.Since(began).Milliseconds()
		if err != nil && !isFileErr(err) {
			cancelNext()
			if nextRes != nil {
				<-nextRes
			}
			return err
		}
		finish()
		if err != nil {
			fail(cur, err)
		} else {
			jr.Append(rec)
			j.mu.Lock()
			j.apply(rec)
			j.mu.Unlock()
			j.dropCheckpoints(cur.I)
		}
		if pre != nil && pre.err != nil && !isFileErr(pre.err) {
			// The parallel read stopped for a shared reason; redo it next time.
			pre = nil
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
		}
		cur = next
	}
	return nil
}

func (j *Job) hashFileQuiet(ctx context.Context, e env, d *diskio.Disk, p string, idx int, stage string) (xxh3.Hash, error) {
	return j.hashFileOpt(ctx, e, d, p, idx, stage, true, false)
}

// oneFile copies and proves one file. startNext begins the next file's S1
// once the copy itself is done.
func (j *Job) oneFile(ctx context.Context, e env, en *entry, srcPath, final string, s1 xxh3.Hash, startNext func()) (Rec, error) {
	rec := Rec{I: en.I, N: en.S, S1: s1.String()}
	if _, err := os.Lstat(final); err == nil {
		// Never overwrite: compare what is there instead.
		startNext()
		j.setLive(func(l *live) { l.stage = "Checking the file already at the destination" })
		d1, err := j.hashFile(ctx, e, e.dst, final, en.I, "d1", false)
		if err != nil {
			return rec, err
		}
		if d1 != s1 {
			return rec, perFile("a different file with this name is already at the destination; it was left untouched")
		}
		rec.St, rec.D1 = "identical", d1.String()
		return rec, nil
	}
	tmp := tempFor(final)
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			j.dropCheckpoints(en.I)
		}
		j.setLive(func(l *live) { l.stage = "Copying" })
		if err := j.copyFile(ctx, e, srcPath, tmp, en.I, nil); err != nil {
			if errors.Is(err, errNoSpace) {
				return rec, waitErr{"Waiting for free space on the destination"}
			}
			return rec, err
		}
		startNext()
		j.setLive(func(l *live) { l.stage = "Reading back the copy (D1)" })
		d1, err := j.hashFile(ctx, e, e.dst, tmp, en.I, "d1", false)
		if err != nil {
			return rec, err
		}
		if d1 != s1 {
			// Read the destination once more before believing it.
			j.dropCheckpoints(en.I)
			j.setLive(func(l *live) { l.stage = "Reading back the copy again" })
			d1, err = j.hashFile(ctx, e, e.dst, tmp, en.I, "d1", false)
			if err != nil {
				return rec, err
			}
		}
		if d1 == s1 {
			setTimes(tmp, en.M, os.FileMode(en.O))
			if err := place(tmp, final); err != nil {
				if errors.Is(err, errExists) {
					return rec, perFile("a file with this name appeared at the destination during the copy; the copy was kept as %s", placeFailed(tmp, final))
				}
				return rec, perFile("could not give the copy its name: %v", err)
			}
			j.setLive(func(l *live) { l.temp = "" })
			rec.St, rec.D1, rec.A = "verified", d1.String(), attempt+1
			return rec, nil
		}
	}
	kept := placeFailed(tmp, final)
	j.setLive(func(l *live) { l.temp = "" })
	return rec, perFile("the copy does not match the source after two attempts; the last copy was kept as %s", filepath.Base(kept))
}

// oneFileHashed copies one file while hashing the bytes read for the copy
// (C). Then the copy is read back (D1) on the destination disk while the
// source is read a second time (S2) on the source disk, at the same moment.
// The file gets its real name only when C, D1 and S2 all agree — or, if S2
// differs because the source itself has changed since, as a proven copy of
// what was read, with a warning. Anything else is tried once more, then
// kept aside under a plain name; nothing is deleted.
//
// copied is the C hash of a copy already made by the copy pass of the
// copy-then-check scheme; then the first attempt only checks it.
func (j *Job) oneFileHashed(ctx context.Context, e env, en *entry, srcPath, final, copied string) ([]Rec, error) {
	rec := Rec{I: en.I, N: en.S}
	tmp := tempFor(final)
	// at is where the copy now is. Since v0.2.16 a copy gets its real name as
	// soon as it is written and flushed, before it is checked; copies left by
	// v0.2.15 in the middle of a job are still under the temporary name.
	at := tmp
	if copied != "" {
		if fi, err := os.Stat(tmp); err == nil && fi.Size() == en.S {
			at = tmp
		} else if fi, err := os.Stat(final); err == nil && fi.Size() == en.S {
			at = final
		} else {
			copied = "" // the copy is not there as it was left: copy it again
		}
	}
	name := func() error { // the real name, given once and never over another file
		if at == final {
			return nil
		}
		if err := j.placeProven(tmp, final, en); err != nil {
			return err
		}
		at = final
		return nil
	}
	if _, err := os.Lstat(final); err == nil && copied == "" {
		// Never overwrite: compare what is there instead. S2 follows in s2Phase.
		j.setLive(func(l *live) { l.stage = "Reading the source (S1)" })
		s1, err := j.hashFile(ctx, e, e.src, srcPath, en.I, "s1", true)
		if err != nil {
			return nil, err
		}
		j.setLive(func(l *live) { l.stage = "Checking the file already at the destination" })
		d1, err := j.hashFile(ctx, e, e.dst, final, en.I, "d1", false)
		if err != nil {
			return nil, err
		}
		if d1 != s1 {
			return nil, perFile("a different file with this name is already at the destination; it was left untouched")
		}
		rec.St, rec.S1, rec.D1 = "identical", s1.String(), d1.String()
		return []Rec{rec}, nil
	}
	why := ""
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			j.dropCheckpoints(en.I)
		}
		var c string
		if attempt == 0 && copied != "" {
			c = copied
		} else {
			hs := xxh3.New()
			j.setLive(func(l *live) { l.stage = "Copying (the source is hashed as it is read)" })
			if err := j.copyFile(ctx, e, srcPath, tmp, en.I, hs); err != nil {
				if errors.Is(err, errNoSpace) {
					return nil, waitErr{"Waiting for free space on the destination"}
				}
				return nil, err
			}
			c = hs.Sum128().String()
			at = tmp
			if err := name(); err != nil {
				return nil, err
			}
		}

		withS2 := j.st.Spec.S2 == "now"
		s2ch := make(chan hashRes, 1)
		if withS2 {
			go func() {
				h, err := j.hashFileQuiet(ctx, e, e.src, srcPath, en.I, "s2")
				s2ch <- hashRes{h, err}
			}()
			j.setLive(func(l *live) { l.stage = "Reading back the copy (D1) and the source again (S2)" })
		} else {
			s2ch <- hashRes{}
			j.setLive(func(l *live) { l.stage = "Reading back the copy (D1)" })
		}
		d1, err := j.hashFile(ctx, e, e.dst, at, en.I, "d1", false)
		if err == nil && d1.String() != c {
			// Read the destination once more before believing it.
			j.dropCheckpoint(en.I, "d1")
			j.setLive(func(l *live) { l.stage = "Reading back the copy again" })
			d1, err = j.hashFile(ctx, e, e.dst, at, en.I, "d1", false)
		}
		s2 := <-s2ch // always wait: the source read must not outlive this file
		if err != nil {
			return nil, err
		}
		if s2.err != nil && !isFileErr(s2.err) {
			return nil, s2.err
		}
		if d1.String() != c {
			why = "the copy does not match what was read from the source"
			if err := j.unname(&at, tmp, final); err != nil {
				return nil, err
			}
			continue
		}
		rec.C, rec.D1 = c, d1.String()
		if !withS2 {
			// The destination is checked; the source is not read again.
			if err := name(); err != nil {
				return nil, err
			}
			rec.St, rec.A = "verified", attempt+1
			return []Rec{rec}, nil
		}
		if s2.err == nil && s2.h == d1 {
			if err := name(); err != nil {
				return nil, err
			}
			rec.St, rec.S2, rec.A = "strong", s2.h.String(), attempt+1
			return []Rec{rec}, nil
		}
		if sz, m, serr := statSM(srcPath); serr == nil && (sz != en.S || m != en.M) {
			// The source changed after it was listed: the copy is proven against
			// what was read for it, which is not a copy error.
			if err := name(); err != nil {
				return nil, err
			}
			rec.St, rec.A = "verified", attempt+1
			return []Rec{rec, {I: en.I, N: en.S, D1: rec.D1, St: "s2changed", P: en.P,
				Err: "the source changed after the copy (not a copy error)"}}, nil
		}
		why = "the source read differently the second time; its disk or cable may be failing"
		if s2.err != nil {
			why += " (" + s2.err.Error() + ")"
		}
		if err := j.unname(&at, tmp, final); err != nil {
			return nil, err
		}
	}
	kept := placeFailed(tmp, final)
	j.setLive(func(l *live) { l.temp = "" })
	return nil, perFile("%s, after two attempts; the last copy was kept as %s", why, filepath.Base(kept))
}

// copyOnly is the copy pass of the copy-then-check scheme (and of a copy
// with no check): the file is copied under its temporary name while the
// bytes read are hashed (C), flushed, and then given its real name at once.
// D1 and S2 follow in checkPhase or a Verify item. A file already at the
// destination is compared as in the other schemes.
func (j *Job) copyOnly(ctx context.Context, e env, en *entry, srcPath, final string) ([]Rec, error) {
	if _, err := os.Lstat(final); err == nil {
		return j.oneFileHashed(ctx, e, en, srcPath, final, "")
	}
	tmp := tempFor(final)
	hs := xxh3.New()
	j.setLive(func(l *live) { l.stage = "Copying (the source is hashed as it is read)" })
	if err := j.copyFile(ctx, e, srcPath, tmp, en.I, hs); err != nil {
		if errors.Is(err, errNoSpace) {
			return nil, waitErr{"Waiting for free space on the destination"}
		}
		return nil, err
	}
	if err := j.placeProven(tmp, final, en); err != nil {
		return nil, err
	}
	return []Rec{{I: en.I, N: en.S, St: "copied", C: hs.Sum128().String()}}, nil
}

// unname takes the real name away from a copy that failed its check, so a
// bad copy never sits under the name of a good one: it goes back to our own
// temporary name, where the next attempt replaces it or, after the last
// attempt, it is kept aside as "name.shuttle-failed-copy". Nothing is deleted.
func (j *Job) unname(at *string, tmp, final string) error {
	if *at != final {
		return nil
	}
	if _, err := os.Lstat(tmp); err == nil {
		return perFile("could not set the failed copy aside: %s is in the way", filepath.Base(tmp))
	}
	if err := os.Rename(final, tmp); err != nil {
		return perFile("could not set the failed copy aside: %v", err)
	}
	*at = tmp
	return nil
}

// checkPhase is the second pass of the copy-then-check scheme: each copy is
// read back (D1) while its source is read again (S2), and named once all
// three agree. A copy that does not match is copied once more.
func (j *Job) checkPhase(ctx context.Context, e env, jr *Journal, accepting func() bool) error {
	if j.st.CheckEnd == 0 {
		jr.Flush()
		j.mu.Lock()
		j.st.CheckEnd = jr.Size()
		j.mu.Unlock()
		j.save()
	}
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return err
	}
	defer it.Close()
	src, dst := j.source(), j.st.Spec.Dest
	var stop error
	ReadJournal(j.path("journal.log"), j.st.CheckEnd, func(r Rec) bool {
		j.mu.Lock()
		todo := r.St == "copied" && r.I < len(j.status) && j.status[r.I] == stCopied
		j.mu.Unlock()
		if !todo {
			return true
		}
		for it.E.I < r.I || it.E.P == "" {
			if !it.Next() {
				stop = errors.New("manifest ended early")
				return false
			}
		}
		if it.E.I != r.I {
			return true
		}
		if !accepting() {
			stop = waitErr{"Waiting for memory to free up"}
			return false
		}
		en := it.E
		j.setLive(func(l *live) { l.file = en.P })
		began := time.Now()
		recs, err := j.oneFileHashed(ctx, e, &en, src.Path(en.P), filepath.Join(dst, filepath.FromSlash(en.P)), r.C)
		if err != nil {
			if !isFileErr(err) {
				stop = err
				return false
			}
			recs = []Rec{{I: en.I, St: "failed", N: en.S, P: en.P, Err: err.Error()}}
		}
		recs[0].Ms = time.Since(began).Milliseconds()
		for _, out := range recs {
			jr.Append(out)
			j.mu.Lock()
			j.apply(out)
			j.mu.Unlock()
		}
		j.dropCheckpoints(en.I)
		return true
	})
	return stop
}

// placeProven gives a proven copy its real name and modified time.
func (j *Job) placeProven(tmp, final string, en *entry) error {
	setTimes(tmp, en.M, os.FileMode(en.O))
	if err := place(tmp, final); err != nil {
		if errors.Is(err, errExists) {
			return perFile("a file with this name appeared at the destination during the copy; the copy was kept as %s", placeFailed(tmp, final))
		}
		return perFile("could not give the copy its name: %v", err)
	}
	j.setLive(func(l *live) { l.temp = "" })
	return nil
}

// s2Phase reads every proven source file again and compares it with D1.
func (j *Job) s2Phase(ctx context.Context, e env, jr *Journal) error {
	if j.st.S2JournalEnd == 0 {
		jr.Flush()
		j.mu.Lock()
		j.st.S2JournalEnd = jr.Size()
		j.mu.Unlock()
		j.save()
	}
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return err
	}
	defer it.Close()
	src := j.source()
	var stop error
	ReadJournal(j.path("journal.log"), j.st.S2JournalEnd, func(r Rec) bool {
		if (r.St != "verified" && r.St != "identical") || j.status[r.I] != stVerified {
			return true
		}
		for it.E.I < r.I || it.E.P == "" {
			if !it.Next() {
				stop = errors.New("manifest ended early")
				return false
			}
		}
		en := it.E
		j.setLive(func(l *live) { l.file, l.stage = en.P, "Reading the source again (S2)" })
		h, err := j.hashFile(ctx, e, e.src, src.Path(en.P), en.I, "s2", true)
		out := Rec{I: en.I, N: en.S, S2: h.String(), D1: r.D1}
		switch {
		case err != nil && !isFileErr(err):
			stop = err
			return false
		case err == nil && h.String() == r.D1:
			out.St = "s2ok"
		default:
			out.P = en.P
			if s, m, serr := statSM(src.Path(en.P)); serr != nil || s != en.S || m != en.M {
				out.St, out.Err = "s2changed", "the source changed after the copy (not a copy error)"
			} else {
				out.St, out.Err = "s2unstable", "the source read differently the second time; its disk or cable may be failing"
				if err != nil {
					out.Err += ": " + err.Error()
				}
			}
		}
		jr.Append(out)
		j.mu.Lock()
		j.apply(out)
		j.mu.Unlock()
		j.dropCheckpoints(en.I)
		return true
	})
	return stop
}

// run carries the job forward from wherever it stands.
func (j *Job) run(ctx context.Context, e env, accepting func() bool) error {
	if j.st.Spec.isVerify() {
		return j.runVerify(ctx, e, accepting)
	}
	if j.st.Phase == "scan" {
		if err := j.scan(ctx, j.source()); err != nil {
			return err
		}
	}
	j.mu.Lock()
	j.status = make([]byte, j.st.Files) // rebuilt from the journal below
	j.c = counts{}
	j.mu.Unlock()
	jr, _, err := OpenJournal(j.path("journal.log"), func(r Rec) { j.mu.Lock(); j.apply(r); j.mu.Unlock() })
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	defer jr.Close()
	if j.st.Phase == "copy" {
		if err := j.copyPhase(ctx, e, jr, accepting); err != nil {
			return err
		}
		jr.Flush()
		j.mu.Lock()
		j.st.Phase = "s2"
		if j.st.Spec.checkLater() {
			j.st.Phase = "check"
		}
		j.mu.Unlock()
		j.save()
	}
	if j.st.Phase == "check" {
		if err := j.checkPhase(ctx, e, jr, accepting); err != nil {
			return err
		}
		jr.Flush()
		j.mu.Lock()
		j.st.Phase = "s2"
		j.mu.Unlock()
		j.save()
	}
	if j.st.Phase == "s2" && j.st.Spec.S2 == "now" {
		if err := j.s2Phase(ctx, e, jr); err != nil {
			return err
		}
	}
	jr.Flush()
	j.mu.Lock()
	j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
	j.live = live{}
	j.mu.Unlock()
	return j.save()
}
