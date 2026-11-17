package diskio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Created (v0.2.100): a file's birth time on macOS, zero where the system
// does not say; never a made-up date.
func TestCreated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	c := Created(fi)
	if runtime.GOOS != "darwin" {
		if !c.IsZero() {
			t.Fatalf("want no birth time here, got %v", c)
		}
		return
	}
	if d := time.Since(c); d < -time.Minute || d > time.Minute {
		t.Fatalf("birth time %v is not about now", c)
	}
}
