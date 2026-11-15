package copyjob

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// XXH64 lists (v0.2.57). When a job made an XXH64 of each file as the
// destination was read back, it saves them in one list for other tools, in
// the format `xxhsum -H1` writes and `xxhsum -c` reads: a line per file,
// the 16-digit hash, two spaces, the path.
//
// The list goes beside the destination folder, never inside it: for a
// destination "/Volumes/Backup/Day 1" it is "/Volumes/Backup/Day 1.xxh64",
// with paths starting "Day 1/", so `xxhsum -c "Day 1.xxh64"` run in
// "/Volumes/Backup" checks the copy. When the destination is a disk's top
// folder there is nothing above it to write to, so the list goes inside it,
// with paths from there. An existing file is never replaced: the name gets
// " 2", " 3" and so on. Only files whose copy is proven are listed.

// x64Ext is the list's file extension, as other tools name XXH64 lists.
const x64Ext = ".xxh64"

// writeX64List saves the list and returns its path, or "" when the job made
// no XXH64 (the option was off, or nothing was read back).
func (j *Job) writeX64List() (string, error) {
	j.mu.Lock()
	want, files, dest := j.st.Spec.XXH64, j.st.Files, filepath.Clean(j.st.Spec.Dest)
	j.mu.Unlock()
	if !want || files == 0 {
		return "", nil
	}
	// The newest record of each file decides: its XXH64 if it has one, and
	// whether it failed. Read only for this, then dropped: 8 bytes a file.
	vals := make([]uint64, files)
	have := make([]uint64, (files+63)/64)
	bad := make([]uint64, (files+63)/64)
	set := func(b []uint64, i int, on bool) {
		if on {
			b[i/64] |= 1 << (i % 64)
		} else {
			b[i/64] &^= 1 << (i % 64)
		}
	}
	is := func(b []uint64, i int) bool { return b[i/64]&(1<<(i%64)) != 0 }
	listed := 0
	ReadJournal(j.path("journal.log"), 0, func(r Rec) bool {
		if r.I < 0 || r.I >= files {
			return true
		}
		if r.X64 != "" {
			if v, err := strconv.ParseUint(r.X64, 16, 64); err == nil {
				vals[r.I] = v
				set(have, r.I, true)
				set(bad, r.I, false)
			}
		}
		if r.St == "failed" {
			set(bad, r.I, true)
		}
		return true
	})
	for i := 0; i < files; i++ {
		if is(have, i) && !is(bad, i) {
			listed++
		}
	}
	if listed == 0 {
		return "", nil
	}
	if !diskio.VolumeMounted(dest) {
		return "", errors.New("the destination disk is not connected")
	}
	dir, prefix, name := filepath.Dir(dest), filepath.Base(dest)+"/", filepath.Base(dest)
	if v, err := diskio.VolumeOf(dest); err != nil {
		return "", err
	} else if filepath.Clean(v.Mount) == dest || dir == dest {
		dir, prefix = dest, "" // the disk's top folder: nothing above it to write to
	}
	tmp := filepath.Join(dir, "."+name+x64Ext+".shuttle-part")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) // our own temporary file
	if err != nil {
		return "", fmt.Errorf("could not write the XXH64 list beside the destination folder: %v", err)
	}
	w := bufio.NewWriterSize(f, 256<<10)
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	for it.Next() {
		if i := it.E.I; i >= 0 && i < files && is(have, i) && !is(bad, i) {
			w.WriteString(x64Line(vals[i], prefix+it.E.P))
		}
	}
	it.Close()
	err = w.Flush()
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("could not write the XXH64 list: %v", err)
	}
	for n := 1; n < 1000; n++ {
		final := filepath.Join(dir, name+x64Ext)
		if n > 1 {
			final = filepath.Join(dir, fmt.Sprintf("%s %d%s", name, n, x64Ext))
		}
		switch err := place(tmp, final); {
		case err == nil:
			return final, nil
		case !errors.Is(err, errExists):
			os.Remove(tmp)
			return "", fmt.Errorf("could not name the XXH64 list: %v", err)
		}
	}
	os.Remove(tmp)
	return "", errors.New("could not find a free name for the XXH64 list")
}

// noteX64List keeps where the list went, or why it could not be saved.
func (j *Job) noteX64List(p string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.st.X64List, j.st.X64Err = p, ""
	if err != nil {
		j.st.X64Err = err.Error()
	}
}

// x64Line is one line as `xxhsum -H1` writes it. A path with a backslash
// or a line break is written escaped, with a backslash before the line,
// as xxhsum and the GNU checksum tools do.
func x64Line(v uint64, path string) string {
	if strings.ContainsAny(path, "\\\n\r") {
		path = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r").Replace(path)
		return fmt.Sprintf("\\%016x  %s\n", v, path)
	}
	return fmt.Sprintf("%016x  %s\n", v, path)
}
