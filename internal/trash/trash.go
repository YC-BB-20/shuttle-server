// Package trash is the only code in Shuttle that removes a user's files: the
// Delete function (v0.2.106, COMPATIBILITY.md rule 2). Everything else in
// Shuttle may only remove its own temp files.
//
// MoveToTrash moves an item into the Trash of the disk it is on, so it can be
// taken back: ~/.Trash for this computer's own disk, VOLUME/.Trashes/UID on
// another disk (macOS); the freedesktop Trash on Linux. It is a rename on the
// same disk, so nothing is copied and nothing is half-moved. When that is not
// possible it fails; it never falls back to deleting for good.
//
// RemoveForGood deletes an item that cannot go to a Trash (an SMB share or
// the LucidLink mount). Symbolic links are removed, never followed.
package trash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrNoTrash means the item could not be moved to a Trash; it is unchanged.
var ErrNoTrash = errors.New("could not be moved to the Trash")

// sameDisk reports whether two existing paths are on the same file system;
// replaced in tests.
var sameDisk = func(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && devOf(ia) == devOf(ib)
}

// MoveToTrash moves path into the Trash of its disk, whose root is mount,
// and returns where it went. The item keeps its name unless the Trash
// already holds that name; then " 2", " 3"… is added, like the Finder.
func MoveToTrash(path, mount string) (string, error) {
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); err != nil {
		return "", err
	}
	dir, info, err := trashFor(path, mount)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTrash, err)
	}
	if !sameDisk(filepath.Dir(path), dir) {
		return "", fmt.Errorf("%w: its Trash is on another disk", ErrNoTrash)
	}
	name := filepath.Base(path)
	for n := 1; n <= 9999; n++ {
		cand := name
		if n > 1 {
			ext := filepath.Ext(name)
			if ext == name { // ".hidden"
				ext = ""
			}
			cand = strings.TrimSuffix(name, ext) + " " + strconv.Itoa(n) + ext
		}
		to := filepath.Join(dir, cand)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		if info != "" {
			// Linux: the .trashinfo is claimed first (O_EXCL), so two deletes never pick one name.
			f, err := os.OpenFile(filepath.Join(info, cand+".trashinfo"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					continue
				}
				return "", fmt.Errorf("%w: %v", ErrNoTrash, err)
			}
			fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", escape(path), time.Now().Format("2006-01-02T15:04:05"))
			f.Close()
			if err := os.Rename(path, to); err != nil {
				os.Remove(filepath.Join(info, cand+".trashinfo"))
				return "", fmt.Errorf("%w: %v", ErrNoTrash, err)
			}
			return to, nil
		}
		if err := os.Rename(path, to); err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoTrash, err)
		}
		return to, nil
	}
	return "", fmt.Errorf("%w: the Trash already holds too many items called %q", ErrNoTrash, name)
}

// trashFor returns the Trash folder for path (made if missing) and, on
// Linux, the folder for its .trashinfo records.
func trashFor(path, mount string) (dir, info string, err error) {
	home, _ := os.UserHomeDir()
	uid := strconv.Itoa(os.Getuid())
	if runtime.GOOS == "darwin" {
		if home != "" && sameDisk(filepath.Dir(path), home) {
			dir = filepath.Join(home, ".Trash")
			return dir, "", os.MkdirAll(dir, 0o700)
		}
		if mount == "" || mount == "/" {
			return "", "", errors.New("no Trash found for this disk")
		}
		top := filepath.Join(mount, ".Trashes")
		if err := os.Mkdir(top, 0o1333); err != nil && !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
		dir = filepath.Join(top, uid)
		return dir, "", os.MkdirAll(dir, 0o700)
	}
	base := ""
	if home != "" && sameDisk(filepath.Dir(path), home) {
		base = filepath.Join(home, ".local", "share", "Trash")
	} else if mount != "" && mount != "/" {
		base = filepath.Join(mount, ".Trash-"+uid)
	} else {
		return "", "", errors.New("no Trash found for this disk")
	}
	dir, info = filepath.Join(base, "files"), filepath.Join(base, "info")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	return dir, info, os.MkdirAll(info, 0o700)
}

// escape percent-encodes a path for a .trashinfo record.
func escape(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || c == '-' || c == '_' || c == '.' || c == '~' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// RemoveForGood deletes path and, for a folder, everything in it. Links
// are removed, not followed. It stops at the first error, leaving the rest.
func RemoveForGood(path string) error {
	return os.RemoveAll(filepath.Clean(path))
}
