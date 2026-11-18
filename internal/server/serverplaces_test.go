package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// tinyDAV is just enough WebDAV for listing, making folders and moving.
type tinyDAV struct {
	mu    sync.Mutex
	items map[string]bool // path under /dav, no slashes at the ends → is a folder
}

func (d *tinyDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dav"), "/")
	switch r.Method {
	case "PROPFIND":
		dir, ok := d.items[p]
		if p != "" && !ok {
			w.WriteHeader(404)
			return
		}
		if p == "" {
			dir = true
		}
		var names []string
		names = append(names, p)
		if r.Header.Get("Depth") == "1" && dir {
			for k := range d.items {
				if k != p && strings.TrimSuffix(k[:strings.LastIndex("/"+k, "/")], "/") == p {
					names = append(names, k)
				}
			}
		}
		sort.Strings(names)
		w.WriteHeader(207)
		fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
		for _, n := range names {
			isDir := n == "" || d.items[n]
			rt := ""
			if isDir {
				rt = "<d:collection/>"
			}
			fmt.Fprintf(w, `<d:response><d:href>/dav/%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype><d:getcontentlength>5</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, n, rt)
		}
		fmt.Fprint(w, `</d:multistatus>`)
	case "MKCOL":
		if _, ok := d.items[p]; ok {
			w.WriteHeader(405)
			return
		}
		d.items[p] = true
		w.WriteHeader(201)
	case "MOVE":
		u, _ := url.Parse(r.Header.Get("Destination"))
		to := strings.Trim(strings.TrimPrefix(u.Path, "/dav"), "/")
		if _, ok := d.items[to]; ok {
			w.WriteHeader(412)
			return
		}
		for k, v := range d.items {
			if k == p || strings.HasPrefix(k, p+"/") {
				delete(d.items, k)
				d.items[to+strings.TrimPrefix(k, p)] = v
			}
		}
		w.WriteHeader(201)
	default:
		w.WriteHeader(405)
	}
}

func TestServerIsAPlaceInFiles(t *testing.T) {
	dav := &tinyDAV{items: map[string]bool{"Shoot": true, "Shoot/a.mov": false, "Shoot/Day 2": true, "Old": true, ".hidden": false}}
	ts := httptest.NewServer(dav)
	defer ts.Close()
	s, _ := newTest()
	s.Servers = &remote.Store{Dir: t.TempDir()}
	s.Jobs = &delJobs{set: copyjob.DefaultSettings}
	pub, err := s.Servers.Save(remote.Config{Name: "Office", Kind: remote.KindWebDAV, URL: ts.URL + "/dav", User: "u", Secret: "secret-pw"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.HTTP().Handler
	do := func(method, path string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
		req.Host = "localhost:8470"
		req.Header.Set("X-Shuttle", "1")
		req.Header.Set("Origin", "http://localhost:8470")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "secret-pw") {
			t.Fatal("the password reached the page")
		}
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	top := remote.Place(pub.ID, "")
	_, out := do("GET", "/api/browse?files=1&path="+url.QueryEscape(top), nil)
	found := false
	for _, p := range out["places"].([]any) {
		pm := p.(map[string]any)
		found = found || pm["kind"] == "remote" && pm["path"] == top && pm["name"] == "Office"
	}
	if !found {
		t.Fatalf("the server is not among the places: %v", out["places"])
	}
	if out["error"] != nil || len(out["folders"].([]any)) != 2 {
		t.Fatalf("top: %v", out)
	}
	_, out = do("GET", "/api/browse?files=1&path="+url.QueryEscape(top+"/Shoot"), nil)
	fs := out["folders"].([]any)
	if len(fs) != 2 || fs[0].(map[string]any)["name"] != "Day 2" || fs[1].(map[string]any)["file"] != true || out["parent"] != top {
		t.Fatalf("Shoot: %v", out)
	}
	if cr := out["crumbs"].([]any); len(cr) != 2 || cr[0].(map[string]any)["name"] != "Office" {
		t.Fatalf("crumbs: %v", cr)
	}
	if code, out := do("POST", "/api/mkdir", map[string]string{"parent": top + "/Shoot", "name": "Day 3"}); code != 200 || !dav.items["Shoot/Day 3"] {
		t.Fatalf("mkdir: %d %v", code, out)
	}
	if code, _ := do("POST", "/api/mkdir", map[string]string{"parent": top + "/Shoot", "name": "Day 3"}); code == 200 {
		t.Fatal("an existing folder was made again")
	}
	code, out := do("POST", "/api/move", map[string]any{"folder": top + "/Shoot", "names": []string{"a.mov", "Day 2"}, "to": top + "/Old"})
	if code != 200 || out["moved"] != float64(2) {
		t.Fatalf("move: %d %v", code, out)
	}
	if _, ok := dav.items["Old/a.mov"]; !ok || !dav.items["Old/Day 2"] {
		t.Fatalf("not moved: %v", dav.items)
	}
	if code, _ := do("POST", "/api/move", map[string]any{"folder": top + "/Old", "names": []string{"Day 2"}, "to": top + "/Old/Day 2"}); code == 200 {
		t.Fatal("a folder was moved into itself")
	}
	if code, _ := do("POST", "/api/move", map[string]any{"folder": top + "/Old", "names": []string{"a.mov"}, "to": t.TempDir()}); code == 200 {
		t.Fatal("a move from a server to this computer was accepted")
	}
}
