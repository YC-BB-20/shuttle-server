package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// With nothing that can check space, or a bad request, the answer is "fits":
// the warning never blocks anything.
func TestSpaceCheckAnswersFitsWhenItCannotTell(t *testing.T) {
	s := &Server{}
	for _, u := range []string{"/api/spacecheck", "/api/spacecheck?dest=rel&bytes=5", "/api/spacecheck?dest=/x&bytes=-1", "/api/spacecheck?dest=/x&bytes=5"} {
		w := httptest.NewRecorder()
		s.spaceCheck(w, httptest.NewRequest(http.MethodGet, u, nil))
		if w.Code != 200 || w.Body.String() != "{\"fits\":true}\n" {
			t.Errorf("%s: %d %q", u, w.Code, w.Body.String())
		}
	}
}
