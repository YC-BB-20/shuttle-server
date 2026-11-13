// Package location is the boundary between the copy code and where files
// live. Today there is one kind: a folder on a volume the computer mounts.
// Vendor media (section 17) will be another kind behind the same interface.
package location

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one regular file found by a walk.
type Entry struct {
	Rel   string // slash-separated path inside the location
	Size  int64
	Mtime int64 // unix nanoseconds
	Mode  fs.FileMode
}

// Skipped is something a walk did not take, with the reason.
type Skipped struct {
	Rel, Why string
}

// Source is a place files are read from. It is only ever read.
type Source interface {
	Root() string
	Walk(ctx context.Context, file func(Entry) error, skip func(Skipped)) error
	Path(rel string) string
}

// Folder is a folder on a mounted volume. When Only is set, just those
// entries of the folder itself (files or folders, by name) are taken.
type Folder struct {
	Dir  string
	Only []string
	// System leaves out the files and folders macOS and Windows make for
	// themselves (IsSystemName); AppleDouble also leaves out "._" files.
	System, AppleDouble bool
}

// WhySystem is the reason given for a system file left out by a walk.
const WhySystem = "a system file, left out (Settings)"

// systemNames are names the operating system writes for itself: Finder
// rewrites .DS_Store whenever a folder is opened, so a copy of one can
// never stay proven. Compared without regard to case.
var systemNames = map[string]bool{
	".ds_store": true, ".spotlight-v100": true, ".trashes": true, ".fseventsd": true,
	".temporaryitems": true, ".documentrevisions-v100": true, "thumbs.db": true, "desktop.ini": true,
}

// IsSystemName reports a name the system makes for itself, and with
// appleDouble also a "._" file (Mac information on a non-Mac disk).
func IsSystemName(name string, appleDouble bool) bool {
	return systemNames[strings.ToLower(name)] || appleDouble && strings.HasPrefix(name, "._")
}

// IsSystemPath reports whether any part of a slash-separated path is a
// system name.
func IsSystemPath(rel string, appleDouble bool) bool {
	for _, n := range strings.Split(rel, "/") {
		if IsSystemName(n, appleDouble) {
			return true
		}
	}
	return false
}

func (f Folder) Root() string           { return f.Dir }
func (f Folder) Path(rel string) string { return filepath.Join(f.Dir, filepath.FromSlash(rel)) }

// IsShuttleTemp reports Shuttle's own temporary and failed-copy names, which
// a walk never copies.
func IsShuttleTemp(name string) bool {
	return strings.HasSuffix(name, ".shuttle-part") || strings.Contains(name, ".shuttle-failed")
}

// Walk visits every regular file. Directories are read in batches and only a
// stack of folder paths is held, never a list of every file.
func (f Folder) Walk(ctx context.Context, file func(Entry) error, skip func(Skipped)) error {
	var only map[string]bool
	if len(f.Only) > 0 {
		only = make(map[string]bool, len(f.Only))
		for _, n := range f.Only {
			only[n] = true
		}
	}
	stack := []string{""}
	for len(stack) > 0 {
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		d, err := os.Open(f.Path(rel))
		if err != nil {
			skip(Skipped{rel, "could not open folder: " + err.Error()})
			continue
		}
		for {
			if err := ctx.Err(); err != nil {
				d.Close()
				return err
			}
			ents, err := d.ReadDir(512)
			for _, e := range ents {
				if only != nil && rel == "" && !only[e.Name()] {
					continue
				}
				r := e.Name()
				if rel != "" {
					r = rel + "/" + e.Name()
				}
				switch {
				case f.System && IsSystemName(e.Name(), f.AppleDouble):
					skip(Skipped{r, WhySystem})
				case e.IsDir():
					stack = append(stack, r)
				case e.Type()&fs.ModeSymlink != 0:
					skip(Skipped{r, "a link, not copied"})
				case !e.Type().IsRegular():
					skip(Skipped{r, "not a regular file"})
				case IsShuttleTemp(e.Name()):
				default:
					info, err := e.Info()
					if err != nil {
						skip(Skipped{r, err.Error()})
						continue
					}
					if err := file(Entry{r, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()}); err != nil {
						d.Close()
						return err
					}
				}
			}
			if err != nil {
				break // io.EOF or a read error: this folder is done
			}
		}
		d.Close()
	}
	return nil
}
