package server

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/location"
)

// Copy size (v0.2.48): while a source is being chosen, the page asks how
// much a copy of it would be. The files are counted by the same walk and
// the same rules (system files left out per Settings) as the copy's own
// scan, in the background, one count at a time. Asking about something
// else cancels the count under way; a count nobody has asked about for
// sizeIdle stops by itself. Only names and sizes are read.

const (
	sizeIdle  = 20 * time.Second
	sizeLimit = 15 * time.Minute
)

// SizeCount is the answer: what has been counted so far, and whether the
// count is complete.
type SizeCount struct {
	Path    string   `json:"path"`
	Only    []string `json:"only,omitempty"`
	Files   int64    `json:"files"`
	Bytes   int64    `json:"bytes"`
	Skipped int64    `json:"skipped,omitempty"` // links, unreadable folders and the like, not copied
	Done    bool     `json:"done"`
	Error   string   `json:"error,omitempty"`
}

type sizer struct {
	mu      sync.Mutex
	cur     SizeCount
	cancel  context.CancelFunc
	lastAsk time.Time
	running bool
}

func (z *sizer) ask(path string, only []string, f location.Folder) SizeCount {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.lastAsk = time.Now()
	if z.cur.Path == path && slices.Equal(z.cur.Only, only) && (z.running || z.cur.Done) {
		return z.cur
	}
	if z.cancel != nil {
		z.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), sizeLimit)
	z.cancel, z.running = cancel, true
	z.cur = SizeCount{Path: path, Only: only}
	go z.count(ctx, cancel, f, path, only)
	return z.cur
}

func (z *sizer) count(ctx context.Context, cancel context.CancelFunc, f location.Folder, path string, only []string) {
	defer cancel()
	var files, bytes, skipped int64
	mine := func() bool { return z.cur.Path == path && slices.Equal(z.cur.Only, only) }
	report := func(done bool, msg string) bool {
		z.mu.Lock()
		defer z.mu.Unlock()
		if !mine() {
			return false
		}
		z.cur.Files, z.cur.Bytes, z.cur.Skipped, z.cur.Done, z.cur.Error = files, bytes, skipped, done, msg
		if done || msg != "" {
			z.running = false
		}
		return time.Since(z.lastAsk) < sizeIdle
	}
	err := f.Walk(ctx, func(e location.Entry) error {
		files++
		bytes += e.Size
		if files%2000 == 0 && !report(false, "") {
			cancel() // nobody is looking any more, or something else was asked
		}
		return nil
	}, func(s location.Skipped) {
		if s.Why != location.WhySystem {
			skipped++
		}
	})
	switch {
	case err == nil:
		report(true, "")
	case ctx.Err() == context.DeadlineExceeded:
		report(false, "Counting took too long; the copy will count the files itself when it starts.")
	case ctx.Err() != nil:
		z.mu.Lock()
		if mine() {
			z.running, z.cur = false, SizeCount{} // stopped; asked again, it starts over
		}
		z.mu.Unlock()
	default:
		report(false, "The files could not be counted: "+err.Error())
	}
}

// copySize answers GET /api/size?path=…&only=a&only=b.
func (s *Server) copySize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := filepath.Clean(q.Get("path"))
	only := q["only"]
	if len(only) > 10000 {
		only = only[:10000]
	}
	if !filepath.IsAbs(p) {
		writeJSON(w, http.StatusOK, SizeCount{Path: p, Error: "Choose a folder first."})
		return
	}
	v, ok, hung := volume(p, 2*time.Second)
	switch {
	case hung:
		writeJSON(w, http.StatusOK, SizeCount{Path: p, Error: "This disk is not responding, so its files cannot be counted."})
		return
	case !ok:
		writeJSON(w, http.StatusOK, SizeCount{Path: p, Error: "This folder cannot be found."})
		return
	case v.Network:
		writeJSON(w, http.StatusOK, SizeCount{Path: p, Error: "Files on a network server are not counted."})
		return
	}
	set := copyjob.DefaultSettings
	if s.Jobs != nil {
		set = s.Jobs.Settings()
	}
	f := location.Folder{Dir: p, Only: only, System: set.SkipSystem, AppleDouble: set.SkipAppleDouble}
	writeJSON(w, http.StatusOK, s.sizes.ask(p, only, f))
}
