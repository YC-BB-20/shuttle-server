package server

import (
	"github.com/YC-BB-20/shuttle-server/internal/remote"

	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/trash"
)

// The Delete function (v0.2.106; COMPATIBILITY.md rule 2, DESIGN.md
// section 14). Off until the owner turns it on in Settings; SMB shares and
// the LucidLink mount need a second switch, since there nothing can go to a
// Trash and an item is deleted for good. Everything asked for is checked
// before anything is touched: if one item may not be deleted, none is. The
// page asks its one or two questions first; this is the last fence.

// deleteState is the deletion running for good on a share, if any. Only one
// runs at a time; moving to the Trash is a rename and is done at once.
type deleteState struct {
	mu     sync.Mutex
	Busy   bool   `json:"busy"`
	Total  int    `json:"total"`
	Done   int    `json:"done"`
	Failed int    `json:"failed"`
	Error  string `json:"error,omitempty"`
	Folder string `json:"folder,omitempty"`
}

var deleting deleteState

// deletePlaces, deleteVolume and deleteLog are places, the volume lookup
// and the log; tests replace them.
var (
	deletePlaces = places
	deleteVolume = func(p string) (diskio.Volume, bool) {
		v, ok, hung := volume(p, 2*time.Second)
		return v, ok && !hung
	}
)

type deleteReq struct {
	Folder string   `json:"folder"`
	Names  []string `json:"names"`
}

func (s *Server) deleteStatus(w http.ResponseWriter, r *http.Request) {
	deleting.mu.Lock()
	defer deleting.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"busy": deleting.Busy, "total": deleting.Total, "done": deleting.Done,
		"failed": deleting.Failed, "error": deleting.Error, "folder": deleting.Folder})
}

func (s *Server) deleteItems(w http.ResponseWriter, r *http.Request) (any, error) {
	set := s.Jobs.Settings()
	if !set.DeleteOn {
		return nil, errors.New("Delete is switched off. Turn it on in Settings first.")
	}
	var req deleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, errors.New("could not read the request")
	}
	if remote.IsPlace(req.Folder) {
		return nil, errors.New("Delete on a server is not built yet; nothing was deleted")
	}
	folder := filepath.Clean(req.Folder)
	if !filepath.IsAbs(req.Folder) || folder != strings.TrimSuffix(req.Folder, "/") && folder != req.Folder {
		return nil, errors.New("this folder cannot be used")
	}
	if len(req.Names) == 0 || len(req.Names) > 2000 {
		return nil, errors.New("choose between 1 and 2000 items to delete")
	}
	// Only in the places the Files view shows, and never a disk or the home folder itself.
	home, _ := os.UserHomeDir()
	if !pickable(folder) {
		return nil, errors.New("nothing can be deleted here")
	}
	var place *Place
	for _, x := range deletePlaces() {
		if x.Kind == "disk" || x.Kind == "share" {
			if under(folder, x.Path) && (place == nil || len(x.Path) > len(place.Path)) {
				x := x
				place = &x
			}
		}
	}
	if place != nil && place.Note != "" {
		return nil, errors.New("this disk is not responding")
	}
	v, ok := deleteVolume(folder)
	if !ok {
		return nil, errors.New("could not tell which disk this is")
	}
	if v.ReadOnly {
		return nil, errors.New("this disk can only be read")
	}
	forGood := v.Share != ""
	if v.Network && !forGood {
		return nil, errors.New("Shuttle deletes only on disks, SMB shares and LucidLink")
	}
	if forGood && !set.DeleteShares {
		return nil, errors.New("Deleting on LucidLink and SMB shares is switched off. It deletes for good, with no Trash; turn it on in Settings if you mean it.")
	}
	data := ""
	if s.LogsDir != "" {
		data = filepath.Dir(s.LogsDir)
	}
	exe, _ := os.Executable()
	seen := map[string]bool{}
	var paths []string
	for _, n := range req.Names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") || seen[n] {
			return nil, fmt.Errorf("%q cannot be deleted", n)
		}
		seen[n] = true
		p := filepath.Join(folder, n)
		if _, err := os.Lstat(p); err != nil {
			return nil, fmt.Errorf("%q is no longer there", n)
		}
		switch {
		case p == home || p == "/" || place != nil && p == place.Path:
			return nil, errors.New("a whole disk or the home folder cannot be deleted")
		case data != "" && (under(data, p) || under(p, data)):
			return nil, errors.New("Shuttle's own data folder cannot be deleted from here")
		case exe != "" && under(exe, p):
			return nil, errors.New("the folder holding Shuttle itself cannot be deleted")
		}
		if v, ok := s.Jobs.(viewer); ok {
			for _, j := range v.Views() {
				if j.State == "finished" || j.State == "stopped" {
					continue
				}
				for _, jp := range []string{j.Source, j.Dest} {
					if jp != "" && (under(jp, p) || under(p, jp)) {
						return nil, fmt.Errorf("a job in the queue still uses %q: stop it or let it finish first", n)
					}
				}
			}
		}
		paths = append(paths, p)
	}
	log := s.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if !forGood {
		moved := 0
		for _, p := range paths {
			to, err := trash.MoveToTrash(p, v.Mount)
			if err != nil {
				log.Warn("delete: not moved to the Trash", "path", p, "err", err)
				return map[string]any{"trashed": moved}, fmt.Errorf("%d moved to the Trash; %q was not: %v. Nothing else was touched.", moved, filepath.Base(p), err)
			}
			moved++
			log.Info("delete: moved to the Trash", "path", p, "to", to)
		}
		return map[string]any{"trashed": moved}, nil
	}
	deleting.mu.Lock()
	if deleting.Busy {
		deleting.mu.Unlock()
		return nil, errors.New("a delete is still running; wait for it to finish")
	}
	deleting.Busy, deleting.Total, deleting.Done, deleting.Failed, deleting.Error, deleting.Folder = true, len(paths), 0, 0, "", folder
	deleting.mu.Unlock()
	log.Info("delete: deleting for good on a share", "folder", folder, "items", len(paths), "share", v.Share)
	go func() {
		for _, p := range paths {
			err := trash.RemoveForGood(p)
			deleting.mu.Lock()
			if err != nil {
				deleting.Failed++
				if deleting.Error == "" {
					deleting.Error = fmt.Sprintf("%q: %v", filepath.Base(p), err)
				}
				log.Warn("delete: could not delete", "path", p, "err", err)
			} else {
				deleting.Done++
				log.Info("delete: deleted for good", "path", p)
			}
			deleting.mu.Unlock()
		}
		deleting.mu.Lock()
		deleting.Busy = false
		deleting.mu.Unlock()
	}()
	return map[string]any{"deleting": len(paths)}, nil
}
