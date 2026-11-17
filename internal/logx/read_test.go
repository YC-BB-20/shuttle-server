package logx

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindJobLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	l, done, err := Open(dir, os.Stderr, now)
	if err != nil {
		t.Fatal(err)
	}
	l = slog.New(l.Handler()) // same handler
	l.Info("copy job added", "job", "20261117-120000.000", "source", "/Volumes/A 1/x")
	l.Info("other", "job", "20261117-120000.000-2")
	l.Warn("file changed", "job", "20261117-120000.000", "path", `a "b".mov`)
	done()
	got := Find(dir, now, now, "job", "20261117-120000.000", 10)
	if len(got) != 2 {
		t.Fatalf("got %d lines: %+v", len(got), got)
	}
	if got[0].Msg != "copy job added" || got[0].More != "source=/Volumes/A 1/x" || got[1].Level != "WARN" || got[1].More != `path=a "b".mov` {
		t.Fatalf("parsed %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, now.Format("2006-01"), "shuttle.log")); err != nil {
		t.Fatal(err)
	}
	if got := Find(dir, now, now, "job", "nope", 10); len(got) != 0 {
		t.Fatal("found lines of another job")
	}
}
