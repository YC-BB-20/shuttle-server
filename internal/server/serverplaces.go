package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// Servers as places (v0.2.122): every server added in Settings is a place in
// Files and the folder picker, beside the drives. Its path is "srv:ID/…"
// (remote.Place); a copy to, from or between servers is a server copy job.
// Logins never reach the page: only the server's name and id.

// serverPlaces lists the added servers as places.
func (s *Server) serverPlaces() []Place {
	if s.Servers == nil {
		return nil
	}
	list, err := s.Servers.List()
	if err != nil {
		return nil
	}
	out := make([]Place, 0, len(list))
	for _, p := range list {
		out = append(out, Place{Name: p.Name, Path: remote.Place(p.ID, ""), Kind: "remote", Share: p.Kind})
	}
	return out
}

// openPlace opens the server of a place.
func (s *Server) openPlace(p string) (remote.Remote, remote.Public, string, error) {
	id, rel, ok := remote.SplitPlace(p)
	if !ok || s.Servers == nil {
		return nil, remote.Public{}, "", errors.New("this is not a place on a server")
	}
	r, pub, err := s.Servers.Open(id)
	if err != nil {
		return nil, remote.Public{}, "", errors.New("this server is no longer in Settings")
	}
	return r, pub, rel, nil
}

// browseServer lists one folder on a server, like browse does on a disk.
func (s *Server) browseServer(w http.ResponseWriter, r *http.Request, p string, pl []Place) {
	id, rel, _ := remote.SplitPlace(p)
	out := map[string]any{"path": remote.Place(id, rel), "parent": remote.Place(id, path.Dir("/" + rel)[1:]), "top": rel == "",
		"places": pl, "folders": []Folder{}}
	if rel == "" {
		out["parent"] = remote.Place(id, "")
	}
	rem, pub, _, err := s.openPlace(p)
	if err != nil {
		out["path"], out["crumbs"], out["error"] = p, []Folder{{Name: "Server", Path: p}}, err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	crumbs := []Folder{{Name: pub.Name, Path: remote.Place(id, "")}}
	if rel != "" {
		at := ""
		for _, part := range strings.Split(rel, "/") {
			at = path.Join(at, part)
			crumbs = append(crumbs, Folder{Name: part, Path: remote.Place(id, at)})
		}
	}
	out["crumbs"] = crumbs
	withFiles := r.URL.Query().Get("files") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second) // inside the page's own 15 s limit
	defer cancel()
	var dirs, files []Folder
	err = rem.List(ctx, rel, func(e remote.Entry) error {
		name := path.Base(e.Path)
		if strings.HasPrefix(name, ".") || remote.IsTemp(name) {
			return nil
		}
		if len(dirs)+len(files) >= maxListed {
			out["truncated"] = true
			return errStopList
		}
		f := Folder{Name: name, Path: remote.Place(id, e.Path), Mtime: e.Mod}
		switch {
		case e.Dir:
			dirs = append(dirs, f)
		case withFiles:
			f.File, f.Size = true, e.Size
			files = append(files, f)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopList) {
		out["error"] = fmt.Sprintf("This folder on %s could not be read: %v", pub.Name, err)
		writeJSON(w, http.StatusOK, out)
		return
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

var errStopList = errors.New("listed enough")

// mkdirServer makes an empty folder on a server. S3 has no empty folders:
// there a folder exists once something is copied into it.
func (s *Server) mkdirServer(r *http.Request, parent, name string) (any, error) {
	rem, pub, rel, err := s.openPlace(parent)
	if err != nil {
		return nil, err
	}
	if pub.Kind == remote.KindS3 {
		return nil, errors.New("S3 has no empty folders: copy into this place and the folder appears with what is copied")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	p := path.Join(rel, name)
	if _, err := rem.Stat(ctx, p); err == nil {
		return nil, errors.New("something called \"" + name + "\" is already here")
	} else if !errors.Is(err, remote.ErrNotFound) {
		return nil, err
	}
	if err := rem.Mkdir(ctx, p); err != nil {
		return nil, err
	}
	id, _, _ := remote.SplitPlace(parent)
	return map[string]string{"path": remote.Place(id, p)}, nil
}

// moveServer moves ticked items into another folder on the same server: a
// rename on the server, nothing passes through this computer. Everything is
// checked before anything moves; an existing name is never replaced.
func (s *Server) moveServer(r *http.Request, folder string, names []string, to string) (any, error) {
	fid, frel, ok1 := remote.SplitPlace(folder)
	tid, trel, ok2 := remote.SplitPlace(to)
	if !ok1 || !ok2 {
		return nil, errors.New("Move works only within one drive, share or server. To another place, use Copy.")
	}
	if fid != tid {
		return nil, errors.New("Move works only within one server. To another server or drive, use Copy.")
	}
	if frel == trel {
		return nil, errors.New("they are already in that folder")
	}
	rem, pub, _, err := s.openPlace(folder)
	if err != nil {
		return nil, err
	}
	if pub.Kind == remote.KindS3 {
		return nil, errors.New("S3 has no rename, so Shuttle cannot move here without copying every byte again on the server. Use Copy instead.")
	}
	if len(names) == 0 || len(names) > 2000 {
		return nil, errors.New("choose between 1 and 2000 items to move")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	seen := map[string]bool{}
	var froms []string
	for _, n := range names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\\x00") || seen[n] {
			return nil, fmt.Errorf("%q cannot be moved", n)
		}
		seen[n] = true
		p := path.Join(frel, n)
		if p == trel || strings.HasPrefix(trel, p+"/") {
			return nil, fmt.Errorf("%q cannot be moved into itself", n)
		}
		if _, err := rem.Stat(ctx, p); errors.Is(err, remote.ErrNotFound) {
			return nil, fmt.Errorf("%q is no longer there", n)
		} else if err != nil {
			return nil, err
		}
		if _, err := rem.Stat(ctx, path.Join(trel, n)); err == nil {
			return nil, fmt.Errorf("%q is already in the folder to move into; nothing was moved", n)
		} else if !errors.Is(err, remote.ErrNotFound) {
			return nil, err
		}
		place := remote.Place(fid, p)
		if v, ok := s.Jobs.(viewer); ok {
			for _, j := range v.Views() {
				if j.State == "finished" || j.State == "stopped" {
					continue
				}
				for _, jp := range []string{j.Source, j.Dest} {
					if jp != "" && (jp == place || strings.HasPrefix(jp, place+"/") || strings.HasPrefix(place, jp+"/")) {
						return nil, fmt.Errorf("a job in the queue still uses %q: stop it or let it finish first", n)
					}
				}
			}
		}
		froms = append(froms, p)
	}
	moved := 0
	for _, p := range froms {
		dst := path.Join(trel, path.Base(p))
		if err := rem.Rename(ctx, p, dst); err != nil {
			if errors.Is(err, remote.ErrExists) {
				err = errors.New("something of that name appeared in the target")
			}
			if s.Log != nil {
				s.Log.Warn("move on a server: not moved", "server", pub.Name, "path", p, "to", dst, "err", err)
			}
			return map[string]any{"moved": moved}, fmt.Errorf("%d moved; %q was not: %v. Nothing else was touched.", moved, path.Base(p), err)
		}
		moved++
		if s.Log != nil {
			s.Log.Info("move on a server: moved", "server", pub.Name, "path", p, "to", dst)
		}
	}
	return map[string]any{"moved": moved}, nil
}
