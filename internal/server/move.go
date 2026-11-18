package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// moveRename is os.Rename; tests replace it.
var moveRename = os.Rename

// moveItems moves ticked items into another folder on the same drive or
// share (POST /api/move, v0.2.114): a rename only, so a folder of any size
// moves at once and nothing is read or written. It never copies: on another
// drive it refuses. It never replaces: a name already in the target is
// refused. Everything is checked before anything moves; if one item may not
// move, none does. Nothing a job in the queue still uses, no whole disk, the
// home folder or Shuttle's own folders.
func (s *Server) moveItems(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Folder string   `json:"folder"`
		Names  []string `json:"names"`
		To     string   `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, errors.New("could not read the request")
	}
	folder, to := filepath.Clean(req.Folder), filepath.Clean(req.To)
	if !filepath.IsAbs(req.Folder) || !filepath.IsAbs(req.To) || strings.Contains(req.Folder, "/../") || strings.Contains(req.To, "/../") ||
		strings.HasSuffix(req.Folder, "/..") || strings.HasSuffix(req.To, "/..") {
		return nil, errors.New("these folders cannot be used")
	}
	if len(req.Names) == 0 || len(req.Names) > 2000 {
		return nil, errors.New("choose between 1 and 2000 items to move")
	}
	if folder == to {
		return nil, errors.New("they are already in that folder")
	}
	if !pickable(folder) || !pickable(to) {
		return nil, errors.New("nothing can be moved here")
	}
	if !isDir(to) {
		return nil, errors.New("the folder to move into is no longer there")
	}
	vf, ok1 := deleteVolume(folder)
	vt, ok2 := deleteVolume(to)
	if !ok1 || !ok2 {
		return nil, errors.New("could not tell which drive this is")
	}
	if vf.ID != vt.ID {
		return nil, errors.New("Move works only within one drive or share, where it is instant. To another drive, use Copy.")
	}
	if vf.ReadOnly {
		return nil, errors.New("this drive can only be read")
	}
	if vf.Network && vf.Share == "" {
		return nil, errors.New("Shuttle moves only on disks, SMB shares and LucidLink")
	}
	home, _ := os.UserHomeDir()
	data := ""
	if s.LogsDir != "" {
		data = filepath.Dir(s.LogsDir)
	}
	exe, _ := os.Executable()
	roots := map[string]bool{"/": true, home: true}
	for _, x := range deletePlaces() {
		roots[x.Path] = true
	}
	seen := map[string]bool{}
	var froms []string
	for _, n := range req.Names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") || seen[n] {
			return nil, fmt.Errorf("%q cannot be moved", n)
		}
		seen[n] = true
		p := filepath.Join(folder, n)
		if _, err := os.Lstat(p); err != nil {
			return nil, fmt.Errorf("%q is no longer there", n)
		}
		switch {
		case roots[p]:
			return nil, errors.New("a whole disk or the home folder cannot be moved")
		case under(to, p):
			return nil, fmt.Errorf("%q cannot be moved into itself", n)
		case data != "" && (under(data, p) || under(p, data)):
			return nil, errors.New("Shuttle's own data folder cannot be moved from here")
		case exe != "" && under(exe, p):
			return nil, errors.New("the folder holding Shuttle itself cannot be moved")
		}
		if _, err := os.Lstat(filepath.Join(to, n)); err == nil {
			return nil, fmt.Errorf("%q is already in the folder to move into; nothing was moved", n)
		}
		if v, ok := s.Jobs.(viewer); ok {
			for _, j := range v.Views() {
				if j.State == "finished" || j.State == "stopped" {
					continue
				}
				for _, jp := range []string{j.Source, j.Dest} {
					if jp != "" && (under(jp, p) || under(p, jp) || under(jp, filepath.Join(to, n))) {
						return nil, fmt.Errorf("a job in the queue still uses %q: stop it or let it finish first", n)
					}
				}
			}
		}
		froms = append(froms, p)
	}
	moved := 0
	for _, p := range froms {
		dst := filepath.Join(to, filepath.Base(p))
		if _, err := os.Lstat(dst); err == nil { // appeared since the check: never replaced
			return map[string]any{"moved": moved}, fmt.Errorf("%d moved; %q was not, as something of that name appeared in the target", moved, filepath.Base(p))
		}
		if err := moveRename(p, dst); err != nil {
			if errors.Is(err, syscall.EXDEV) {
				err = errors.New("it is on another drive")
			}
			if s.Log != nil {
				s.Log.Warn("move: not moved", "path", p, "to", dst, "err", err)
			}
			return map[string]any{"moved": moved}, fmt.Errorf("%d moved; %q was not: %v. Nothing else was touched.", moved, filepath.Base(p), err)
		}
		moved++
		if s.Log != nil {
			s.Log.Info("move: moved", "path", p, "to", dst)
		}
	}
	return map[string]any{"moved": moved}, nil
}
