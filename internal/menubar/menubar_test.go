package menubar

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStatusFileAndHelperStoppedWithShuttle(t *testing.T) {
	dir := t.TempDir()
	pidFile := dir + "/pid"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, Options{Dir: dir, URL: "http://localhost:8470", Every: 50 * time.Millisecond,
			Get: func() Line {
				return Line{Level: "warn", Headline: "Waiting\nfor space", Version: "0.1.1", Update: "available", UpdateVersion: "0.2.1"}
			},
			Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			Command: []string{"/bin/sh", "-c", "sleep 60 & echo $! > " + pidFile + "; wait"}})
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	b, err := os.ReadFile(StatusFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Split(string(b), "\n")
	if f[1] != "warn" || f[2] != "Waiting for space" || f[3] != "0.1.1" || f[4] != "http://localhost:8470" || f[5] != "available" || f[6] != "0.2.1" {
		t.Fatalf("status file %q", b)
	}
	pb, _ := os.ReadFile(pidFile)
	pid := strings.TrimSpace(string(pb))
	cancel()
	<-done
	var n int
	for _, c := range pid {
		n = n*10 + int(c-'0')
	}
	if alive(n) {
		t.Fatal("helper's child still running after Shuttle stopped")
	}
	if _, err := os.Stat(StatusFile(dir)); err == nil {
		t.Fatal("status file left behind")
	}
}

func TestGivesUpAfterThreeFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var log strings.Builder
	Run(ctx, Options{Dir: t.TempDir(), Every: 20 * time.Millisecond,
		Get:     func() Line { return Line{Level: "ok"} },
		Log:     slog.New(slog.NewTextHandler(&log, nil)),
		Command: []string{"/bin/sh", "-c", "echo broken >&2; exit 1"}})
	if strings.Count(log.String(), "restarting") != 3 || !strings.Contains(log.String(), "failing three times") || !strings.Contains(log.String(), "broken") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestTailBufferBounded(t *testing.T) {
	tb := &tailBuffer{max: 10}
	tb.Write([]byte(strings.Repeat("x", 100) + "0123456789"))
	if tb.String() != "0123456789" {
		t.Fatal(tb.String())
	}
}

// alive treats a zombie (stopped, waiting to be reaped by init) as stopped.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no /proc (macOS): trust kill
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] != "Z"
}

// The update item must not close the menu when pressed: it is a button inside
// a view, and the refresh timer also runs while the menu is open, so the text
// moves on to "Checking for updates…" or "Installing update…" in place.
func TestUpdateItemKeepsMenuOpen(t *testing.T) {
	s := string(script)
	for _, want := range []string{
		"upd.view = updView",
		"buttonWithTitleTargetAction('Check for updates', target, 'updateAction:')",
		"addTimerForMode(timer, $.NSRunLoopCommonModes)",
		"updButton.title = title",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("menubar.js lacks %q", want)
		}
	}
	if strings.Contains(s, "add('Check for updates', 'updateAction:')") {
		t.Error("update is still a plain menu item, which closes the menu")
	}
}
