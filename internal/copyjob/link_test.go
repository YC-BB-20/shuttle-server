package copyjob

import (
	"bytes"
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/linkdl"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// v0.2.90: a link download is queued, fetched, read back, hashed, named
// without overwriting, continues a partial file, and fails plainly when the
// link has expired. The link never reaches the job file or the log.
func TestLinkDownload(t *testing.T) {
	linkdl.AllowLocal = true
	defer func() { linkdl.AllowLocal = false }()
	data := make([]byte, 3<<20+17)
	rand.New(rand.NewSource(9)).Read(data)
	var ranged atomic.Int32
	expired := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired.Load() && r.Header.Get("Range") != "bytes=0-0" {
			http.Error(w, "gone", http.StatusForbidden)
			return
		}
		if rg := r.Header.Get("Range"); rg != "" && rg != "bytes=0-0" {
			ranged.Add(1)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="A001 clip.mov"`)
		http.ServeContent(w, r, "x.mov", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	defer srv.Close()
	link := srv.URL + "/download/x?sig=SECRET123#rq-path=Day%201/Cam%20A"

	dest, state := t.TempDir(), t.TempDir()
	m := newManager(t, state)
	m.Secrets = filepath.Join(t.TempDir(), "links")
	// A file with the same name is already there: it is never replaced.
	os.MkdirAll(filepath.Join(dest, "Day 1", "Cam A"), 0o755)
	os.WriteFile(filepath.Join(dest, "Day 1", "Cam A", "A001 clip.mov"), []byte("mine"), 0o644)
	// Half the file is already there from an earlier try: it continues.
	j, err := m.AddLink(context.Background(), link, dest, LinkOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, part := linkTarget(j.st)
	os.WriteFile(part, data[:1<<20], 0o644)
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "finished")
	if v.Error != "" || v.LinkHash != xxh3.Sum128(data).String() {
		t.Fatalf("finished with %q, hash %s", v.Error, v.LinkHash)
	}
	if ranged.Load() == 0 {
		t.Fatal("the partial file was not continued")
	}
	got, err := os.ReadFile(filepath.Join(dest, "Day 1", "Cam A", "A001 clip (2).mov"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("downloaded file wrong: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "Day 1", "Cam A", "A001 clip.mov")); string(b) != "mine" {
		t.Fatal("the file already there was changed")
	}
	if _, err := os.Stat(part); err == nil {
		t.Fatal("partial file left behind")
	}
	if _, err := os.Stat(m.linkPath(j.st.ID)); err == nil {
		t.Fatal("the saved link was not removed")
	}
	jb, _ := os.ReadFile(filepath.Join(state, j.st.ID, "job.json"))
	if strings.Contains(string(jb), "SECRET123") || strings.Contains(string(jb), "/download/") {
		t.Fatal("the link reached the job file")
	}
	if v.FromDisk != "127.0.0.1" || v.LinkName != "A001 clip.mov" || v.Checks == nil || v.Checks.Dest != 1 {
		t.Fatalf("view %+v", v)
	}

	// An expired link fails with the reason and leaves nothing behind.
	expired.Store(true)
	j2, err := m.AddLink(context.Background(), srv.URL+"/other.mov", dest, LinkOpts{})
	if err != nil {
		t.Fatal(err)
	}
	v2 := waitState(t, m, j2.st.ID, "finished")
	if !strings.Contains(v2.Error, "expired") {
		t.Fatalf("expired link: %q", v2.Error)
	}

	// Links that are not plain files are refused with a reason.
	for _, bad := range []string{"https://vimeo.com/123", "ftp://x/y", "https://app.frame.io/reviews/abc"} {
		if _, err := m.AddLink(context.Background(), bad, dest, LinkOpts{}); err == nil {
			t.Fatalf("%s was accepted", bad)
		}
	}
}

// v0.2.92: links added together are one job. Its files keep the folders
// their links name, download one after another in folder order, one that
// fails does not stop the others, and the Files tab lists them as folders.
func TestLinkGroup(t *testing.T) {
	linkdl.AllowLocal = true
	defer func() { linkdl.AllowLocal = false }()
	files := map[string][]byte{}
	for i, n := range []string{"a.mov", "b.mov", "c.mov", "gone.mov"} {
		b := make([]byte, 200<<10+i)
		rand.New(rand.NewSource(int64(i))).Read(b)
		files[n] = b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/")
		if n == "gone.mov" && r.Header.Get("Range") != "bytes=0-0" {
			http.Error(w, "gone", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+n+`"`)
		http.ServeContent(w, r, n, time.Unix(1700000000, 0), bytes.NewReader(files[n]))
	}))
	defer srv.Close()
	links := []string{
		srv.URL + "/c.mov?sig=SECRET9#rq-path=Shoot/Day%202&rq-share=Shoot",
		srv.URL + "/gone.mov?sig=SECRET9#rq-path=Shoot/Day%202&rq-share=Shoot",
		srv.URL + "/b.mov?sig=SECRET9#rq-path=Shoot/Day%201/Cam%20A&rq-share=Shoot",
		srv.URL + "/a.mov?sig=SECRET9#rq-path=Shoot&rq-share=Shoot",
		"ftp://not/a/link",
	}
	dest, st := t.TempDir(), t.TempDir()
	m := newManager(t, st)
	m.Secrets = filepath.Join(t.TempDir(), "links")
	j, errs, err := m.AddLinks(context.Background(), links, dest, LinkOpts{})
	if err != nil || j == nil || len(errs) != 1 {
		t.Fatalf("add: %v %v %v", j, errs, err)
	}
	var order []string
	for _, it := range j.st.Spec.Items {
		order = append(order, linkRel(it))
	}
	if want := "Shoot/Day 1/Cam A/b.mov Shoot/Day 2/c.mov Shoot/Day 2/gone.mov Shoot/a.mov"; strings.Join(order, " ") != want {
		t.Fatalf("order %q", order)
	}
	if j.st.Files != 4 || j.st.Spec.LinkName != "Shoot" || linkSite(j.st.Spec) != "Frame.io" {
		t.Fatalf("job %d files, name %q", j.st.Files, j.st.Spec.LinkName)
	}
	// Before it starts, the Files tab shows the folders that will be made.
	p, err := m.FilesIn(j.st.ID, "all", true, "Shoot", 0, 50)
	if err != nil || len(p.Rows) != 1 || p.Rows[0].Path != "Shoot/a.mov" || len(p.Folders) != 2 || p.Folders[1].Files != 2 {
		t.Fatalf("files before: %+v %v", p, err)
	}
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "finished")
	if !strings.Contains(v.Error, "1 of 4 files") || !strings.Contains(v.Error, "gone.mov") || v.Verified != 3 || v.Failed != 1 {
		t.Fatalf("finished: %q, %d verified, %d failed", v.Error, v.Verified, v.Failed)
	}
	for rel, n := range map[string]string{"Shoot/a.mov": "a.mov", "Shoot/Day 1/Cam A/b.mov": "b.mov", "Shoot/Day 2/c.mov": "c.mov"} {
		if b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel))); err != nil || !bytes.Equal(b, files[n]) {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "Shoot", "Day 2", "gone.mov")); err == nil {
		t.Fatal("the failed file has a name")
	}
	if _, err := os.Stat(m.linkPath(j.st.ID)); err == nil {
		t.Fatal("the saved links were not removed")
	}
	if jb, _ := os.ReadFile(filepath.Join(st, j.st.ID, "job.json")); strings.Contains(string(jb), "SECRET9") {
		t.Fatal("a link reached the job file")
	}
	p, _ = m.FilesIn(j.st.ID, "problems", false, "", 0, 50)
	if len(p.Rows) != 1 || p.Rows[0].Path != "Shoot/Day 2/gone.mov" || p.Rows[0].Why == "" || p.Counts["done"] != 3 {
		t.Fatalf("files after: %+v", p)
	}
}

// v0.2.95: a download says where it comes from; the destination folder's
// name (say "Vimeo") has nothing to do with it.
func TestLinkSiteNames(t *testing.T) {
	for want, ls := range map[string]LinkSpec{
		"Frame.io":    {Host: "app.frame.io"},
		"Frame.io ":   {Host: "frameio-assets.s3.amazonaws.com"},
		"Frame.io  ":  {Host: "d1.cloudfront.net", Share: "Shoot"},
		"Vimeo":       {Host: "vimeo.com", Site: "vimeo"},
		"SoundCloud":  {Host: "soundcloud.com", Site: "soundcloud"},
		"example.com": {Host: "www.example.com"},
	} {
		if got := siteName(ls); got != strings.TrimSpace(want) {
			t.Errorf("%+v: %q", ls, got)
		}
	}
	a, b := LinkSpec{Host: "app.frame.io"}, LinkSpec{Host: "example.com"}
	if linkSite(Spec{Link: &a, Items: []LinkSpec{a, a}}) != "Frame.io" || linkSite(Spec{Link: &a, Items: []LinkSpec{a, b}}) != "Links" {
		t.Error("a job of several links is named wrongly")
	}
}
