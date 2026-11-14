// Package server serves the page and its small API on this computer only.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/events"
	"github.com/YC-BB-20/shuttle-server/internal/update"
)

// Server holds what the handlers need.
type Server struct {
	Page    []byte // built-in page
	PageDir string // if set, index.html is read from here on every request
	Hub     *events.Hub
	Status  func() any
	About   func() any
	Jobs    Actions // nil in safe mode
	Listen  string
	Updates Updates // works in safe mode too
	Restart func()
	Quit    func()
}

// Updates are the update commands.
type Updates interface {
	SetKey(key string) error
	Check(ctx context.Context) (*update.Release, error)
	Install(ctx context.Context, now bool) error
	Rollback() error
	History(ctx context.Context) ([]update.Choice, string)
	InstallCommit(ctx context.Context, commit string) error
}

// Actions are the commands the page may send about copy jobs.
type Actions interface {
	Add(copyjob.Spec) (*copyjob.Job, error)
	Pause(id string) error
	Resume(id string) error
	Stop(id string) error
	ContinueAnyway(id string) error
	SetHidden(id string, hidden bool) error
	Problems(id string, limit int) []copyjob.Problem
	Files(id, filter string, from, limit int) (copyjob.FilePage, error)
	History(skip, limit int) []copyjob.View
	FileInfo(id string, i int) (copyjob.FileInfo, error)
	AskRecheck(id string, i int, which string) error
	Settings() copyjob.Settings
	SetSettings(copyjob.Settings) (copyjob.Settings, error)
}

// HTTP returns an http.Server with a timeout on every request.
func (s *Server) HTTP() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/status", s.json(s.Status))
	mux.HandleFunc("GET /api/about", s.json(s.About))
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/browse", s.browse)
	mux.HandleFunc("GET /api/storage", s.storageList)
	mux.HandleFunc("GET /api/jobs/{id}/problems", s.problems)
	mux.HandleFunc("GET /api/jobs/{id}/files", s.files)
	mux.HandleFunc("GET /api/jobs/{id}/files/{i}", s.fileInfo)
	mux.HandleFunc("POST /api/jobs/{id}/files/{i}/recheck", s.command(s.recheck))
	mux.HandleFunc("GET /api/history", s.jobHistory)
	mux.HandleFunc("POST /api/jobs", s.command(s.addJob))
	mux.HandleFunc("GET /api/settings/copy", s.copySettings)
	mux.HandleFunc("POST /api/settings/copy", s.command(s.saveCopySettings))
	mux.HandleFunc("POST /api/restart", s.always(s.restart))
	mux.HandleFunc("POST /api/quit", s.always(s.quit))
	mux.HandleFunc("GET /api/update/history", s.history)
	mux.HandleFunc("POST /api/update/{action}", s.always(s.updateAction))
	mux.HandleFunc("POST /api/jobs/{id}/{action}", s.command(s.jobAction))
	return &http.Server{
		Handler:           localOnly(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

// localOnly refuses requests whose Host is not this computer, so a web page
// elsewhere cannot reach the API through a renamed address.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "Shuttle Server answers only on localhost", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	page := s.Page
	if s.PageDir != "" {
		if b, err := os.ReadFile(filepath.Join(s.PageDir, "index.html")); err == nil {
			page = b
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
	w.Write(page)
}

func (s *Server) json(f func() any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(f())
	}
}

// events streams status with server-sent events. Each write has its own
// deadline, so a page that stops reading cannot hold a write forever.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	c, err := s.Hub.Subscribe()
	if err != nil {
		http.Error(w, "Too many Shuttle pages are open. Close one and reload.", http.StatusServiceUnavailable)
		return
	}
	defer s.Hub.Unsubscribe(c)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(format string, a ...any) bool {
		rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(w, format, a...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write("retry: 3000\n\n") {
		return
	}
	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.Done:
			return
		case <-keep.C:
			if !write(": keep-alive\n\n") {
				return
			}
		case msg := <-c.C:
			if !write("data: %s\n\n", msg) {
				return
			}
		}
	}
}
