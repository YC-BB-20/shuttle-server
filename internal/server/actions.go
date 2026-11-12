package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
)

// command protects a request that changes something. Besides the localhost
// Host check, it needs the X-Shuttle header, which a page on another site
// cannot send without a preflight this server never approves, and an Origin
// (when present) on this computer.
func (s *Server) command(h func(http.ResponseWriter, *http.Request) (any, error)) http.HandlerFunc {
	return s.guarded(h, true)
}

// always is command for actions that also work in safe mode.
func (s *Server) always(h func(http.ResponseWriter, *http.Request) (any, error)) http.HandlerFunc {
	return s.guarded(h, false)
}

func (s *Server) guarded(h func(http.ResponseWriter, *http.Request) (any, error), needJobs bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Shuttle") != "1" || !localOrigin(r.Header.Get("Origin")) {
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		if needJobs && s.Jobs == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Shuttle Server is in safe mode; nothing runs."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		out, err := h(w, r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func localOrigin(o string) bool {
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) addJob(w http.ResponseWriter, r *http.Request) (any, error) {
	var spec copyjob.Spec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		return nil, errors.New("could not read the request")
	}
	if spec.Source == "" || spec.Dest == "" {
		return nil, errors.New("choose a source folder and a destination folder")
	}
	if _, err := s.Jobs.Add(spec); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "pause":
		err = s.Jobs.Pause(id)
	case "resume":
		err = s.Jobs.Resume(id)
	case "stop":
		err = s.Jobs.Stop(id)
	default:
		err = errors.New("unknown action")
	}
	return map[string]bool{"ok": err == nil}, err
}

func (s *Server) problems(w http.ResponseWriter, r *http.Request) {
	if s.Jobs == nil {
		writeJSON(w, http.StatusOK, []copyjob.Problem{})
		return
	}
	p := s.Jobs.Problems(r.PathValue("id"), 200)
	if p == nil {
		p = []copyjob.Problem{}
	}
	writeJSON(w, http.StatusOK, p)
}

// Folder is one row in the folder picker.
type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// browse lists the folders inside a folder, for the folder picker. It shows
// names only and lists at most 1000.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	places := []Folder{{"Home", home}}
	if vols, err := os.ReadDir("/Volumes"); err == nil {
		for _, v := range vols {
			places = append(places, Folder{v.Name(), filepath.Join("/Volumes", v.Name())})
		}
	} else if vols, err := os.ReadDir("/media"); err == nil {
		for _, v := range vols {
			places = append(places, Folder{v.Name(), filepath.Join("/media", v.Name())})
		}
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		p = home
	}
	p = filepath.Clean(p)
	out := map[string]any{"path": p, "parent": filepath.Dir(p), "places": places, "folders": []Folder{}}
	ents, err := os.ReadDir(p)
	if err != nil {
		out["error"] = "This folder cannot be opened."
		writeJSON(w, http.StatusOK, out)
		return
	}
	var dirs []Folder
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() || (e.Type()&os.ModeSymlink != 0 && isDir(filepath.Join(p, e.Name()))) {
			dirs = append(dirs, Folder{e.Name(), filepath.Join(p, e.Name())})
		}
		if len(dirs) >= 1000 {
			out["truncated"] = true
			break
		}
	}
	sort.Slice(dirs, func(a, b int) bool { return strings.ToLower(dirs[a].Name) < strings.ToLower(dirs[b].Name) })
	if dirs != nil {
		out["folders"] = dirs
	}
	writeJSON(w, http.StatusOK, out)
}

func isDir(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

func (s *Server) restart(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Restart == nil {
		return nil, errors.New("restart is not available")
	}
	go s.Restart() // after this reply is sent
	return map[string]bool{"ok": true}, nil
}

// updateAction: key, check, install, rollback. Long steps run in the
// background; the page and the icon follow them through the status.
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Updates == nil {
		return nil, errors.New("updates are not available")
	}
	var body struct {
		Key string `json:"key"`
		Now bool   `json:"now"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	bg := func(f func(context.Context)) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			f(ctx)
		}()
	}
	switch r.PathValue("action") {
	case "key":
		if err := s.Updates.SetKey(body.Key); err != nil {
			return nil, err
		}
		if body.Key != "" {
			bg(func(ctx context.Context) { s.Updates.Check(ctx) })
		}
	case "check":
		bg(func(ctx context.Context) { s.Updates.Check(ctx) })
	case "install":
		bg(func(ctx context.Context) { s.Updates.Install(ctx, body.Now) })
	case "rollback":
		if err := s.Updates.Rollback(); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown action")
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Quit == nil {
		return nil, errors.New("quit is not available")
	}
	go s.Quit() // after this reply is sent
	return map[string]bool{"ok": true}, nil
}
