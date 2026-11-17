package copyjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// Tunables; tests make them small.
var (
	// ChunkSize is one read or write. 16 MB since v0.2.55 (was 4 MB), the
	// same as the speed test: with the system cache bypassed there is no
	// read-ahead, so a bigger request is what keeps a USB disk's queue full.
	ChunkSize       int64         = 16 << 20
	CheckpointEvery int64         = 1 << 30
	chunkDelay      time.Duration // tests only: slows every chunk
)

// Buffers in flight: the copy keeps up to copyBufs so the source can run
// ahead of the destination; a read-back keeps two, one read while the other
// is hashed.
const (
	copyBufs = 4
	readBufs = 2
	bufAlign = 64 << 10
)

// bufPlan sizes the buffers for one file: up to want buffers of ChunkSize,
// smaller for a small file (it never gets more than it needs), and all of
// them within share bytes of the budget. They are taken as one lease, so two
// readers can never each hold part of what they need and wait for the rest.
func bufPlan(size int64, want int, share int64) (per int64, n int) {
	per = ChunkSize
	if size < per {
		per = max(bufAlign, (size+bufAlign-1)/bufAlign*bufAlign)
	}
	n = want
	if need := int((size + per - 1) / per); need < n {
		n = max(2, need)
	}
	for int64(n)*per > share && n > 2 { // fewer buffers before smaller ones
		n--
	}
	for int64(n)*per > share && per > bufAlign {
		per = max(bufAlign, per/2/bufAlign*bufAlign)
	}
	for int64(n)*per > share && n > 1 {
		n--
	}
	return per, n
}

// takeBufs takes one lease and splits it into n buffers of per bytes.
func takeBufs(ctx context.Context, b *limits.Budget, per int64, n int) (limits.Lease, [][]byte, error) {
	l, err := b.Acquire(ctx, per*int64(n))
	if err != nil {
		return l, nil, err
	}
	bufs := make([][]byte, n)
	for i := range bufs {
		lo, hi := int64(i)*per, int64(i+1)*per
		bufs[i] = l.B[lo:hi:hi]
	}
	return l, bufs, nil
}

// fileError is a problem with one file: it gets a red chip, the job goes on.
type fileError struct{ msg string }

func (e fileError) Error() string { return e.msg }

func perFile(format string, a ...any) error { return fileError{fmt.Sprintf(format, a...)} }

var errNoSpace = errors.New("not enough free space on the destination")

// noSpace is errNoSpace with the numbers for the waiting line (v0.2.92).
type noSpace struct {
	disk       string
	need, free int64
}

func (e noSpace) Error() string        { return errNoSpace.Error() }
func (e noSpace) Is(target error) bool { return target == errNoSpace }

// fullAt describes the destination disk of tmp when a file of need bytes
// does not fit.
func fullAt(tmp string, need int64) noSpace {
	ns := noSpace{disk: diskName(tmp), need: need}
	if v, err := diskio.VolumeOf(existingDir(tmp)); err == nil {
		ns.free = int64(v.Free)
		if ns.disk == "" {
			ns.disk = v.Name
		}
	}
	return ns
}

// existingDir is the nearest folder above p that exists.
func existingDir(p string) string {
	d := filepath.Dir(p)
	for {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return d
		}
		d = up
	}
}

// spaceWait is the reason a copy waits on a full destination disk. It
// needs the owner, so it says what to do; it continues by itself once
// there is room, and nothing already copied is lost.
func spaceWait(err error) waitErr {
	var ns noSpace
	disk := "The destination disk"
	if errors.As(err, &ns) && ns.disk != "" {
		disk = "Destination disk " + ns.disk
	}
	if ns.need > 0 {
		return waitErr{fmt.Sprintf("%s is full: the next file needs %s and %s is free. Make room there, or pause this copy. It continues by itself once there is room; nothing copied is lost",
			disk, Size(ns.need), Size(max(0, ns.free)))}
	}
	return waitErr{disk + " is full. Make room there, or pause this copy. It continues by itself once there is room; nothing copied is lost"}
}

type env struct {
	format    destFormat // the destination's format (v0.2.92)
	budget    *limits.Budget
	src       *diskio.Disk
	dst       *diskio.Disk
	afterCopy func(tmp string) // test hook
	copyRead  func(b []byte)   // test hook
	space     func() error     // free-space check before the copy pass (nil: none)
	wrapHash  func()           // v0.2.81: writes the hash list beside the media folder when the copy asked for it (nil: none)
	lucid     LucidClient      // v0.2.98: LucidLink's client (nil: not reachable)
	lucidMax  int64            // bytes allowed to wait for LucidLink's upload before copying pauses (0: no limit)
	log       *slog.Logger     // nil: not logged
}

func statSM(p string) (int64, int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, 0, err
	}
	return fi.Size(), fi.ModTime().UnixNano(), nil
}

// hashFile reads a whole file and hashes it, resuming from a checkpoint and
// saving one every CheckpointEvery bytes and when interrupted.
func (j *Job) hashFile(ctx context.Context, e env, d *diskio.Disk, path string, idx int, stage string, isSrc bool) (sum, error) {
	return j.hashFileOpt(ctx, e, d, path, idx, stage, isSrc, true)
}

// gone reports whether a disk went away, or the folder the copy reads from
// or writes to vanished with it: then a file that cannot be opened is a
// shared problem (the copy waits), not a failure of that file.
func (j *Job) gone(d *diskio.Disk, isSrc bool) bool {
	if !diskio.Present(d) {
		return true
	}
	root := j.st.Spec.Dest
	if isSrc {
		root = j.st.Spec.Source
	}
	_, err := os.Stat(root)
	return err != nil
}

// hashFileOpt is hashFile; show=false keeps it off the progress line (the
// next file's S1, read in parallel).
func (j *Job) hashFileOpt(ctx context.Context, e env, d *diskio.Disk, path string, idx int, stage string, isSrc, show bool) (sum, error) {
	return j.hashFileAlgo(ctx, e, d, path, idx, stage, isSrc, show, j.algo())
}

// algo is which hashes this job makes (v0.2.58).
func (j *Job) algo() string { return normAlgo(j.st.Spec.Algo) }

// hashFileAlgo is hashFileOpt with the kind of hash chosen by the caller: a
// Verify reads each file with the kind its stored record holds.
func (j *Job) hashFileAlgo(ctx context.Context, e env, d *diskio.Disk, path string, idx int, stage string, isSrc, show bool, algo string) (sum, error) {
	size, mtime, err := statSM(path)
	if err != nil {
		if j.gone(d, isSrc) {
			return sum{}, diskio.ErrGone
		}
		return sum{}, perFile("could not read: %v", err)
	}
	f, err := diskio.OpenRead(path)
	if err != nil {
		if j.gone(d, isSrc) {
			return sum{}, diskio.ErrGone
		}
		return sum{}, perFile("could not open: %v", err)
	}
	defer f.Close()
	h := newHasher(algo)
	var off int64
	if c, ok := j.checkpoint(idx, stage); ok && c.Size == size && c.Mtime == mtime && h.UnmarshalBinary(c.State) == nil && int64(h.Size()) == c.Offset {
		off = c.Offset
	} else {
		h.Reset()
	}
	save := func() error {
		st, _ := h.MarshalBinary()
		return j.saveCheckpoint(Checkpoint{Index: idx, Stage: stage, Offset: off, State: st, Size: size, Mtime: mtime})
	}
	per, nb := bufPlan(size-off, readBufs, e.budget.Total()/4)
	lease, bufs, err := takeBufs(ctx, e.budget, per, nb)
	if err != nil {
		return sum{}, err
	}
	defer e.budget.Release(lease)
	// One buffer is read while another is hashed, so the disk never waits
	// for the hasher (v0.2.55). The hash, and every checkpoint, covers
	// exactly the bytes before off, in order.
	type piece struct {
		b   []byte
		n   int
		err error
	}
	free := make(chan []byte, nb)
	for _, b := range bufs {
		free <- b
	}
	full := make(chan piece, nb)
	rctx, stopReader := context.WithCancel(ctx)
	readerDone := make(chan struct{})
	defer func() { stopReader(); <-readerDone }() // before the lease goes back
	go func(pos int64) {
		defer close(readerDone)
		for pos < size {
			var b []byte
			select {
			case b = <-free:
			case <-rctx.Done():
				return
			}
			if rem := size - pos; rem < int64(len(b)) {
				b = b[:rem]
			}
			n, err := diskio.ReadChunk(rctx, d, f, b, pos)
			if err == nil {
				time.Sleep(chunkDelay)
				if isSrc {
					j.srcBytes.Add(int64(n))
				} else {
					j.dstBytes.Add(int64(n))
				}
			}
			full <- piece{b, n, err} // never blocks: full holds every buffer
			if err != nil || n == 0 {
				return
			}
			pos += int64(n)
		}
	}(off)
	last := off
	if show {
		j.setLive(func(l *live) { l.done, l.size = off, size })
	}
	for off < size {
		var p piece
		if ctx.Err() == nil {
			select {
			case p = <-full:
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			save()
			return sum{}, context.Cause(ctx)
		}
		if err := p.err; err != nil {
			if off > last {
				save()
			}
			if errors.Is(err, diskio.ErrGone) || errors.Is(err, diskio.ErrStalled) {
				return sum{}, err
			}
			return sum{}, perFile("read error: %v", err)
		}
		if p.n == 0 {
			return sum{}, perFile("file became shorter while being read")
		}
		h.Write(p.b[:p.n])
		off += int64(p.n)
		free <- p.b[:cap(p.b)]
		if show {
			j.setLive(func(l *live) { l.done = off })
		}
		if off-last >= CheckpointEvery && off < size {
			save()
			last = off
		}
	}
	if s2, m2, err := statSM(path); err != nil || s2 != size || m2 != mtime {
		if err != nil && j.gone(d, isSrc) {
			return sum{}, diskio.ErrGone
		}
		return sum{}, perFile("changed while being read")
	}
	return h.Sum(), nil
}

// copyFile copies src to tmp at disk speed. With hs set, the bytes read for
// the copy are hashed into hs at the same time as they are written, so the
// hash never holds up the disks unless it is slower than both. It continues a
// partial copy from its checkpoint (with the hasher's saved state), and
// flushes to the drive at the end, so the read-back reads what the drive holds.
func (j *Job) copyFile(ctx context.Context, e env, src, tmp string, idx int, hs *hasher) error {
	start := time.Now()
	j.copySince.Store(start.UnixNano())
	defer func() { j.copySince.Store(0); j.copyNs.Add(int64(time.Since(start))) }()
	size, mtime, err := statSM(src)
	if err != nil {
		if j.gone(e.src, true) {
			return diskio.ErrGone
		}
		return perFile("could not read: %v", err)
	}
	var off int64
	if c, ok := j.checkpoint(idx, "copy"); ok && c.Size == size && c.Mtime == mtime {
		if fi, err := os.Stat(tmp); err == nil && fi.Size() >= c.Offset {
			if hs == nil || (hs.UnmarshalBinary(c.State) == nil && int64(hs.Size()) == c.Offset) {
				off = c.Offset
			}
		}
	}
	if hs != nil && off == 0 {
		hs.Reset()
	}
	hashState := func() []byte {
		if hs == nil {
			return nil
		}
		b, _ := hs.MarshalBinary()
		return b
	}
	// The copy's folder may not exist yet: ask the nearest folder that does
	// (v0.2.92; asking about the not-yet-made file skipped this check).
	if v, err := diskio.VolumeOf(existingDir(tmp)); err == nil && diskio.VolumeMounted(tmp) && v.Free < uint64(size-off)+(64<<20) {
		return fullAt(tmp, size-off+(64<<20))
	}
	in, err := diskio.OpenRead(src)
	if err != nil {
		if j.gone(e.src, true) {
			return diskio.ErrGone
		}
		return perFile("could not open: %v", err)
	}
	defer in.Close()
	// An unplugged disk leaves /Volumes/NAME as a plain path on the internal
	// disk: never create folders there.
	if !diskio.Present(e.dst) || !diskio.VolumeMounted(tmp) {
		return diskio.ErrGone
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		if !diskio.Present(e.dst) {
			return diskio.ErrGone
		}
		return perFile("could not create folder at destination: %v", err)
	}
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		if !diskio.Present(e.dst) {
			return diskio.ErrGone
		}
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EILSEQ) || errors.Is(err, syscall.ENAMETOOLONG) {
			return perFile("the destination disk's format does not accept this name (%v)", err)
		}
		return perFile("could not write at destination: %v", err)
	}
	defer out.Close()
	if err := out.Truncate(off); err != nil { // our own temporary file
		return perFile("could not prepare the copy: %v", err)
	}
	diskio.NoCache(out)
	j.setLive(func(l *live) { l.temp, l.done, l.size = tmp, off, size })

	// Up to four buffers of 16 MB (v0.2.55; was two of 4 MB): the source
	// reads ahead into them while the destination writes, so neither disk
	// waits for the other. One lease, at most half the budget.
	type chunk struct {
		b   []byte
		n   int
		off int64
	}
	per, nb := bufPlan(size-off, copyBufs, e.budget.Total()/2)
	lease, bufs, err := takeBufs(ctx, e.budget, per, nb)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return err
	}
	defer e.budget.Release(lease) // runs after the reader has stopped (below)
	free := make(chan []byte, nb)
	for _, b := range bufs {
		free <- b
	}
	full := make(chan chunk, nb)
	readErr := make(chan error, 1)
	rctx, stopReader := context.WithCancel(ctx)
	go func() {
		defer close(full)
		pos := off
		for pos < size {
			var l []byte
			select {
			case l = <-free:
			case <-rctx.Done():
				readErr <- nil
				return
			}
			want := l
			if rem := size - pos; rem < int64(len(want)) {
				want = want[:rem]
			}
			n, err := diskio.ReadChunk(rctx, e.src, in, want, pos)
			if err == nil && n == 0 {
				err = perFile("file became shorter while being copied")
			}
			if err != nil {
				free <- l
				readErr <- err
				return
			}
			j.srcBytes.Add(int64(n))
			if e.copyRead != nil {
				e.copyRead(want[:n])
			}
			time.Sleep(chunkDelay)
			full <- chunk{l, n, pos}
			pos += int64(n)
		}
		readErr <- nil
	}()

	// The hasher works beside the writer, one chunk at a time, so after each
	// chunk the hash covers exactly what was written.
	var hashIn chan []byte
	hashDone := make(chan struct{})
	if hs != nil {
		hashIn = make(chan []byte)
		go func() {
			for b := range hashIn {
				hs.Write(b)
				hashDone <- struct{}{}
			}
		}()
		defer close(hashIn)
	}
	written, last := off, off
	var werr error
	for c := range full {
		if werr == nil {
			var before []byte
			if hs != nil {
				before = hashState()
				hashIn <- c.b[:c.n]
			}
			err := diskio.WriteChunk(ctx, e.dst, out, c.b[:c.n], c.off)
			if hs != nil {
				<-hashDone
			}
			if err != nil {
				werr = err
				stopReader()
				if hs != nil {
					hs.UnmarshalBinary(before) // the chunk was not written: take it out of the hash
				}
			} else {
				written += int64(c.n)
				j.dstBytes.Add(int64(c.n))
				j.copyBytes.Add(int64(c.n))
				j.setLive(func(l *live) { l.done = written })
			}
		}
		free <- c.b
		if werr == nil && written-last >= CheckpointEvery && written < size {
			if err := diskio.Flush(ctx, e.dst, out); err == nil {
				j.saveCheckpoint(Checkpoint{Index: idx, Stage: "copy", Offset: written, State: hashState(), Size: size, Mtime: mtime})
				last = written
			}
		}
		if werr == nil && ctx.Err() != nil {
			stopReader()
		}
	}
	rerr := <-readErr
	stopReader()
	// Keep what was written: flush it and record where to continue.
	if werr != nil || rerr != nil || written < size {
		if written > off && diskio.Flush(context.Background(), e.dst, out) == nil {
			j.saveCheckpoint(Checkpoint{Index: idx, Stage: "copy", Offset: written, State: hashState(), Size: size, Mtime: mtime})
		}
	}
	switch {
	case ctx.Err() != nil:
		return context.Cause(ctx)
	case werr != nil:
		if errors.Is(werr, diskio.ErrGone) || errors.Is(werr, diskio.ErrStalled) {
			return werr
		}
		if errors.Is(werr, syscall.ENOSPC) || strings.Contains(werr.Error(), "no space") {
			return fullAt(tmp, size-written+(64<<20))
		}
		return perFile("write error: %v", werr)
	case rerr != nil:
		return rerr
	}
	if err := diskio.Flush(ctx, e.dst, out); err != nil {
		if errors.Is(err, diskio.ErrGone) {
			return err
		}
		return perFile("could not flush the copy to the drive: %v", err)
	}
	if hs != nil {
		if s2, m2, err := statSM(src); err != nil || s2 != size || m2 != mtime {
			if err != nil && j.gone(e.src, true) {
				return diskio.ErrGone
			}
			return perFile("changed while being read")
		}
		if size-off >= CheckpointEvery {
			// A big file: record that the copy is complete, so a pause during the
			// read-back does not copy it again.
			j.saveCheckpoint(Checkpoint{Index: idx, Stage: "copy", Offset: size, State: hashState(), Size: size, Mtime: mtime})
		}
	}
	if e.afterCopy != nil {
		e.afterCopy(tmp)
	}
	return nil
}

// tempName is the name a copy has while it is being written.
//
// Visible, not hidden (since v0.2.13): a file being copied shows in Finder
// as "clip.mov.shuttle-part" while it is written. Since v0.2.16 it gets its
// real name as soon as it is written and flushed, before it is checked; a
// copy that then fails its check loses the name again (unname).
func tempName(final string) string {
	dir, base := filepath.Split(final)
	name := base + ".shuttle-part"
	if len(name) > 250 {
		name = fmt.Sprintf("%x.shuttle-part", xxh3.Sum128([]byte(base)).Lo)
	}
	return filepath.Join(dir, name)
}

// oldTempName is the hidden name used before v0.2.13.
func oldTempName(final string) string {
	t := tempName(final)
	dir, name := filepath.Split(t)
	return filepath.Join(dir, "."+name)
}

// tempFor returns the temporary name for final, first moving a partial copy
// left under the hidden pre-v0.2.13 name, so a paused copy resumes after an
// update instead of starting the file again.
func tempFor(final string) string {
	t := tempName(final)
	if _, err := os.Lstat(t); os.IsNotExist(err) {
		if _, err := os.Lstat(oldTempName(final)); err == nil {
			os.Rename(oldTempName(final), t)
		}
	}
	return t
}

var errExists = errors.New("exists")

// place gives tmp its final name without ever replacing an existing file:
// a hard link fails if the name exists; where links are not supported
// (exFAT, FAT32) it checks, then renames.
func place(tmp, final string) error {
	err := os.Link(tmp, final)
	if err == nil {
		os.Remove(tmp) // the proven copy now has its real name; drop our temp name
		return nil
	}
	if os.IsExist(err) {
		return errExists
	}
	if _, err := os.Lstat(final); err == nil {
		return errExists
	}
	return os.Rename(tmp, final)
}

// placeFailed keeps a failed copy beside the original name for inspection.
func placeFailed(tmp, final string) string {
	for n := 1; n < 100; n++ {
		name := final + ".shuttle-failed-copy"
		if n > 1 {
			name = fmt.Sprintf("%s.shuttle-failed-copy-%d", final, n)
		}
		if place(tmp, name) == nil {
			return name
		}
	}
	return tmp
}

func setTimes(p string, mtime int64, mode os.FileMode) {
	t := time.Unix(0, mtime)
	os.Chtimes(p, t, t)
	if mode != 0 {
		os.Chmod(p, mode)
	}
}
