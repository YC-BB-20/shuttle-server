package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/storage"
)

// viewer is what eject needs from the jobs: which ones are still going.
type viewer interface{ Views() []copyjob.View }

// ejectFn and ejectPlaces are storage.Eject and places; tests replace them.
var (
	ejectFn     = storage.Eject
	ejectPlaces = places
)

// eject asks the system to eject a connected disk or unmount a share
// (POST /api/disks/eject, v0.2.100). Only a disk in the list of places, never
// this computer's own disk, and never one a job not yet finished still uses.
func (s *Server) eject(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		return nil, errors.New("choose a disk to eject")
	}
	want := filepath.Clean(req.Path)
	var place *Place
	for _, p := range ejectPlaces() {
		if p.Path == want {
			p := p
			place = &p
			break
		}
	}
	if place == nil || place.Kind == "internal" || place.Kind == "home" || want == "/" {
		return nil, errors.New("only a connected disk or share can be ejected")
	}
	if v, ok := s.Jobs.(viewer); ok {
		for _, j := range v.Views() {
			if j.State == "finished" || j.State == "stopped" {
				continue
			}
			if under(j.Source, want) || under(j.Dest, want) {
				return nil, errors.New("a job in the queue still uses this disk: stop it or let it finish first")
			}
		}
	}
	if err := ejectFn(r.Context(), want, place.Kind == "share" || place.Kind == "server"); err != nil {
		return nil, errors.New("Not ejected: " + err.Error())
	}
	return map[string]any{"ok": true}, nil
}

func under(p, dir string) bool {
	p = filepath.Clean(p)
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}
