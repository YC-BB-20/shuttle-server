package launcher

import (
	"os"
	"path/filepath"
	"runtime"
)

const prettyName = "Shuttle Server"

// PrettyName renames the program file from "shuttle-server" to "Shuttle
// Server" and leaves "shuttle-server" behind as a shortcut to it, so the old
// start command still works. macOS lists a background item (System Settings →
// Login Items) by its program file's name, and for a program with no Apple
// developer signature that name cannot be set any other way. It returns the
// path to use from now on; if anything is in the way it changes nothing.
func PrettyName(bin string) string {
	if runtime.GOOS != "darwin" {
		return bin
	}
	return prettyAt(bin)
}

func prettyAt(bin string) string {
	if filepath.Base(bin) != "shuttle-server" {
		return bin
	}
	renamed := filepath.Join(filepath.Dir(bin), prettyName)
	if _, err := os.Lstat(renamed); err == nil {
		return bin // something is already there: leave both alone
	}
	if err := os.Rename(bin, renamed); err != nil {
		return bin
	}
	os.Symlink(prettyName, bin) // best effort: only the old start command needs it
	return renamed
}
