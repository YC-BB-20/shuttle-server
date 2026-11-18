package diskio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
			if err == nil && v.Mount == root {
				return true
			}
			// LucidLink mounts its filespace one folder deeper, at
			// /Volumes/NAME/FILESPACE (v0.2.102): the path counts as
			// mounted when its own volume is mounted inside root.
			at := clean
			for len(at) > len(root) {
				if _, err := os.Stat(at); err == nil {
					break
				}
				at = filepath.Dir(at)
			}
			v, err = volumeOf(at)
			return err == nil && strings.HasPrefix(v.Mount, root+"/")
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
	Total uint64
	Type  string // file system, for example apfs, hfs, exfat, smbfs
	// ReadOnly and Network come from the mount. Of the network mounts only
	// an SMB share and the LucidLink mount are used for copies (Share set,
	// v0.2.97–98); the others are listed in the picker only.
	ReadOnly bool
	Network  bool
	Share    string // ShareSMB, ShareLucid or ""
	From     string // the mount's source, without a user name
}

// Network mounts copies may use (DESIGN.md section 19).
const (
	ShareSMB   = "smb"       // an SMB share, for example a CTERA Edge Filer
	ShareLucid = "lucidlink" // the LucidLink desktop client's mount
)

// CopyOK says whether copies may read from and write to the volume.
func (v Volume) CopyOK() bool { return !v.Network || v.Share != "" }

// shareKind tells an SMB share or the LucidLink mount from the file system
// type, the mount's source and its mount point.
// A disk the owner merely named "Lucid…" is not LucidLink: the mount
// point's name counts only on a file system that is not a disk's own.
func shareKind(typ, from, mount string) string {
	t := strings.ToLower(typ)
	if strings.Contains(strings.ToLower(typ+" "+from), "lucid") {
		return ShareLucid
	}
	switch t {
	case "smbfs", "cifs", "smb3":
		// The LucidLink app on macOS serves its filespace as an SMB share
		// from this computer itself (//guest:@127.0.0.1:22000/NAME). A
		// share from this computer is never a filer (v0.2.102).
		if localHost(from) {
			return ShareLucid
		}
		return ShareSMB
	case "apfs", "hfs", "exfat", "msdos", "vfat", "fat32", "ntfs", "ntfs3", "ext2", "ext3", "ext4", "btrfs", "xfs", "udf", "cd9660", "tmpfs":
		return ""
	}
	if strings.Contains(strings.ToLower(mount), "lucid") {
		return ShareLucid
	}
	return ""
}

// localHost says whether a share's source ("//host:port/share") is served
// by this computer.
func localHost(from string) bool {
	h := strings.TrimPrefix(from, "//")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndexByte(h, '@'); i >= 0 {
		h = h[i+1:]
	}
	h = strings.ToLower(h)
	if strings.HasPrefix(h, "[") { // [::1]:port
		if i := strings.IndexByte(h, ']'); i > 0 {
			h = h[1:i]
		}
	} else if i := strings.LastIndexByte(h, ':'); i >= 0 && strings.Count(h, ":") == 1 {
		h = h[:i]
	}
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

// shareFrom drops the user name from a mount's source:
// "//ana@filer.local/Media" becomes "//filer.local/Media".
func shareFrom(from string) string {
	pre, s := "", from
	if strings.HasPrefix(s, "//") {
		pre, s = "//", s[2:]
	}
	host, rest := s, ""
	if i := strings.IndexByte(s, '/'); i >= 0 {
		host, rest = s[:i], s[i:]
	}
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	return pre + strings.ToLower(host) + rest
}

// shareID identifies a share by its source and mount point rather than by
// the file-system id, which changes when macOS reconnects a share.
func shareID(from, mount string) string { return "share:" + from + ":" + mount }

// networkTypes are file systems reached over the network.
var networkTypes = map[string]bool{"smbfs": true, "afpfs": true, "nfs": true, "nfs4": true, "webdav": true,
	"cifs": true, "smb3": true, "fuse.sshfs": true, "davfs": true, "9p": true}

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
