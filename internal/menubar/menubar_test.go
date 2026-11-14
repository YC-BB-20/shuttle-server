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
	// On a slow machine the helper may not have written its child's number yet.
	var n int
	for k := 0; k < 250 && n <= 0; k++ {
		pb, _ := os.ReadFile(pidFile)
		n, _ = strconv.Atoi(strings.TrimSpace(string(pb)))
		if n <= 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	<-done
	if n <= 0 {
		t.Fatal("helper never wrote its child's process number")
	}
	// The kill is delivered at once but the child may take a moment to go.
	for k := 0; k < 250 && alive(n); k++ {
		time.Sleep(20 * time.Millisecond)
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
		// No /proc at all (macOS): trust kill. With /proc, a missing entry
		// means the process exited between the two checks.
		_, perr := os.Stat("/proc/self")
		return perr != nil
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] != "Z"
}

// The update item stays open on a click and changes its text in place: it is
// a self-drawn view (a plain item always closes the menu), drawn with the
// menu's own font, colours and highlight, never a button, which drew grey and
// indented. The menu is never closed and reopened.
func TestUpdateItemStaysOpenAndLooksLikeOthers(t *testing.T) {
	s := string(script)
	for _, want := range []string{
		"add('Open Shuttle', 'openPage:');\n  menu.addItem($.NSMenuItem.separatorItem);",
		"upd.view = updView",
		"'mouseUp:'",
		"menuFontOfSize(0)",
		"selectedMenuItemTextColor",
		"'menu:willHighlightItem:'",
		"menu.delegate = target",
		"addTimerForMode(timer, $.NSRunLoopCommonModes)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("menubar.js lacks %q", want)
		}
	}
	if strings.Contains(s, "performClick") || strings.Contains(s, "NSButton") {
		t.Error("update item closes and reopens the menu, or is a button")
	}
}

// An icon that ran a while before quitting (as after the Mac sleeps) is
// restarted every time; Shuttle does not give up on it.
func TestLongRunningHelperNeverGivenUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var log strings.Builder
	Run(ctx, Options{Dir: t.TempDir(), Every: 20 * time.Millisecond, ForgiveAfter: 50 * time.Millisecond,
		Get:     func() Line { return Line{Level: "ok"} },
		Log:     slog.New(slog.NewTextHandler(&log, nil)),
		Command: []string{"/bin/sh", "-c", "sleep 0.1"}})
	if strings.Contains(log.String(), "failing three times") || strings.Count(log.String(), "restarting") < 5 {
		t.Fatalf("log:\n%s", log.String())
	}
}

// The helper quits only when Shuttle's process is gone, not merely when the
// status file is old (it is, for a moment, after the Mac wakes).
func TestHelperChecksServerBeforeQuitting(t *testing.T) {
	for _, want := range []string{"if (serverAlive()) lastGood = now;", "t.setArguments($(['-0', serverPid]));"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("menubar.js lacks %q", want)
		}
	}
}
