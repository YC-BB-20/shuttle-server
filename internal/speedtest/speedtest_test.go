package speedtest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

func newTester(t *testing.T) *Tester {
	sizeUnit = 4 << 20
	return &Tester{Budget: limits.NewBudget(64 << 20), Disks: diskio.NewRegistry(1, time.Minute),
		StateFile: filepath.Join(t.TempDir(), "speedtests.json")}
}

func waitRounds(t *testing.T, tt *Tester, n int) Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := tt.Status()
		if !st.Running || st.Round > n {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("test never got going")
	return Status{}
}

// A write test writes and reads its own file round after round, keeps the
// result, and removes the file when stopped.
func TestWriteAndReadThenStop(t *testing.T) {
	tt := newTester(t)
	dir := t.TempDir()
	if err := tt.Start(dir, 3, false); err == nil {
		t.Error("odd size accepted")
	}
	if err := tt.Start(dir, 2, false); err != nil {
		t.Fatal(err)
	}
	if err := tt.Start(dir, 2, false); err == nil {
		t.Error("second test at once accepted")
	}
	st := waitRounds(t, tt, 2)
	tt.Stop()
	for tt.Status().Running {
		time.Sleep(5 * time.Millisecond)
	}
	st = tt.Status()
	if st.Write <= 0 || st.Read <= 0 || st.Ended != "Stopped by you." || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, TestFile)); !os.IsNotExist(err) {
		t.Error("test file left behind")
	}
	tt2 := &Tester{StateFile: tt.StateFile}
	if r := tt2.Results(); len(r) != 1 || r[0].Read <= 0 || r[0].Write <= 0 {
		t.Fatalf("results not kept: %+v", r)
	}
}

// A camera card is only read: files already there, nothing written.
func TestCameraCardReadOnly(t *testing.T) {
	tt := newTester(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "DCIM"), 0o755)
	if err := tt.Start(dir, 1, false); err != nil {
		t.Fatal(err)
	}
	for tt.Status().Running {
		time.Sleep(5 * time.Millisecond)
	}
	if st := tt.Status(); !st.ReadOnly || st.Error != "" || st.Ended == "" {
		t.Fatalf("empty card: %+v", st)
	}
	big := make([]byte, 70<<20)
	os.WriteFile(filepath.Join(dir, "DCIM", "clip.mov"), big, 0o644)
	before, _ := os.ReadDir(dir)
	if err := tt.Start(dir, 1, false); err != nil {
		t.Fatal(err)
	}
	waitRounds(t, tt, 1)
	tt.Stop()
	for tt.Status().Running {
		time.Sleep(5 * time.Millisecond)
	}
	st := tt.Status()
	if !st.ReadOnly || st.Read <= 0 || st.Write != 0 {
		t.Fatalf("%+v", st)
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Error("something was written to the card")
	}
}

// A busy disk is refused.
func TestBusyDiskRefused(t *testing.T) {
	tt := newTester(t)
	tt.Busy = func(string) string { return "a copy is running" }
	if err := tt.Start(t.TempDir(), 1, false); err == nil {
		t.Error("busy disk accepted")
	}
}
