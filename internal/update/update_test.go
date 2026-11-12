package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const goodKey = "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz"

// artifact builds the zip-holding-a-tar.gz that CI uploads, around a tiny
// script standing in for the program.
func artifact(script string) []byte {
	var tg bytes.Buffer
	gz := gzip.NewWriter(&tg)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "shuttle-server", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg})
	tw.Write([]byte(script))
	tw.Close()
	gz.Close()
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	w, _ := zw.Create("shuttle-server-x.tar.gz")
	w.Write(tg.Bytes())
	zw.Close()
	return z.Bytes()
}

func fakeGitHub(t *testing.T, sha, script string, sawKey *string) *httptest.Server {
	name := "shuttle-server-" + runtime.GOOS + "-" + runtime.GOARCH
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*sawKey = r.Header.Get("Authorization")
		switch {
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{map[string]any{
				"id": 42, "head_sha": sha, "created_at": "2026-11-12T10:00:00Z",
				"head_commit": map[string]any{"message": "v0.2.1: better things\n\nbody"}}}})
		case strings.HasSuffix(r.URL.Path, "/runs/42/artifacts"):
			json.NewEncoder(w).Encode(map[string]any{"artifacts": []any{map[string]any{
				"id": 7, "name": name, "expired": false, "archive_download_url": srv.URL + "/dl/7"}}})
		case r.URL.Path == "/dl/7":
			w.Write(artifact(script))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func newUpdater(t *testing.T, api string) (*Updater, *bool) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "shuttle-server")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\necho old\n"), 0o755)
	restarted := false
	u := &Updater{Repo: "o/r", API: api, KeyPath: filepath.Join(dir, "secrets", "github-key"), BinPath: bin,
		Version: "0.2.0", Commit: "d67a650", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Restart: func() { restarted = true }}
	u.Init()
	return u, &restarted
}

func TestCheckInstallBackupAndRestart(t *testing.T) {
	var saw string
	gh := fakeGitHub(t, "abcdef1234567", "#!/bin/sh\necho 'Shuttle Server 0.2.1 (abcdef1)'\n", &saw)
	defer gh.Close()
	u, restarted := newUpdater(t, gh.URL)
	if _, err := u.Check(context.Background()); err == nil {
		t.Fatal("check worked without a key")
	}
	if err := u.SetKey("not a key"); err == nil {
		t.Fatal("bad key accepted")
	}
	if err := u.SetKey(goodKey); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(u.KeyPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", fi.Mode())
	}
	rel, err := u.Check(context.Background())
	if err != nil || rel == nil || rel.Version != "0.2.1" || rel.Commit != "abcdef1" {
		t.Fatalf("%+v %v", rel, err)
	}
	if saw != "Bearer "+goodKey {
		t.Fatal("key not sent to GitHub")
	}
	js, _ := json.Marshal(u.State())
	if strings.Contains(string(js), goodKey) || strings.Contains(string(js), "github_pat") {
		t.Fatal("the key leaked into the state shown on the page")
	}
	if err := u.Install(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !*restarted {
		t.Fatal("no restart after install")
	}
	b, _ := os.ReadFile(u.BinPath)
	if !strings.Contains(string(b), "0.2.1") {
		t.Fatal("new version not in place")
	}
	s := u.State()
	old, err := os.ReadFile(s.Backup)
	if err != nil || string(old) != "#!/bin/sh\necho old\n" || !strings.Contains(s.Undo, "cp ") {
		t.Fatalf("backup %q %v undo %q", old, err, s.Undo)
	}
}

func TestBrokenDownloadChangesNothing(t *testing.T) {
	var saw string
	gh := fakeGitHub(t, "abcdef1234567", "#!/bin/sh\necho 'Shuttle Server 0.2.1 (0000000)'\n", &saw)
	defer gh.Close()
	u, restarted := newUpdater(t, gh.URL)
	u.SetKey(goodKey)
	if err := u.Install(context.Background(), true); err == nil {
		t.Fatal("a build reporting the wrong commit was installed")
	}
	b, _ := os.ReadFile(u.BinPath)
	if string(b) != "#!/bin/sh\necho old\n" || *restarted {
		t.Fatal("the current program was touched")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(u.BinPath), ".shuttle-server.new")); err == nil {
		t.Fatal("rejected download left behind")
	}
}

func TestUpToDateAndWaitForCopy(t *testing.T) {
	var saw string
	gh := fakeGitHub(t, "d67a650fffff", "", &saw)
	defer gh.Close()
	u, _ := newUpdater(t, gh.URL)
	u.SetKey(goodKey)
	if rel, err := u.Check(context.Background()); err != nil || rel != nil {
		t.Fatalf("same commit offered as an update: %+v %v", rel, err)
	}
	gh2 := fakeGitHub(t, "abcdef1234567", "#!/bin/sh\necho 'x (abcdef1)'\n", &saw)
	defer gh2.Close()
	u.API = gh2.URL
	u.CopyBusy = func() bool { return true }
	if err := u.Install(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !u.State().Waiting {
		t.Fatal("did not wait for the running copy")
	}
	b, _ := os.ReadFile(u.BinPath)
	if string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("installed during a copy")
	}
}

func TestRollback(t *testing.T) {
	u, restarted := newUpdater(t, "http://unused")
	if u.Rollback() == nil {
		t.Fatal("rollback with no backup")
	}
	bdir := filepath.Join(filepath.Dir(u.BinPath), "backups")
	os.MkdirAll(bdir, 0o755)
	os.WriteFile(filepath.Join(bdir, "shuttle-server-0.1.1-aeba491"), []byte("previous"), 0o755)
	if err := u.Rollback(); err != nil || !*restarted {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(u.BinPath); string(b) != "previous" {
		t.Fatal(fmt.Sprintf("%q", b))
	}
	if _, err := os.Stat(filepath.Join(bdir, "shuttle-server-0.2.0-d67a650")); err != nil {
		t.Fatal("the version rolled back from was not kept")
	}
}

// The history lists green builds and kept versions; any of them can be
// installed, a kept one without downloading, each checked like an update.
func TestHistoryAndInstallAChosenVersion(t *testing.T) {
	var saw string
	gh := fakeGitHub(t, "abcdef1234567", "#!/bin/sh\necho 'Shuttle Server 0.2.1 (abcdef1)'\n", &saw)
	defer gh.Close()
	u, restarted := newUpdater(t, gh.URL)
	backups := filepath.Join(filepath.Dir(u.BinPath), "backups")
	os.MkdirAll(backups, 0o755)
	os.WriteFile(filepath.Join(backups, "shuttle-server-0.1.1-1111111"), []byte("#!/bin/sh\necho 'Shuttle Server 0.1.1 (1111111)'\n"), 0o755)

	// No key: kept versions and the running one are still listed.
	h, note := u.History(context.Background())
	if note == "" || len(h) != 2 {
		t.Fatalf("without a key: %+v %q", h, note)
	}
	u.SetKey(goodKey)
	h, note = u.History(context.Background())
	byCommit := map[string]Choice{}
	for _, c := range h {
		byCommit[c.Commit] = c
	}
	if note != "" || !byCommit["abcdef1"].OnGitHub || !byCommit["1111111"].Kept || !byCommit["d67a650"].Installed {
		t.Fatalf("history %+v %q", h, note)
	}
	if err := u.InstallCommit(context.Background(), "d67a650"); err == nil {
		t.Fatal("installing the running version should be refused")
	}

	// A kept version: no download, and the running one is kept in turn.
	gh.Close()
	if err := u.InstallCommit(context.Background(), "1111111"); err != nil || !*restarted {
		t.Fatalf("kept version: %v restarted=%v", err, *restarted)
	}
	if b, _ := os.ReadFile(u.BinPath); !strings.Contains(string(b), "0.1.1") {
		t.Fatal("chosen version not in place")
	}
	if _, err := os.Stat(filepath.Join(backups, "shuttle-server-0.2.0-d67a650")); err != nil {
		t.Fatal("the replaced version was not kept")
	}
}

func TestChosenVersionMustStartAndNameItsCommit(t *testing.T) {
	var saw string
	gh := fakeGitHub(t, "abcdef1234567", "#!/bin/sh\necho 'something else'\n", &saw)
	defer gh.Close()
	u, restarted := newUpdater(t, gh.URL)
	u.SetKey(goodKey)
	if err := u.InstallCommit(context.Background(), "abcdef1"); err == nil || *restarted {
		t.Fatalf("a build that does not name its commit was installed: %v", err)
	}
	if b, _ := os.ReadFile(u.BinPath); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("the running version was changed")
	}
}
