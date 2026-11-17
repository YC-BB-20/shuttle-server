package diskio

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// noCache sets F_NOCACHE so file data does not fill the system cache and
// reads come from the disk. Best effort: an error leaves caching on.
func noCache(f *os.File) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_NOCACHE, 1)
	return e == 0
}

func dropCache(*os.File, int64, int64) {}

// NoCache is exported for writers.
func NoCache(f *os.File) bool { return noCache(f) }

func volumeOf(p string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return Volume{}, err
	}
	mount := cstr(st.Mntonname[:])
	name := filepath.Base(mount)
	if mount == "/" {
		name = "This Mac"
	}
	typ := cstr(st.Fstypename[:])
	const mntRdonly, mntLocal = 0x1, 0x1000
	from := shareFrom(cstr(st.Mntfromname[:]))
	v := Volume{ID: fmt.Sprintf("%x:%x:%s", st.Fsid.Val[0], st.Fsid.Val[1], mount), Mount: mount, Name: name,
		Free: uint64(st.Bavail) * uint64(st.Bsize), Total: uint64(st.Blocks) * uint64(st.Bsize), Type: typ,
		ReadOnly: st.Flags&mntRdonly != 0, Network: networkTypes[typ] || st.Flags&mntLocal == 0,
		Share: shareKind(typ, from, mount), From: from}
	if v.Share != "" && mount != "/" {
		v.ID = shareID(from, mount)
	}
	return v, nil
}

func cstr(b []int8) string {
	s := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		s = append(s, byte(c))
	}
	return string(s)
}
