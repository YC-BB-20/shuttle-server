package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
		limit := int64(64 << 10)
		if strings.HasPrefix(r.URL.Path, "/api/links/") {
			limit = 2 << 20 // up to 200 pasted links; signed Frame.io links are over 1 KB each
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
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
	if spec.Kind == copyjob.KindVerify && spec.Source == "" {
		return nil, errors.New("choose a folder or file to verify")
	}
	if spec.Kind != copyjob.KindVerify && (spec.Source == "" || spec.Dest == "") {
		return nil, errors.New("choose a source folder and a destination folder")
	}
	add := func() error { _, err := s.Jobs.Add(spec); return err }
	if d, ok := s.Jobs.(drafter); ok && spec.Kind != copyjob.KindVerify {
		// v0.2.72: the list the picker's count made, if it is for this copy.
		if dr := s.sizes.take(filepath.Clean(strings.TrimSpace(spec.Source)), spec.Only); dr != nil {
			add = func() error { _, err := d.AddDraft(spec, dr); return err }
		}
	}
	if err := add(); err != nil {
		var fe *copyjob.FormatError
		if errors.As(err, &fe) {
			// v0.2.92: some files may not fit the destination's format; the
			// page asks "copy the rest" (sent again with ignore_format) or cancel.
			return map[string]any{"ok": false, "format": fe.Short}, nil
		}
		var se *copyjob.SpaceError
		if errors.As(err, &se) {
			// Not added: the page asks "continue anyway" (sent again with
			// ignore_space) or "cancel job" (nothing to undo).
			return map[string]any{"ok": false, "space": se.Short}, nil
		}
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
	case "continue": // the copy may not fit; the owner lets it run anyway
		err = s.Jobs.ContinueAnyway(id)
	case "move": // v0.2.85: a new place in the queue, right before another job or at the end
		var b struct {
			Before string `json:"before"`
		}
		if err = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&b); err == nil {
			err = s.Jobs.Move(id, b.Before)
		}
	case "retry": // v0.2.92: a finished copy's failed files, or one of them, back in the queue
		b := struct {
			File *int `json:"file"`
		}{}
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&b) // no body: every failed file
		rt, ok := s.Jobs.(retrier)
		if !ok {
			return nil, errors.New("not available")
		}
		file := -1
		if b.File != nil {
			file = *b.File
		}
		n, err := rt.RetryFailed(id, file)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "files": n}, nil
	case "keep-failed": // v0.2.99: the answer "leave them failed" to a copy that ended with failed files
		kf, ok := s.Jobs.(failKeeper)
		if !ok {
			return nil, errors.New("not available")
		}
		err = kf.KeepFailed(id)
	case "hide", "unhide": // a history entry, page view only
		err = s.Jobs.SetHidden(id, r.PathValue("action") == "hide")
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

// files is one page of a copy's file list.
func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	if s.Jobs == nil {
		writeJSON(w, http.StatusOK, copyjob.FilePage{Rows: []copyjob.FileRow{}, Next: -1})
		return
	}
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	p, err := s.Jobs.FilesIn(r.PathValue("id"), q.Get("filter"), q.Get("tree") == "1", q.Get("dir"), from, limit)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// fileInfo is everything known about one file of a copy.
func (s *Server) fileInfo(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if s.Jobs == nil || err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
		return
	}
	fi, err := s.Jobs.FileInfo(r.PathValue("id"), i)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, fi)
}

// recheck asks for one file to be read again in full: {"which":"source"}
// or {"which":"dest"}. It only reads.
func (s *Server) recheck(w http.ResponseWriter, r *http.Request) (any, error) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		return nil, errors.New("no such file")
	}
	var body struct {
		Which string `json:"which"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body)
	err = s.Jobs.AskRecheck(r.PathValue("id"), i, body.Which)
	return map[string]bool{"ok": err == nil}, err
}

// jobHistory lists finished and stopped copies, 20 at a time.
func (s *Server) jobHistory(w http.ResponseWriter, r *http.Request) {
	if s.Jobs == nil {
		writeJSON(w, http.StatusOK, []copyjob.View{})
		return
	}
	skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
	writeJSON(w, http.StatusOK, s.Jobs.History(max(skip, 0), 20))
}

func (s *Server) copySettings(w http.ResponseWriter, r *http.Request) {
	if s.Jobs == nil {
		writeJSON(w, http.StatusOK, copyjob.DefaultSettings)
		return
	}
	writeJSON(w, http.StatusOK, s.Jobs.Settings())
}

func (s *Server) saveCopySettings(w http.ResponseWriter, r *http.Request) (any, error) {
	var c copyjob.Settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&c); err != nil {
		return nil, errors.New("could not read the request")
	}
	return s.Jobs.SetSettings(c)
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
		Key    string `json:"key"`
		Now    bool   `json:"now"`
		Commit string `json:"commit"`
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
	case "choose": // a version picked from the history
		bg(func(ctx context.Context) { s.Updates.InstallCommit(ctx, body.Commit) })
	case "rollback":
		if err := s.Updates.Rollback(); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown action")
	}
	return map[string]bool{"ok": true}, nil
}

// history lists the versions that can be installed: recent green builds
// and every version kept on this computer.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.Updates == nil {
		json.NewEncoder(w).Encode(map[string]any{"versions": []any{}, "note": "updates are not available"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	v, note := s.Updates.History(ctx)
	json.NewEncoder(w).Encode(map[string]any{"versions": v, "note": note})
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Quit == nil {
		return nil, errors.New("quit is not available")
	}
	go s.Quit() // after this reply is sent
	return map[string]bool{"ok": true}, nil
}

type loginState struct {
	Supported bool `json:"supported"`
	On        bool `json:"on"`
}

func (s *Server) loginState() any {
	if s.Login == nil {
		return loginState{}
	}
	return loginState{Supported: true, On: s.Login.Enabled()}
}

func (s *Server) saveLogin(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Login == nil {
		return nil, errors.New("starting at login is not available on this computer")
	}
	var in struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil {
		return nil, errors.New("could not read the request")
	}
	if err := s.Login.Set(in.On); err != nil {
		return nil, fmt.Errorf("could not change starting at login: %w", err)
	}
	return s.loginState(), nil
}

// added is one page of the files found added to a copy's source after the copy.
func (s *Server) added(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.Jobs.(sourceChecker)
	if s.Jobs == nil || !ok {
		writeJSON(w, http.StatusOK, map[string]any{"rows": []copyjob.AddedFile{}, "next": -1})
		return
	}
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, next, err := sc.AddedList(r.PathValue("id"), from, limit)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "next": next})
}

func (s *Server) checkSource(w http.ResponseWriter, r *http.Request) (any, error) {
	sc, ok := s.Jobs.(sourceChecker)
	if !ok {
		return nil, errors.New("not available")
	}
	err := sc.CheckSourceAgain(r.PathValue("id"))
	return map[string]bool{"ok": err == nil}, err
}

func (s *Server) copyAdded(w http.ResponseWriter, r *http.Request) (any, error) {
	sc, ok := s.Jobs.(sourceChecker)
	if !ok {
		return nil, errors.New("not available")
	}
	id, err := sc.CopyAdded(r.PathValue("id"))
	return map[string]any{"ok": err == nil, "id": id}, err
}

// export sends a copy's hash list as a download (v0.2.80): built from the
// copy's records, streamed, never held in memory, nothing read from a disk.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	ex, ok := s.Jobs.(exporter)
	if s.Jobs == nil || !ok {
		http.Error(w, "not available", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	format, ext, ctype := copyjob.FormatXXHSum, "txt", "text/plain; charset=utf-8"
	if q.Get("format") == copyjob.FormatCSV {
		format, ext, ctype = copyjob.FormatCSV, "csv", "text/csv; charset=utf-8"
	}
	id := r.PathValue("id")
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="shuttle-hashes-`+ext2name(id)+`.`+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, _, err := ex.Export(w, id, format, q.Get("algo"), q["path"]); err != nil {
		// Nothing was written yet in the common failures (no such copy), so an error page can still be sent.
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// ext2name keeps only characters that are safe in a file name.
func ext2name(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return -1
	}, id)
}

// maxBulk bounds how many jobs one bulk request acts on.
const maxBulk = 500

// bulkAction pauses, resumes, stops or moves several jobs at once (v0.2.94,
// the queue's selection): POST /api/jobs/bulk {"action": "stop", "ids":
// [...]}, or {"action": "move", "moves": [{"id": …, "before": …}, …]}, each
// move as POST /api/jobs/{id}/move, done in turn ("before" empty: the end).
// The jobs are handled in the order given (the page puts the running one
// last, so the next one does not start in between). One that cannot be
// handled does not stop the others; the answer says how many were done and
// why the rest were not.
func (s *Server) bulkAction(w http.ResponseWriter, r *http.Request) (any, error) {
	var b struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
		Moves  []struct {
			ID     string `json:"id"`
			Before string `json:"before"`
		} `json:"moves"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b); err != nil {
		return nil, errors.New("could not read the request")
	}
	if b.Action == "move" {
		if len(b.Moves) == 0 {
			return nil, errors.New("nothing is selected")
		}
		if len(b.Moves) > maxBulk {
			return nil, fmt.Errorf("select at most %d at a time", maxBulk)
		}
		done, errs := 0, []string{}
		for _, mv := range b.Moves {
			if err := s.Jobs.Move(mv.ID, mv.Before); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			done++
		}
		return map[string]any{"ok": len(errs) == 0, "done": done, "errors": errs}, nil
	}
	var do func(string) error
	switch b.Action {
	case "pause":
		do = s.Jobs.Pause
	case "resume":
		do = s.Jobs.Resume
	case "stop":
		do = s.Jobs.Stop
	default:
		return nil, errors.New("unknown action")
	}
	if len(b.IDs) == 0 {
		return nil, errors.New("nothing is selected")
	}
	if len(b.IDs) > maxBulk {
		return nil, fmt.Errorf("select at most %d at a time", maxBulk)
	}
	done, errs := 0, []string{}
	for _, id := range b.IDs {
		if err := do(id); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		done++
	}
	return map[string]any{"ok": len(errs) == 0, "done": done, "errors": errs}, nil
}
