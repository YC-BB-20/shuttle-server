package server

import (
	"net/http"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/logx"
)

// jobLog returns one copy's log lines (GET /api/jobs/{id}/log?since=…), or
// the last lines of the whole log (GET /api/log). Read only; at most 300
// lines, each month's file read from its last 32 MB (v0.2.87).
func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) {
	if s.LogsDir == "" {
		writeJSON(w, http.StatusOK, []logx.Line{})
		return
	}
	now := time.Now()
	since := now
	if t, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("since")); err == nil && t.Before(now) {
		since = t
	}
	key, id := "", r.PathValue("id")
	if id != "" {
		key = "job"
	}
	lines := logx.Find(s.LogsDir, since, now, key, id, 300)
	if lines == nil {
		lines = []logx.Line{}
	}
	writeJSON(w, http.StatusOK, lines)
}
