package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDAV is a small in-memory WebDAV server under /webdav/.
type fakeDAV struct {
	mu      sync.Mutex
	files   map[string][]byte // path without leading slash
	dirs    map[string]bool
	cutPuts int // PUTs to cut off part-way, keeping what arrived
	free    string
}

func newFakeDAV() *fakeDAV {
	return &fakeDAV{files: map[string][]byte{}, dirs: map[string]bool{"": true}, free: "123000000000"}
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != "owner" || p != "pw" {
		w.WriteHeader(401)
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/webdav")
	if !ok {
		w.WriteHeader(404)
		return
	}
	p = strings.Trim(p, "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case "PROPFIND":
		io.Copy(io.Discard, r.Body)
		f.propfind(w, p, r.Header.Get("Depth"))
	case "MKCOL":
		if f.dirs[p] {
			w.WriteHeader(405)
			return
		}
		if !f.dirs[parent(p)] {
			w.WriteHeader(409)
			return
		}
		f.dirs[p] = true
		w.WriteHeader(201)
	case "PUT":
		if _, ok := f.files[p]; ok && r.Header.Get("If-None-Match") == "*" {
			w.WriteHeader(412)
			return
		}
		if !f.dirs[parent(p)] {
			w.WriteHeader(409)
			return
		}
		if f.cutPuts > 0 {
			f.cutPuts--
			b := make([]byte, 1000)
			n, _ := io.ReadFull(r.Body, b)
			f.files[p] = b[:n] // a cut-off upload leaves a partial file behind
			hj, _ := w.(http.Hijacker)
			c, _, _ := hj.Hijack()
			c.Close()
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.files[p] = b
		w.WriteHeader(201)
	case "GET":
		b, ok := f.files[p]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			off, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.WriteHeader(206)
			w.Write(b[off:])
			return
		}
		w.Write(b)
	case "MOVE":
		d, _ := url.Parse(r.Header.Get("Destination"))
		to := strings.Trim(strings.TrimPrefix(d.Path, "/webdav"), "/")
		b, ok := f.files[p]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if _, exists := f.files[to]; exists && r.Header.Get("Overwrite") == "F" {
			w.WriteHeader(412)
			return
		}
		f.files[to] = b
		delete(f.files, p)
		w.WriteHeader(201)
	default:
		w.WriteHeader(405)
	}
}

func parent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func (f *fakeDAV) propfind(w http.ResponseWriter, p, depth string) {
	item := func(q string) string {
		href := "/webdav/" + (&url.URL{Path: q}).EscapedPath()
		if f.dirs[q] {
			return fmt.Sprintf(`<d:response><d:href>%s/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype><d:quota-available-bytes>%s</d:quota-available-bytes></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, strings.TrimSuffix(href, "/"), f.free)
		}
		return fmt.Sprintf(`<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>Tue, 17 Nov 2026 10:00:00 GMT</d:getlastmodified><d:getetag>"e%d"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, href, len(f.files[q]), len(f.files[q]))
	}
	_, isFile := f.files[p]
	if !f.dirs[p] && !isFile {
		w.WriteHeader(404)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
	b.WriteString(item(p))
	if depth == "1" && f.dirs[p] {
		var kids []string
		for q := range f.dirs {
			if q != "" && q != p && parent(q) == p {
				kids = append(kids, q)
			}
		}
		for q := range f.files {
			if parent(q) == p {
				kids = append(kids, q)
			}
		}
		sort.Strings(kids)
		for _, q := range kids {
			b.WriteString(item(q))
		}
	}
	b.WriteString(`</d:multistatus>`)
	w.WriteHeader(207)
	io.WriteString(w, b.String())
}

func davConfig(u string) Config {
	return Config{Name: "CTERA", Kind: KindWebDAV, URL: u + "/webdav", User: "owner", Secret: "pw"}
}

func TestWebDAVCheckListStat(t *testing.T) {
	f := newFakeDAV()
	f.dirs["Shoot 1"] = true
	f.files["Shoot 1/C 001.mov"] = []byte("hello world")
	f.files["read me.txt"] = []byte("x")
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(davConfig(ts.URL), testOpts())
	ctx := context.Background()
	in, err := r.Check(ctx)
	if err != nil || in.Folders != 1 || in.Files != 1 || in.Free != 123000000000 {
		t.Fatalf("check: %+v %v", in, err)
	}
	var got []string
	if err := r.List(ctx, "Shoot 1", func(e Entry) error { got = append(got, fmt.Sprintf("%s:%d:%v", e.Path, e.Size, e.Dir)); return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Shoot 1/C 001.mov:11:false" {
		t.Fatalf("list: %v", got)
	}
	e, err := r.Stat(ctx, "Shoot 1/C 001.mov")
	if err != nil || e.Size != 11 || e.ETag != "e11" || e.Mod.IsZero() {
		t.Fatalf("stat: %+v %v", e, err)
	}
	if _, err := r.Stat(ctx, "nope.mov"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	rc, err := r.Open(ctx, "Shoot 1/C 001.mov", 6)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "world" {
		t.Fatalf("open: %q", b)
	}
}

func TestWebDAVFolderSetting(t *testing.T) {
	f := newFakeDAV()
	f.dirs["Projects"] = true
	f.dirs["Projects/Job"] = true
	f.files["Projects/Job/a.mov"] = []byte("abc")
	ts := httptest.NewServer(f)
	defer ts.Close()
	c := davConfig(ts.URL)
	c.Folder = "Projects/Job"
	r, _ := New(c, testOpts())
	e, err := r.Stat(context.Background(), "a.mov")
	if err != nil || e.Path != "a.mov" || e.Size != 3 {
		t.Fatalf("stat in folder: %+v %v", e, err)
	}
}

func TestWebDAVWrongLogin(t *testing.T) {
	ts := httptest.NewServer(newFakeDAV())
	defer ts.Close()
	c := davConfig(ts.URL)
	c.Secret = "secret-that-must-not-show"
	r, _ := New(c, testOpts())
	_, err := r.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the login") || strings.Contains(err.Error(), "secret-that") {
		t.Fatalf("wrong login: %v", err)
	}
}

// Upload under a temporary name, a cut-off try replaced by the next one,
// then rename: never over an existing file.
func TestWebDAVUploadRetryAndRename(t *testing.T) {
	f := newFakeDAV()
	f.cutPuts = 1
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(davConfig(ts.URL), testOpts())
	ctx := context.Background()
	if err := r.Mkdir(ctx, "Shoot/Day 1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Mkdir(ctx, "Shoot/Day 1"); err != nil {
		t.Fatalf("existing folder: %v", err)
	}
	src := source(200000)
	var st UploadState
	var last int64
	if err := r.Upload(ctx, "Shoot/Day 1/.c.mov.shuttle-part", int64(len(src)), bytes.NewReader(src), &st, nil, func(n int64) { last = n }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.files["Shoot/Day 1/.c.mov.shuttle-part"], src) || last != int64(len(src)) {
		t.Fatal("uploaded bytes differ")
	}
	if err := r.Rename(ctx, "Shoot/Day 1/.c.mov.shuttle-part", "Shoot/Day 1/c.mov"); err != nil {
		t.Fatal(err)
	}
	f.files["Shoot/Day 1/.d.shuttle-part"] = []byte("new")
	if err := r.Rename(ctx, "Shoot/Day 1/.d.shuttle-part", "Shoot/Day 1/c.mov"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename over a file: %v", err)
	}
	if !bytes.Equal(f.files["Shoot/Day 1/c.mov"], src) {
		t.Fatal("existing file was replaced")
	}
	st = UploadState{}
	if err := r.Upload(ctx, "Shoot/Day 1/c.mov", 3, bytes.NewReader([]byte("new")), &st, nil, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("upload over a file: %v", err)
	}
	if r.Resumable() {
		t.Fatal("WebDAV cannot continue a cut-off upload")
	}
}

// A server that sends the start of an answer and then nothing is cut off by
// the stall timer instead of hanging for ever.
func TestStallTimerCutsHungTransfer(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer ts.Close()
	defer close(release)
	o := testOpts()
	o.Stall = 300 * time.Millisecond
	o.Tries = 1
	r, _ := New(davConfig(ts.URL), o)
	rc, err := r.Open(context.Background(), "x.mov", 0)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	_, err = io.ReadAll(rc)
	rc.Close()
	if err == nil || time.Since(t0) > 5*time.Second {
		t.Fatalf("hung read: %v after %v", err, time.Since(t0))
	}
	if !Retryable(err) || !strings.Contains(err.Error(), "stopped sending") {
		t.Fatalf("stall error: %v", err)
	}
}

func TestConfigValid(t *testing.T) {
	ok := davConfig("https://portal.example.com")
	if err := ok.Valid(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{
		{Name: "x", Kind: KindWebDAV, URL: "http://portal.example.com/webdav", User: "u", Secret: "p"},
		{Name: "x", Kind: KindWebDAV, URL: "https://u:p@portal.example.com/webdav", User: "u", Secret: "p"},
		{Name: "x", Kind: KindS3, URL: "https://portal.example.com", User: "u", Secret: "p"},
		{Name: "", Kind: KindS3, URL: "https://portal.example.com", Bucket: "b", User: "u", Secret: "p"},
		{Name: "x", Kind: KindWebDAV, URL: "https://portal.example.com", User: "u"},
		{Name: "x", Kind: "ftp", URL: "https://portal.example.com", User: "u", Secret: "p"},
	} {
		if c.Valid() == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if _, err := clean("a/../b"); err == nil {
		t.Error("accepted ..")
	}
}

func TestStoreKeepsSecretsOffThePage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "servers")
	f := newFakeDAV()
	ts := httptest.NewServer(f)
	defer ts.Close()
	s := &Store{Dir: dir, Opts: testOpts()}
	c := davConfig(ts.URL)
	p, err := s.Save(c)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, p.ID+".json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi.Mode(), err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode: %v", d.Mode())
	}
	// Changing the name with an empty secret keeps the saved password.
	c.ID, c.Name, c.Secret = p.ID, "CTERA office", ""
	if _, err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	// Changing the user without the password is refused.
	c.User = "other"
	if _, err := s.Save(c); err == nil {
		t.Fatal("kept the password for a different user")
	}
	got, err := s.Check(context.Background(), p.ID)
	if err != nil || got.Test == nil || !got.Test.OK || !strings.Contains(got.Test.Msg, "Connected") || got.Name != "CTERA office" {
		t.Fatalf("check: %+v %v", got.Test, err)
	}
	list, _ := s.List()
	b, _ := json.Marshal(list)
	if len(list) != 1 || strings.Contains(string(b), `"pw"`) || strings.Contains(string(b), "secret") {
		t.Fatalf("list shows: %s", b)
	}
	if err := s.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("not removed")
	}
	if _, err := s.read("../../etc"); err == nil {
		t.Fatal("read outside the folder")
	}
}

func TestStoreShowsOnlyEndOfS3Key(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Opts: testOpts()}
	p, err := s.Save(Config{Name: "CTERA S3", Kind: KindS3, URL: "https://portal.example.com", Bucket: "media", User: "AKIAEXAMPLE1234", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if p.User != "key ending 1234" || !p.Resumable {
		t.Fatalf("public: %+v", p)
	}
	// Renaming it without retyping the keys keeps both.
	if q, err := s.Save(Config{ID: p.ID, Name: "Renamed", Kind: KindS3, URL: "https://portal.example.com", Bucket: "media"}); err != nil || q.User != "key ending 1234" {
		t.Fatalf("rename: %+v %v", q, err)
	}
}
