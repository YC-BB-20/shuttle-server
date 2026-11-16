package server

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// spaceCheck answers the early warning in New copy: does a copy of "bytes"
// fit on the folder "dest"? It only reads; nothing is added or changed.
// Anything it cannot tell (no folder yet, disk not answering) answers
// "fits", because the copy's own check at Start still stops a full disk.
func (s *Server) spaceCheck(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dest := strings.TrimSpace(q.Get("dest"))
	need, err := strconv.ParseInt(q.Get("bytes"), 10, 64)
	sc, ok := s.Jobs.(spaceChecker)
	if err != nil || need < 0 || !ok || dest == "" || !filepath.IsAbs(dest) {
		writeJSON(w, http.StatusOK, map[string]any{"fits": true})
		return
	}
	short, err := sc.CheckSpace(dest, need)
	if err != nil || short == nil {
		writeJSON(w, http.StatusOK, map[string]any{"fits": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fits": false, "space": short})
}
