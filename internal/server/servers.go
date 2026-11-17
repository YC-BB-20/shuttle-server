package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// Servers: the file servers Shuttle can copy to and from (DESIGN.md
// section 19). The page never receives a password or secret key.

func (s *Server) serverList(w http.ResponseWriter, r *http.Request) {
	if s.Servers == nil {
		writeJSON(w, http.StatusOK, []remote.Public{})
		return
	}
	list, err := s.Servers.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the saved servers could not be read"})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) serverSave(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Servers == nil {
		return nil, errors.New("servers are not available")
	}
	var c remote.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		return nil, errors.New("could not read the request")
	}
	return s.Servers.Save(c)
}

func (s *Server) serverAction(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Servers == nil {
		return nil, errors.New("servers are not available")
	}
	id := r.PathValue("id")
	switch r.PathValue("action") {
	case "test":
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		return s.Servers.Check(ctx, id)
	case "remove":
		return map[string]bool{"ok": true}, s.Servers.Remove(id)
	}
	return nil, errors.New("unknown action")
}
