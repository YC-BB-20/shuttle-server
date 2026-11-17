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
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&b); err != nil {
		return nil, errors.New("could not read the request")
	}
	raws := copyjob.SplitLinks(b.URLs)
	if len(raws) == 0 {
		return nil, errors.New("paste one or more download links")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	longAnswer(w)
	return map[string]any{"links": l.LookUpLinks(ctx, raws, b.Password)}, nil
}

// linksAdd queues the links as one download job (POST /api/links/add
// {"urls": [...], "dest": "...", "next": true}): one file, or several that
// keep their folders (v0.2.92). Each link is checked again; one that fails
// does not stop the others and is reported.
func (s *Server) linksAdd(w http.ResponseWriter, r *http.Request) (any, error) {
	l, ok := s.Jobs.(linker)
	if !ok {
		return nil, errors.New("link downloads are not available right now")
	}
	var b struct {
		URLs []string `json:"urls"`
		Dest string   `json:"dest"`
		Next bool     `json:"next"`
		// Before: when Next, the job the download goes in front of.
		Before string `json:"before"`
		// A Vimeo video's password, and the chosen quality (one link only).
		Password string `json:"password"`
		Quality  string `json:"quality"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&b); err != nil {
		return nil, errors.New("could not read the request")
	}
	if len(b.URLs) == 0 {
		return nil, errors.New("paste one or more download links")
	}
	if len(b.URLs) > 1 {
		b.Quality = "" // each link gets its best
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	longAnswer(w)
	j, errs, err := l.AddLinks(ctx, b.URLs, b.Dest, copyjob.LinkOpts{Password: b.Password, Quality: b.Quality})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	if j != nil {
		ids = append(ids, j.ID())
		if b.Next && b.Before != "" {
			l.Move(j.ID(), b.Before) // in front of the next job
		}
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"ok": len(errs) == 0, "ids": ids, "errors": errs}, nil
}

// longAnswer lets a request that looks links up answer after the server's
// usual 15 seconds: a few hundred links take a while (the look-ups have
// their own time limit).
func longAnswer(w http.ResponseWriter) {
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Minute))
}
