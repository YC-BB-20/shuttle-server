package diskio

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func noCache(*os.File) bool { return true }

// dropCache tells Linux not to keep the bytes just read or written.
func dropCache(f *os.File, off, n int64) {
	syscall.Syscall6(syscall.SYS_FADVISE64, f.Fd(), uintptr(off), uintptr(n), 4 /* DONTNEED */, 0, 0)
}

// NoCache is exported for writers.
func NoCache(f *os.File) bool { return noCache(f) }

func volumeOf(p string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return Volume{}, err
	}
	mount, typ := mountPoint(p)
	const stRdonly = 0x1
	return Volume{ID: fmt.Sprintf("%x:%x:%s", st.Fsid.X__val[0], st.Fsid.X__val[1], mount), Mount: mount,
		Name: filepath.Base(mount), Free: uint64(st.Bavail) * uint64(st.Bsize), Total: uint64(st.Blocks) * uint64(st.Bsize),
		Type: typ, ReadOnly: st.Flags&stRdonly != 0, Network: networkTypes[typ]}, nil
}

// mountPoint is the longest mount in /proc/self/mounts that contains p, and
// its file system type.
func mountPoint(p string) (string, string) {
	abs, _ := filepath.Abs(p)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	best, typ := "/", ""
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return best, typ
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 3 {
			continue
		}
		m := strings.ReplaceAll(fs[1], `\040`, " ")
		if (abs == m || strings.HasPrefix(abs, strings.TrimSuffix(m, "/")+"/")) && len(m) >= len(best) {
			best, typ = m, fs[2]
		}
	}
	return best, typ
}
