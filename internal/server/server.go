// Package server serves the page and its small API on this computer only.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/YC-BB-20/shuttle-server/internal/speedtest"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/events"
	"github.com/YC-BB-20/shuttle-server/internal/remote"
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
	Login   Login             // nil where starting at login is not supported
	Speed   *speedtest.Tester // nil in safe mode
	LogsDir string            // logs/YYYY-MM/shuttle.log, read for a copy's Log tab (v0.2.87)
	Servers *remote.Store     // file servers such as CTERA (v0.2.96); logins stay in secrets/servers
	Log     *slog.Logger      // the Delete function's record (v0.2.106); nil: not logged
	sizes   sizer             // the copy size being counted for the picker
}

// Login is starting Shuttle when the owner logs in.
type Login interface {
	Enabled() bool
	Set(on bool) error
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

// drafter is an Actions that can adopt the list made while choosing (v0.2.72).
type drafter interface {
	DraftDir() string
	AddDraft(copyjob.Spec, *copyjob.Draft) (*copyjob.Job, error)
}

// sourceChecker is an Actions that can look at a finished copy's source again
// and copy the files found added to it (v0.2.82).
type sourceChecker interface {
	CheckSourceAgain(id string) error
	CopyAdded(id string) (string, error)
	AddedList(id string, from, limit int) ([]copyjob.AddedFile, int, error)
}

// copiesFinder is an Actions that can say, for a folder, which copies hold
// its files (the file browser, v0.2.89).
type copiesFinder interface {
	CopiesOf(dir string) copyjob.Copies
}

// linker is an Actions that can download links (v0.2.90).
type linker interface {
	LookUpLinks(ctx context.Context, raws []string, password string) []copyjob.LinkInfo
	AddLinks(ctx context.Context, raws []string, dest string, o copyjob.LinkOpts) (*copyjob.Job, []string, error)
	Move(id, before string) error
}

// exporter is an Actions that can write a copy's hash list (v0.2.80).
type exporter interface {
	Export(w io.Writer, id, format, algo string, paths []string) (int, int, error)
}

// spaceChecker is an Actions that can say whether a copy of a given size fits
// on a destination (v0.2.79).
type spaceChecker interface {
	CheckSpace(dst string, need int64) (*copyjob.SpaceShort, error)
}

// retrier is an Actions that can put a finished copy's failed files back in
// the queue (v0.2.92).
type retrier interface {
	RetryFailed(id string, file int) (int, error)
}

// failKeeper answers "leave them failed" (v0.2.99).
type failKeeper interface {
	KeepFailed(id string) error
}

// Actions are the commands the page may send about copy jobs.
type Actions interface {
	Add(copyjob.Spec) (*copyjob.Job, error)
	Pause(id string) error
	Resume(id string) error
	Stop(id string) error
	ContinueAnyway(id string) error
	SetHidden(id string, hidden bool) error
	Move(id, before string) error
	Problems(id string, limit int) []copyjob.Problem
	Files(id, filter string, from, limit int) (copyjob.FilePage, error)
	FilesIn(id, filter string, tree bool, dir string, from, limit int) (copyjob.FilePage, error)
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
	mux.HandleFunc("GET /api/browse/sizes", s.folderSizes)
	mux.HandleFunc("GET /api/copies", s.copies)
	mux.HandleFunc("POST /api/links/info", s.command(s.linksInfo))
	mux.HandleFunc("POST /api/links/add", s.command(s.linksAdd))
	mux.HandleFunc("POST /api/mkdir", s.always(s.mkdir))
	mux.HandleFunc("POST /api/delete", s.command(s.deleteItems))
	mux.HandleFunc("GET /api/delete", s.deleteStatus)
	mux.HandleFunc("POST /api/disks/eject", s.command(s.eject))
	mux.HandleFunc("GET /api/storage", s.storageList)
	mux.HandleFunc("GET /api/size", s.copySize)
	mux.HandleFunc("GET /api/spacecheck", s.spaceCheck)
	mux.HandleFunc("GET /api/speedtest", s.speedStatus)
	mux.HandleFunc("POST /api/speedtest/{action}", s.always(s.speedAction))
	mux.HandleFunc("GET /api/jobs/{id}/problems", s.problems)
	mux.HandleFunc("GET /api/jobs/{id}/log", s.jobLog)
	mux.HandleFunc("GET /api/log", s.jobLog)
	mux.HandleFunc("GET /api/jobs/{id}/added", s.added)
	mux.HandleFunc("GET /api/jobs/{id}/export", s.export)
	mux.HandleFunc("POST /api/jobs/{id}/check-source", s.command(s.checkSource))
	mux.HandleFunc("POST /api/jobs/{id}/copy-added", s.command(s.copyAdded))
	mux.HandleFunc("GET /api/jobs/{id}/files", s.files)
	mux.HandleFunc("GET /api/jobs/{id}/files/{i}", s.fileInfo)
	mux.HandleFunc("POST /api/jobs/{id}/files/{i}/recheck", s.command(s.recheck))
	mux.HandleFunc("GET /api/history", s.jobHistory)
	mux.HandleFunc("POST /api/jobs", s.command(s.addJob))
	mux.HandleFunc("GET /api/settings/copy", s.copySettings)
	mux.HandleFunc("POST /api/settings/copy", s.command(s.saveCopySettings))
	mux.HandleFunc("GET /api/settings/login", s.json(s.loginState))
	mux.HandleFunc("POST /api/settings/login", s.always(s.saveLogin))
	mux.HandleFunc("POST /api/restart", s.always(s.restart))
	mux.HandleFunc("POST /api/quit", s.always(s.quit))
	mux.HandleFunc("GET /api/update/history", s.history)
	mux.HandleFunc("POST /api/update/{action}", s.always(s.updateAction))
	mux.HandleFunc("POST /api/jobs/bulk", s.command(s.bulkAction))
	mux.HandleFunc("GET /api/servers", s.serverList)
	mux.HandleFunc("POST /api/servers", s.always(s.serverSave))
	mux.HandleFunc("POST /api/servers/{id}/{action}", s.always(s.serverAction))
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
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
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
