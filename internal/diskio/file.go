package diskio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrShort means a file ended before its recorded size: it changed, or the
// file system returned less than asked. Retryable.
var ErrShort = errors.New("file shorter than expected")

// OpenRead opens a file read-only with the system cache bypassed where the
// platform allows. Sources are only ever opened this way.
func OpenRead(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	noCache(f)
	return f, nil
}

// ReadChunk fills buf from off, retrying short reads and transient errors a
// few times. It returns how many bytes it read; fewer than len(buf) only at
// the end of the file.
func ReadChunk(ctx context.Context, d *Disk, f *os.File, buf []byte, off int64) (int, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		n := 0
		err := d.Do(ctx, func() error {
			for n < len(buf) {
				k, err := f.ReadAt(buf[n:], off+int64(n))
				n += k
				if err == io.EOF {
					return io.EOF
				}
				if err != nil {
					return err
				}
				if k == 0 {
					return ErrShort
				}
			}
			return nil
		})
		dropCache(f, off, int64(n))
		switch {
		case err == nil || err == io.EOF:
			return n, nil
		case errors.Is(err, ErrStalled), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return n, err
		case Gone(d, err):
			return n, ErrGone
		}
		lastErr = err
	}
	return 0, fmt.Errorf("read failed after retries: %w", lastErr)
}

// WriteChunk writes buf at off.
func WriteChunk(ctx context.Context, d *Disk, f *os.File, buf []byte, off int64) error {
	err := d.Do(ctx, func() error { _, err := f.WriteAt(buf, off); return err })
	if err != nil && Gone(d, err) {
		return ErrGone
	}
	return err
}

// Flush forces written data to the drive (F_FULLFSYNC on macOS).
func Flush(ctx context.Context, d *Disk, f *os.File) error {
	err := d.Do(ctx, f.Sync)
	if err != nil && Gone(d, err) {
		return ErrGone
	}
	return err
}

// Gone decides whether an error means the disk went away: its root is
// missing or is now a different volume.
func Gone(d *Disk, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
		return true
	}
	return !Present(d)
}

// Present reports whether the disk is connected and is the same volume.
func Present(d *Disk) bool {
	v, err := VolumeOf(d.Root)
	return err == nil && v.ID == d.ID
}

// VolumeMounted is false for a path under /Volumes/NAME (macOS) or
// /media/USER/NAME (Linux) whose disk is not mounted.
func VolumeMounted(path string) bool {
	clean := filepath.Clean(path)
	for _, base := range []string{"/Volumes/", "/media/"} {
		if strings.HasPrefix(clean, base) {
			parts := strings.Split(strings.TrimPrefix(clean, base), "/")
			n := 1
			if base == "/media/" {
				n = 2
			}
			if len(parts) < n {
				return true
			}
			root := base + strings.Join(parts[:n], "/")
			if _, err := os.Stat(root); err != nil {
				return false
			}
			v, err := volumeOf(root)
			return err == nil && v.Mount == root
		}
	}
	return true
}

// Volume describes the volume a path is on.
type Volume struct {
	ID    string // file-system id plus mount point
	Mount string
	Name  string
	Free  uint64
}

// VolumeOf finds the volume holding path (or its nearest existing parent).
func VolumeOf(path string) (Volume, error) {
	p := path
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return Volume{}, fmt.Errorf("%s does not exist", path)
		}
		p = parent
	}
	return volumeOf(p)
}
