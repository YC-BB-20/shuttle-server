package safemode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThreeQuickCrashesMeanSafeMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "startup.json")
	now := time.Now()
	for i := 0; i < MaxCrashes; i++ {
		tr, safe, _, err := Open(p, now.Add(time.Duration(i)*time.Second))
		if err != nil || safe {
			t.Fatalf("start %d: safe=%v err=%v", i, safe, err)
		}
		if i == MaxCrashes-1 {
			tr.RecordError("boom", now)
		}
	}
	_, safe, reason, _ := Open(p, now.Add(10*time.Second))
	if !safe || !strings.Contains(reason, "boom") {
		t.Fatalf("safe=%v reason=%q", safe, reason)
	}
	// Still safe on the next start: safe mode does not clear itself.
	if _, safe, _, _ = Open(p, now.Add(20*time.Second)); !safe {
		t.Fatal("safe mode cleared itself")
	}
	Reset(p)
	if _, safe, _, _ = Open(p, now.Add(30*time.Second)); safe {
		t.Fatal("reset did not clear safe mode")
	}
}

func TestStableAndOldCrashesDoNotCount(t *testing.T) {
	p := filepath.Join(t.TempDir(), "startup.json")
	now := time.Now()
	for i := 0; i < 5; i++ {
		tr, safe, _, _ := Open(p, now.Add(time.Duration(i)*time.Second))
		if safe {
			t.Fatal("stable runs counted as crashes")
		}
		tr.Stable()
	}
	Open(p, now)
	Open(p, now.Add(time.Second))
	if _, safe, _, _ := Open(p, now.Add(Window+2*time.Second)); safe {
		t.Fatal("crashes older than the window counted")
	}
}

func TestDamagedRecordSetAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "startup.json")
	os.WriteFile(p, []byte("garbage"), 0o600)
	tr, safe, _, err := Open(p, time.Now())
	if err != nil || safe || tr.Note == "" {
		t.Fatalf("safe=%v err=%v note=%q", safe, err, tr.Note)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("damaged file not kept: %v", ents)
	}
}
