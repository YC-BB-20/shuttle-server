package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
)

// linksInfo looks up pasted links for the Add link pop-up (POST
// /api/links/info {"urls": "…", "password": "…"}). The answer never holds a
// link. The password (a Vimeo video's) is used and never sent back or logged.
func (s *Server) linksInfo(w http.ResponseWriter, r *http.Request) (any, error) {
	l, ok := s.Jobs.(linker)
	if !ok {
		return nil, errors.New("link downloads are not available right now")
	}
	var b struct {
		URLs     string `json:"urls"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 512<<10)).Decode(&b); err != nil {
		return nil, errors.New("could not read the request")
	}
	raws := copyjob.SplitLinks(b.URLs)
	if len(raws) == 0 {
		return nil, errors.New("paste one or more download links")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	return map[string]any{"links": l.LookUpLinks(ctx, raws, b.Password)}, nil
}

// linksAdd queues a download for each link (POST /api/links/add {"urls":
// [...], "dest": "...", "next": true}). Each link is checked again; one that
// fails does not stop the others.
func (s *Server) linksAdd(w http.ResponseWriter, r *http.Request) (any, error) {
	l, ok := s.Jobs.(linker)
	if !ok {
		return nil, errors.New("link downloads are not available right now")
	}
	var b struct {
		URLs []string `json:"urls"`
		Dest string   `json:"dest"`
		Next bool     `json:"next"`
		// Before: when Next, the job the downloads go in front of.
		Before string `json:"before"`
		// A Vimeo video's password, and the chosen quality (one link only).
		Password string `json:"password"`
		Quality  string `json:"quality"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 512<<10)).Decode(&b); err != nil {
		return nil, errors.New("could not read the request")
	}
	if len(b.URLs) == 0 {
		return nil, errors.New("paste one or more download links")
	}
	if len(b.URLs) > 50 {
		b.URLs = b.URLs[:50]
	}
	if len(b.URLs) > 1 {
		b.Quality = "" // each link gets its best
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ids, errs := []string{}, []string{}
	for _, u := range b.URLs {
		j, err := l.AddLink(ctx, u, b.Dest, copyjob.LinkOpts{Password: b.Password, Quality: b.Quality})
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		ids = append(ids, j.ID())
		if b.Next && b.Before != "" {
			l.Move(j.ID(), b.Before) // in front of the next job, keeping their order
		}
	}
	return map[string]any{"ok": len(errs) == 0, "ids": ids, "errors": errs}, nil
}
