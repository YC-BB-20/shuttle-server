package app

import (
	"bytes"
	"encoding/json"
	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/safemode"
)

func runBriefly(t *testing.T, data string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	go func() { time.Sleep(400 * time.Millisecond); syscall.Kill(os.Getpid(), syscall.SIGINT) }()
	code := Run(Options{DataDir: data, Stdout: &out, StableAfter: time.Hour})
	return code, out.String()
}

func writeConfig(t *testing.T, data, body string) {
	os.MkdirAll(data, 0o700)
	if err := os.WriteFile(filepath.Join(data, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStartsAndQuitsCleanly(t *testing.T) {
	data := t.TempDir()
	writeConfig(t, data, `{"listen":"127.0.0.1:0"}`)
	code, out := runBriefly(t, data)
	if code != ExitOK || !strings.Contains(out, "Shuttle Server running") {
		t.Fatalf("code %d\n%s", code, out)
	}
	var rec safemode.Record
	b, _ := os.ReadFile(filepath.Join(data, "state", "startup.json"))
	json.Unmarshal(b, &rec)
	if len(rec.Attempts) != 0 {
		t.Fatal("clean quit left a start attempt counted")
	}
	for _, d := range []string{"logs", "records", "temp.noindex"} {
		if _, err := os.Stat(filepath.Join(data, d)); err != nil {
			t.Errorf("missing %s", d)
		}
	}
}

func TestDamagedConfigMeansSafeMode(t *testing.T) {
	data := t.TempDir()
	writeConfig(t, data, `{"listen":"127.0.0.1:0", "cpu_share": 5}`)
	// The bad config forces defaults, including port 8470; skip if it is taken.
	code, out := runBriefly(t, data)
	if code == ExitStartup && strings.Contains(out, "already running") {
		t.Skip("port 8470 in use")
	}
	if !strings.Contains(out, "safe_mode=true") || !strings.Contains(out, "cpu_share") {
		t.Fatalf("not in safe mode:\n%s", out)
	}
}

func TestProgressHeadlineShowsCopiedAndVerified(t *testing.T) {
	for _, c := range []struct {
		v    copyjob.View
		want string
	}{
		{copyjob.View{Files: 3, Copied: 2}, "Copying — 2 of 3 files copied, 0 verified"},
		{copyjob.View{Files: 3, Verified: 1, Copied: 1}, "Copying — 2 of 3 files copied, 1 verified"},
		{copyjob.View{Files: 3, Hashed: 2, Check: "none"}, "Copying — 2 of 3 files copied (not read back)"},
		{copyjob.View{Files: 3, Identical: 2, Check: "none", Stage: "Copying (the source is hashed as it is read)"}, "Copying — 0 of 1 files copied · 2 already there, verified"},
		{copyjob.View{Files: 3, Identical: 1, Verified: 1, Stage: "Reading back the destination"}, "Verifying — 1 of 2 files copied, 1 verified · 1 already there, verified"},
		{copyjob.View{Files: 2, Stage: "Already at the destination: reading it in full to compare"}, "Verifying — 0 of 2 files copied, 0 verified"},
		{copyjob.View{Files: 3, Verified: 1, Kind: "verify"}, "Verifying — 1 of 3 files checked"},
	} {
		if got := progressHeadline(&c.v); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
