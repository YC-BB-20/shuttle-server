package copyjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// Tunables; tests make them small.
var (
	ChunkSize       int64         = 4 << 20
	CheckpointEvery int64         = 1 << 30
	chunkDelay      time.Duration // tests only: slows every chunk
)

// fileError is a problem with one file: it gets a red chip, the job goes on.
type fileError struct{ msg string }

func (e fileError) Error() string { return e.msg }

func perFile(format string, a ...any) error { return fileError{fmt.Sprintf(format, a...)} }

var errNoSpace = errors.New("not enough free space on the destination")

type env struct {
	budget    *limits.Budget
	src       *diskio.Disk
	dst       *diskio.Disk
	afterCopy func(tmp string) // test hook
	copyRead  func(b []byte)   // test hook
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
func (j *Job) hashFile(ctx context.Context, e env, d *diskio.Disk, path string, idx int, stage string, isSrc bool) (xxh3.Hash, error) {
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
func (j *Job) hashFileOpt(ctx context.Context, e env, d *diskio.Disk, path string, idx int, stage string, isSrc, show bool) (xxh3.Hash, error) {
	size, mtime, err := statSM(path)
	if err != nil {
		if j.gone(d, isSrc) {
			return xxh3.Hash{}, diskio.ErrGone
		}
		return xxh3.Hash{}, perFile("could not read: %v", err)
	}
	f, err := diskio.OpenRead(path)
	if err != nil {
		if j.gone(d, isSrc) {
			return xxh3.Hash{}, diskio.ErrGone
		}
		return xxh3.Hash{}, perFile("could not open: %v", err)
	}
	defer f.Close()
	h := xxh3.New()
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
	lease, err := e.budget.Acquire(ctx, ChunkSize)
	if err != nil {
		return xxh3.Hash{}, err
	}
	defer e.budget.Release(lease)
	buf := lease.B
	last := off
	if show {
		j.setLive(func(l *live) { l.done, l.size = off, size })
	}
	for off < size {
		if ctx.Err() != nil {
			save()
			return xxh3.Hash{}, context.Cause(ctx)
		}
		want := buf
		if rem := size - off; rem < int64(len(want)) {
			want = want[:rem]
		}
		n, err := diskio.ReadChunk(ctx, d, f, want, off)
		if err != nil {
			if ctx.Err() == nil && off > last {
				save()
			}
			if errors.Is(err, diskio.ErrGone) || errors.Is(err, diskio.ErrStalled) || ctx.Err() != nil {
				if ctx.Err() != nil {
					return xxh3.Hash{}, context.Cause(ctx)
				}
				return xxh3.Hash{}, err
			}
			return xxh3.Hash{}, perFile("read error: %v", err)
		}
		if n == 0 {
			return xxh3.Hash{}, perFile("file became shorter while being read")
		}
		h.Write(want[:n])
		off += int64(n)
		time.Sleep(chunkDelay)
		if isSrc {
			j.srcBytes.Add(int64(n))
		} else {
			j.dstBytes.Add(int64(n))
		}
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
			return xxh3.Hash{}, diskio.ErrGone
		}
		return xxh3.Hash{}, perFile("changed while being read")
	}
	return h.Sum128(), nil
}

// copyFile copies src to tmp at disk speed. With hs set, the bytes read for
// the copy are hashed into hs at the same time as they are written, so the
// hash never holds up the disks unless it is slower than both. It continues a
// partial copy from its checkpoint (with the hasher's saved state), and
// flushes to the drive at the end, so the read-back reads what the drive holds.
func (j *Job) copyFile(ctx context.Context, e env, src, tmp string, idx int, hs *xxh3.Hasher) error {
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
	if v, err := diskio.VolumeOf(tmp); err == nil && v.Free < uint64(size-off)+(64<<20) {
		return errNoSpace
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
		return perFile("could not write at destination: %v", err)
	}
	defer out.Close()
	if err := out.Truncate(off); err != nil { // our own temporary file
		return perFile("could not prepare the copy: %v", err)
	}
	diskio.NoCache(out)
	j.setLive(func(l *live) { l.temp, l.done, l.size = tmp, off, size })

	// Two buffers: the source is read into one while the other is written.
	type chunk struct {
		lease limits.Lease
		n     int
		off   int64
	}
	free := make(chan limits.Lease, 2)
	for i := 0; i < 2; i++ {
		l, err := e.budget.Acquire(ctx, ChunkSize)
		if err != nil {
			close(free)
			for l := range free {
				e.budget.Release(l)
			}
			return context.Cause(ctx)
		}
		free <- l
	}
	full := make(chan chunk, 2)
	readErr := make(chan error, 1)
	rctx, stopReader := context.WithCancel(ctx)
	go func() {
		defer close(full)
		pos := off
		for pos < size {
			var l limits.Lease
			select {
			case l = <-free:
			case <-rctx.Done():
				readErr <- nil
				return
			}
			want := l.B
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
				hashIn <- c.lease.B[:c.n]
			}
			err := diskio.WriteChunk(ctx, e.dst, out, c.lease.B[:c.n], c.off)
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
		free <- c.lease
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
	close(free)
	for l := range free {
		e.budget.Release(l)
	}
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
		if strings.Contains(werr.Error(), "no space") {
			return errNoSpace
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
