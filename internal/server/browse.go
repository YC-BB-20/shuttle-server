package server

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// Folder is one row in the picker: a folder, or a file when files are asked for.
type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
	File bool   `json:"file,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// Place is one button in the picker's top row: Home, the computer's own
// disk, and every disk or server that is connected right now.
type Place struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"` // home, internal, disk, server
	Free     uint64 `json:"free,omitempty"`
	Total    uint64 `json:"total,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Note     string `json:"note,omitempty"` // why it cannot be used, if it cannot
}

// maxListed bounds one folder listing.
const maxListed = 2000

// volume looks up a mounted volume with a time limit, since a network share
// or a dying disk can make the lookup hang. Only one lookup per path is ever
// left waiting, so a hung disk cannot pile up threads.
var (
	volMu      sync.Mutex
	volPending = map[string]bool{}
)

// volume reports the volume holding p; hung is true when the lookup did not
// answer in time (or an earlier one still has not).
func volume(p string, wait time.Duration) (v diskio.Volume, ok, hung bool) {
	volMu.Lock()
	if volPending[p] {
		volMu.Unlock()
		return diskio.Volume{}, false, true
	}
	volPending[p] = true
	volMu.Unlock()
	ch := make(chan diskio.Volume, 1)
	go func() {
		v, err := diskio.VolumeOf(p)
		volMu.Lock()
		delete(volPending, p)
		volMu.Unlock()
		if err != nil {
			v = diskio.Volume{}
		}
		ch <- v
	}()
	select {
	case v := <-ch:
		return v, v.Mount != "", false
	case <-time.After(wait):
		return diskio.Volume{}, false, true
	}
}

// mountDirs are the folders whose entries may be mounted disks.
func mountDirs() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/Volumes"}
	}
	dirs := []string{"/mnt"}
	for _, base := range []string{"/media", "/run/media"} {
		users, _ := os.ReadDir(base)
		for _, u := range users {
			dirs = append(dirs, filepath.Join(base, u.Name()))
		}
		dirs = append(dirs, base)
	}
	return dirs
}

func rootName() string {
	if runtime.GOOS == "darwin" {
		return "Macintosh HD"
	}
	return "This computer"
}

// places lists Home, the computer's own disk and every connected disk and
// network server. A folder left behind by an unplugged disk is not a mount
// point and is not listed.
func places() []Place {
	home, _ := os.UserHomeDir()
	out := []Place{{Name: "Home", Path: home, Kind: "home"}}
	internal := Place{Name: rootName(), Path: "/", Kind: "internal"}
	if v, ok, _ := volume("/", 2*time.Second); ok {
		internal.Free, internal.Total = v.Free, v.Total
	}
	out = append(out, internal)
	seen := map[string]bool{"/": true}
	for _, dir := range mountDirs() {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") || len(out) > 100 {
				continue
			}
			p := filepath.Join(dir, e.Name())
			v, ok, hung := volume(p, time.Second)
			if hung {
				out = append(out, Place{Name: e.Name(), Path: p, Kind: "disk", Note: "Not responding"})
			}
			if !ok {
				continue
			}
			if v.Mount == "/" { // "Macintosh HD" in /Volumes is the computer's own disk
				continue
			}
			if v.Mount != p || seen[v.Mount] {
				continue // not a mount point: a folder left behind by an unplugged disk, or a user folder in /media
			}
			seen[v.Mount] = true
			pl := Place{Name: e.Name(), Path: p, Kind: "disk", Free: v.Free, Total: v.Total, ReadOnly: v.ReadOnly}
			if v.Network {
				pl.Kind, pl.Note = "server", "Network servers come later: copies need a disk connected by cable for now"
			}
			out = append(out, pl)
		}
	}
	return out
}

// crumbs splits a path into buttons, starting at the disk it is on.
func crumbs(p string, pl []Place) []Folder {
	root := Folder{Name: rootName(), Path: "/"}
	for _, x := range pl {
		if (x.Kind == "disk" || x.Kind == "server") && (p == x.Path || strings.HasPrefix(p, x.Path+"/")) && len(x.Path) > len(root.Path) {
			root = Folder{Name: x.Name, Path: x.Path}
		}
	}
	out := []Folder{root}
	rest := strings.Trim(strings.TrimPrefix(p, root.Path), "/")
	if rest == "" {
		return out
	}
	at := root.Path
	for _, part := range strings.Split(rest, "/") {
		at = filepath.Join(at, part)
		out = append(out, Folder{Name: part, Path: at})
	}
	return out
}

// browse lists a folder for the picker: its folders, and its files too when
// files=1. Names only, at most maxListed rows.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	pl := places()
	p := r.URL.Query().Get("path")
	if p == "" {
		p = home
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}
	withFiles := r.URL.Query().Get("files") == "1"
	out := map[string]any{"path": p, "parent": filepath.Dir(p), "places": pl, "crumbs": crumbs(p, pl), "folders": []Folder{}}
	if v, ok, _ := volume(p, 2*time.Second); ok && v.Network {
		out["note"] = "This is a network server. Copies to or from network servers come later."
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		out["error"] = "This folder cannot be opened."
		writeJSON(w, http.StatusOK, out)
		return
	}
	var dirs, files []Folder
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if len(dirs)+len(files) >= maxListed {
			out["truncated"] = true
			break
		}
		fp := filepath.Join(p, e.Name())
		switch {
		case e.IsDir() || (e.Type()&os.ModeSymlink != 0 && isDir(fp)):
			dirs = append(dirs, Folder{Name: e.Name(), Path: fp})
		case withFiles && e.Type().IsRegular():
			var n int64
			if fi, err := e.Info(); err == nil {
				n = fi.Size()
			}
			files = append(files, Folder{Name: e.Name(), Path: fp, File: true, Size: n})
		}
	}
	byName := func(l []Folder) {
		sort.Slice(l, func(a, b int) bool { return strings.ToLower(l[a].Name) < strings.ToLower(l[b].Name) })
	}
	byName(dirs)
	byName(files)
	if all := append(dirs, files...); all != nil {
		out["folders"] = all
	}
	writeJSON(w, http.StatusOK, out)
}
