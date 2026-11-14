package server

import (
	"context"
	"net/http"
	"time"

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
