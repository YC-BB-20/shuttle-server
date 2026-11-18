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

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// Folder is one row in the picker: a folder, or a file when files are asked for.
type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
	File bool   `json:"file,omitempty"`
	Size int64  `json:"size,omitempty"`
	// Mtime: when it last changed, for the file browser (v0.2.89).
	Mtime time.Time `json:"mtime,omitzero"`
	// Created: when it was made, where the system knows (macOS; v0.2.100).
	Created time.Time `json:"created,omitzero"`
}

// Place is one button in the picker's top row: Home, the computer's own
// disk, and every disk or server that is connected right now.
type Place struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`            // home, internal, disk, share (used for copies), server (not used)
	Share    string `json:"share,omitempty"` // for a share: smb or lucidlink
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
	// volShare is the last answer for a share's own mount point (v0.2.103).
	// A busy share (LucidLink while it uploads) can take longer than the
	// time limit to answer; it is shown as it was rather than dropped from
	// the list. Only shares: a disk that stops answering still shows as
	// not responding at once.
	volShare = map[string]shareSeen{}
)

type shareSeen struct {
	v  diskio.Volume
	at time.Time
}

// shareGrace is how long a share's last answer stands in for a slow one.
var shareGrace = 2 * time.Minute

func busyShare(p string) (diskio.Volume, bool) {
	volMu.Lock()
	defer volMu.Unlock()
	if s, ok := volShare[p]; ok && time.Since(s.at) < shareGrace {
		return s.v, true
	}
	return diskio.Volume{}, false
}

var ()

// volume reports the volume holding p; hung is true when the lookup did not
// answer in time (or an earlier one still has not).
func volume(p string, wait time.Duration) (v diskio.Volume, ok, hung bool) {
	volMu.Lock()
	if volPending[p] {
		volMu.Unlock()
		if v, ok := busyShare(p); ok {
			return v, true, false
		}
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
		volMu.Lock()
		if v.Share != "" && v.Mount == p {
			if len(volShare) > 50 {
				clear(volShare)
			}
			volShare[p] = shareSeen{v, time.Now()}
		} else {
			delete(volShare, p) // unmounted, or no longer a share: never shown from memory
		}
		volMu.Unlock()
		ch <- v
	}()
	select {
	case v := <-ch:
		return v, v.Mount != "", false
	case <-time.After(wait):
		if v, ok := busyShare(p); ok {
			return v, true, false
		}
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

// places lists the computer's own disk and every connected disk and network
// server. The computer's own disk opens at the home folder: the system's
// folders above it are never shown. A folder left behind by an unplugged
// disk is not a mount point and is not listed.
func places() []Place {
	home, _ := os.UserHomeDir()
	internal := Place{Name: rootName(), Path: home, Kind: "internal"}
	out := []Place{}
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
			if v.Mount != p && dir == "/Volumes" && e.Type().IsDir() && !seen[p] {
				// A plain folder in /Volumes may hold a mount one level
				// down: LucidLink mounts its filespace at
				// /Volumes/NAME/FILESPACE (v0.2.102). A link such as
				// "Macintosh HD" is never followed.
				seen[p] = true
				out = append(out, nestedPlaces(p, v.Mount, seen, 100-len(out))...)
				continue
			}
			if v.Mount == "/" { // "Macintosh HD" in /Volumes is the computer's own disk
				continue
			}
			if v.Mount != p || seen[v.Mount] {
				continue // not a mount point: a folder left behind by an unplugged disk, or a user folder in /media
			}
			seen[v.Mount] = true
			out = append(out, mountPlace(e.Name(), p, v))
		}
	}
	return out
}

// mountPlace is the picker's entry for a mount point.
func mountPlace(name, p string, v diskio.Volume) Place {
	pl := Place{Name: name, Path: p, Kind: "disk", Free: v.Free, Total: v.Total, ReadOnly: v.ReadOnly}
	switch {
	case v.Share != "":
		pl.Kind, pl.Share = "share", v.Share // an SMB share or the LucidLink mount: copies use it (v0.2.97–98)
	case v.Network:
		pl.Kind, pl.Note = "server", "Shuttle copies only with SMB network shares and the LucidLink mount"
	}
	return pl
}

// nestedMax is how many names nestedPlaces reads in one folder.
var nestedMax = 20

// nestedPlaces lists the mount points directly inside folder, a plain
// folder on the volume mounted at parent. It reads at most nestedMax names, and
// only mounts other than parent count: a folder left behind by an
// unplugged disk is never listed.
func nestedPlaces(folder, parent string, seen map[string]bool, room int) []Place {
	f, err := os.Open(folder)
	if err != nil {
		return nil
	}
	names, _ := f.Readdirnames(nestedMax)
	f.Close()
	sort.Strings(names)
	var out []Place
	for _, n := range names {
		if strings.HasPrefix(n, ".") || len(out) >= room {
			continue
		}
		p := filepath.Join(folder, n)
		v, ok, hung := volume(p, time.Second)
		if hung {
			out = append(out, Place{Name: n, Path: p, Kind: "disk", Note: "Not responding"})
		}
		if !ok || v.Mount != p || v.Mount == parent || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, mountPlace(n, p, v))
	}
	return out
}

// crumbs splits a path into buttons, starting at the disk it is on; on the
// computer's own disk, at the home folder.
func crumbs(p string, pl []Place) []Folder {
	home, _ := os.UserHomeDir()
	root := Folder{Name: rootName(), Path: home}
	for _, x := range pl {
		if (x.Kind == "disk" || x.Kind == "server" || x.Kind == "share") && (p == x.Path || strings.HasPrefix(p, x.Path+"/")) && len(x.Path) > len(root.Path) {
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
	onDisk, top := false, p == home
	for _, x := range pl {
		if x.Kind == "disk" || x.Kind == "server" || x.Kind == "share" {
			onDisk = onDisk || p == x.Path || strings.HasPrefix(p, x.Path+"/")
			top = top || p == x.Path
		}
	}
	if !onDisk && p != home && !strings.HasPrefix(p, home+"/") {
		p, top = home, true // the system's folders are not shown
	}
	parent := filepath.Dir(p)
	if top {
		parent = p
	}
	withFiles := r.URL.Query().Get("files") == "1"
	out := map[string]any{"path": p, "parent": parent, "top": top, "places": pl, "crumbs": crumbs(p, pl), "folders": []Folder{}}
	if v, ok, _ := volume(p, 2*time.Second); ok && v.Network && v.Share == "" {
		out["note"] = "This network server is not used for copies: Shuttle copies only with SMB network shares and the LucidLink mount."
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
			f := Folder{Name: e.Name(), Path: fp}
			if fi, err := e.Info(); err == nil {
				f.Mtime, f.Created = fi.ModTime(), diskio.Created(fi)
			}
			dirs = append(dirs, f)
		case withFiles && e.Type().IsRegular():
			f := Folder{Name: e.Name(), Path: fp, File: true}
			if fi, err := e.Info(); err == nil {
				f.Size, f.Mtime, f.Created = fi.Size(), fi.ModTime(), diskio.Created(fi)
			}
			files = append(files, f)
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

// copies says, for a folder, which copies Shuttle made of its files and how
// each file stands in each (GET /api/copies?path=…, v0.2.89). Read only.
func (s *Server) copies(w http.ResponseWriter, r *http.Request) {
	c, ok := s.Jobs.(copiesFinder)
	p := filepath.Clean(r.URL.Query().Get("path"))
	if !ok || !filepath.IsAbs(p) {
		writeJSON(w, http.StatusOK, copyjob.Copies{Places: []copyjob.CopyPlace{}, Files: map[string]copyjob.CopyFile{}, Folders: map[string]copyjob.CopyFolder{}})
		return
	}
	writeJSON(w, http.StatusOK, c.CopiesOf(p))
}
