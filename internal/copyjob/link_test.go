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
	j, err := m.AddLink(context.Background(), link, dest)
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
	j2, err := m.AddLink(context.Background(), srv.URL+"/other.mov", dest)
	if err != nil {
		t.Fatal(err)
	}
	v2 := waitState(t, m, j2.st.ID, "finished")
	if !strings.Contains(v2.Error, "expired") {
		t.Fatalf("expired link: %q", v2.Error)
	}

	// Links that are not plain files are refused with a reason.
	for _, bad := range []string{"https://vimeo.com/123", "ftp://x/y", "https://app.frame.io/reviews/abc"} {
		if _, err := m.AddLink(context.Background(), bad, dest); err == nil {
			t.Fatalf("%s was accepted", bad)
		}
	}
}
