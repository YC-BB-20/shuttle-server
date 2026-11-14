package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/speedtest"
	"github.com/YC-BB-20/shuttle-server/internal/storage"
)

// Disk is one card on the Storage page: a place from the picker plus what
// the system says about how it is connected.
type Disk struct {
	Place
	storage.Info
}

// storageList lists the computer's own disk and every connected disk, with
// connection, speed and kind (v0.2.32). It only reads. The whole answer is
// bounded in time; a slow hardware report is finished in the background and
// Pending tells the page to ask again.
func (s *Server) storageList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 11*time.Second)
	defer cancel()
	out := []Disk{}
	for _, p := range places() {
		d := Disk{Place: p}
		if p.Note == "" && ctx.Err() == nil {
			at := p.Path
			if p.Kind == "internal" {
				at = "/"
			}
			d.Info = storage.Details(ctx, at)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": out, "pending": storage.Pending()})
}

// speedStatus is the running or last speed test, the kept results of every
// disk, and the sizes offered (v0.2.33).
func (s *Server) speedStatus(w http.ResponseWriter, r *http.Request) {
	if s.Speed == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": speedtest.Status{}, "results": []speedtest.Result{}, "sizes": speedtest.Sizes})
		return
	}
	res := s.Speed.Results()
	if res == nil {
		res = []speedtest.Result{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Speed.Status(), "results": res, "sizes": speedtest.Sizes})
}

func (s *Server) speedAction(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Speed == nil {
		return nil, errors.New("the speed test is not available in safe mode")
	}
	switch r.PathValue("action") {
	case "start":
		var req struct {
			Path     string `json:"path"`
			SizeGB   int    `json:"size_gb"`
			ReadOnly bool   `json:"read_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, errors.New("could not read the request")
		}
		if err := s.Speed.Start(req.Path, req.SizeGB, req.ReadOnly); err != nil {
			return nil, err
		}
	case "stop":
		s.Speed.Stop()
	default:
		return nil, errors.New("unknown action")
	}
	return map[string]bool{"ok": true}, nil
}
