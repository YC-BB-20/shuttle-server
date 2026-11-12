package copyjob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

func init() {
	ChunkSize = 64 << 10
	CheckpointEvery = 256 << 10
	FlushEvery = 50 * time.Millisecond
}

func newManager(t *testing.T, state string) *Manager {
	m := &Manager{Dir: state, Budget: limits.NewBudget(8 << 20), Disks: diskio.NewRegistry(1, time.Minute),
		Accepting: func() bool { return true }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	return m
}

// makeTree writes files of awkward sizes, including empty and multi-chunk.
func makeTree(t *testing.T, dir string, big int) map[string][]byte {
	r := rand.New(rand.NewSource(7))
	files := map[string][]byte{}
	sizes := []int{0, 1, 63, 64 << 10, 64<<10 + 1, 3*64<<10 + 5, 1 << 20, big}
	for i, n := range sizes {
		b := make([]byte, n)
		r.Read(b)
		rel := fmt.Sprintf("card/CLIP%02d/frame %d.arx", i%3, i)
		files[rel] = b
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, time.Unix(1700000000+int64(i), 0), time.Unix(1700000000+int64(i), 0))
	}
	return files
}

func waitState(t *testing.T, m *Manager, id string, want ...string) View {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range m.Views() {
			if v.ID == id {
				for _, w := range want {
					if v.State == w {
						return v
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job never reached %v: %+v", want, m.Views())
	return View{}
}

func checkDest(t *testing.T, dst string, files map[string][]byte) {
	t.Helper()
	for rel, want := range files {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: wrong or missing (%v)", rel, err)
		}
	}
	filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
		if err == nil && strings.Contains(p, ".shuttle-") {
			t.Errorf("leftover temporary file %s", p)
		}
		return nil
	})
}

func TestCopyProvesEveryFileWithS2(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 2<<20+77)
	m := newManager(t, filepath.Join(root, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Verified != len(files) || v.Strong != len(files) || v.Failed != 0 || v.S2Warn != 0 {
		t.Fatalf("%+v", v)
	}
	if !v.SameVolume {
		t.Error("same volume not noticed")
	}
	checkDest(t, dst, files)
	fi, _ := os.Stat(filepath.Join(dst, "card/CLIP01/frame 1.arx"))
	if fi.ModTime().Unix() != 1700000001 {
		t.Error("modified time not kept")
	}
	// Source untouched.
	for rel, want := range files {
		got, _ := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if !bytes.Equal(got, want) {
			t.Fatal("source changed")
		}
	}
}

func TestExistingFilesAreNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 100)
	same, diff := "card/CLIP01/frame 1.arx", "card/CLIP02/frame 2.arx"
	for _, rel := range []string{same, diff} {
		os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755)
	}
	os.WriteFile(filepath.Join(dst, same), files[same], 0o644)
	os.WriteFile(filepath.Join(dst, diff), []byte("someone else's file"), 0o644)
	m := newManager(t, filepath.Join(root, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	j, _ := m.Add(Spec{Source: src, Dest: dst, S2: "skip"})
	v := waitState(t, m, j.st.ID, "finished")
	if v.Identical != 1 || v.Failed != 1 || v.Verified != len(files)-2 {
		t.Fatalf("%+v", v)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, diff)); string(b) != "someone else's file" {
		t.Fatal("existing file was overwritten")
	}
	p := m.Problems(j.st.ID, 10)
	if len(p) != 1 || p[0].Path != diff || !strings.Contains(p[0].Why, "left untouched") {
		t.Fatalf("problems %+v", p)
	}
}

func TestBadCopyIsRetriedThenKeptAside(t *testing.T) {
	for _, always := range []bool{false, true} {
		root := t.TempDir()
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		os.MkdirAll(src, 0o755)
		os.WriteFile(filepath.Join(src, "a.mov"), bytes.Repeat([]byte("x"), 300<<10), 0o644)
		m := newManager(t, filepath.Join(root, "state"))
		damaged := 0
		m.AfterCopy = func(tmp string) {
			if always || damaged == 0 {
				damaged++
				f, _ := os.OpenFile(tmp, os.O_RDWR, 0)
				f.WriteAt([]byte("y"), 1000)
				f.Close()
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		go m.Run(ctx)
		j, _ := m.Add(Spec{Source: src, Dest: dst, S2: "skip"})
		v := waitState(t, m, j.st.ID, "finished")
		cancel()
		_, errFinal := os.Stat(filepath.Join(dst, "a.mov"))
		_, errKept := os.Stat(filepath.Join(dst, "a.mov.shuttle-failed-copy"))
		if !always && (v.Verified != 1 || errFinal != nil) {
			t.Fatalf("one bad copy should be retried and pass: %+v", v)
		}
		if always && (v.Failed != 1 || errFinal == nil || errKept != nil) {
			t.Fatalf("a copy that never matches must fail, keep no final name, and be kept aside: %+v final=%v kept=%v", v, errFinal, errKept)
		}
	}
}

func TestPauseIsImmediateAndResumeCompletes(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 40<<20)
	m := newManager(t, filepath.Join(root, "state"))
	slow := diskio.NewRegistry(1, time.Minute)
	m.Disks = slow
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	j, _ := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
	// Wait until the big file is being worked on, then pause.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		v := waitState(t, m, j.st.ID, "running", "finished")
		if v.FileSize == 40<<20 && v.FileDone > 1<<20 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	m.Pause(j.st.ID)
	waitState(t, m, j.st.ID, "paused")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("pause took %v", d)
	}
	time.Sleep(100 * time.Millisecond)
	m.mu.Lock()
	busy := m.running != nil
	m.mu.Unlock()
	if busy {
		t.Fatal("still running after pause")
	}
	m.Resume(j.st.ID)
	v := waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
	checkDest(t, dst, files)
}

func TestStopKeepsProvenFilesAndRemovesPartial(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 40<<20)
	m := newManager(t, filepath.Join(root, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	j, _ := m.Add(Spec{Source: src, Dest: dst, S2: "skip"})
	for {
		v := waitState(t, m, j.st.ID, "running", "finished")
		if strings.HasPrefix(v.Stage, "Copying") && v.FileSize == 40<<20 && v.FileDone > 1<<20 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	m.Stop(j.st.ID)
	v := waitState(t, m, j.st.ID, "stopped")
	filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".shuttle-part") {
			t.Errorf("partial copy left: %s", p)
		}
		return nil
	})
	if v.Verified == 0 {
		t.Log("note: stop came before any file finished")
	}
}

// The crash test runs a job in a child process, kills it with SIGKILL in the
// middle, then lets a fresh manager carry on from the journal.
func TestKilledMidCopyResumesCorrectly(t *testing.T) {
	if os.Getenv("SHUTTLE_CRASH_CHILD") != "" {
		return
	}
	for _, mode := range []string{HashDuringCopy, HashFirst} {
		t.Run(mode, func(t *testing.T) { killedMidCopy(t, mode) })
	}
}

func killedMidCopy(t *testing.T, mode string) {
	root := t.TempDir()
	src, dst, state := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
	files := makeTree(t, src, 30<<20)
	for k := 0; k < 40; k++ { // plus many small files
		b := []byte(fmt.Sprintf("small file %d", k))
		rel := fmt.Sprintf("card/small/%03d.txt", k)
		files[rel] = b
		os.MkdirAll(filepath.Join(src, "card/small"), 0o755)
		os.WriteFile(filepath.Join(src, filepath.FromSlash(rel)), b, 0o644)
	}
	for round := 0; round < 3; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestCrashChild", "-test.v")
		cmd.Env = append(os.Environ(), "SHUTTLE_CRASH_CHILD=1", "HASH="+mode, "SRC="+src, "DST="+dst, "STATE="+state)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(400+round*700) * time.Millisecond)
		cmd.Process.Signal(syscall.SIGKILL)
		cmd.Wait()
	}
	m := newManager(t, state)
	if len(m.jobs) != 1 {
		t.Fatalf("jobs after crashes: %d", len(m.jobs))
	}
	j0 := m.jobs[0]
	t.Logf("after 3 kills: phase=%s verified=%d checkpoints=%d", j0.st.Phase, j0.c.Verified+j0.c.Identical, len(j0.st.Checkpoints))
	if j0.st.Phase == "done" {
		t.Fatal("the child finished before being killed; the test proves nothing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	v := waitState(t, m, m.jobs[0].st.ID, "finished")
	if v.Verified+v.Identical != len(files) || v.Failed != 0 || v.Strong != len(files) {
		t.Fatalf("after crashes: %+v", v)
	}
	checkDest(t, dst, files)
}

func TestCrashChild(t *testing.T) {
	if os.Getenv("SHUTTLE_CRASH_CHILD") == "" {
		t.Skip("helper for TestKilledMidCopyResumesCorrectly")
	}
	chunkDelay = 3 * time.Millisecond // slow enough to be killed mid-file
	m := newManager(t, os.Getenv("STATE"))
	if len(m.jobs) == 0 {
		if _, err := m.Add(Spec{Source: os.Getenv("SRC"), Dest: os.Getenv("DST"), S2: "now", Hash: os.Getenv("HASH")}); err != nil {
			t.Fatal(err)
		}
	}
	m.Run(context.Background()) // killed from outside
}

func TestAddRefusesNestedFolders(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	m := newManager(t, filepath.Join(root, "state"))
	if _, err := m.Add(Spec{Source: filepath.Join(root, "a"), Dest: filepath.Join(root, "a", "b", "c")}); err == nil {
		t.Fatal("destination inside source accepted")
	}
	if _, err := m.Add(Spec{Source: filepath.Join(root, "nope"), Dest: filepath.Join(root, "x")}); err == nil {
		t.Fatal("missing source accepted")
	}
}

// Hashing while copying reads the source twice (copy, S2) instead of three
// times (S1, copy, S2), and still ends with every file Strong.
func TestHashWhileCopyingReadsSourceTwice(t *testing.T) {
	for mode, reads := range map[string]int64{HashDuringCopy: 2, HashFirst: 3} {
		root := t.TempDir()
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		files := makeTree(t, src, 2<<20+77)
		var total int64
		for _, b := range files {
			total += int64(len(b))
		}
		m := newManager(t, filepath.Join(root, "state"))
		ctx, cancel := context.WithCancel(context.Background())
		go m.Run(ctx)
		j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now", Hash: mode})
		if err != nil {
			t.Fatal(err)
		}
		v := waitState(t, m, j.st.ID, "finished")
		cancel()
		if v.Hash != mode || v.Strong != len(files) || v.Verified != len(files) || v.Failed != 0 {
			t.Fatalf("%s: %+v", mode, v)
		}
		if got := j.srcBytes.Load(); got != reads*total {
			t.Fatalf("%s: source bytes read %d, want %d (%d reads)", mode, got, reads*total, reads)
		}
		checkDest(t, dst, files)
	}
}

// A bad read from the source during the copy makes the hash and the copy
// wrong in the same way; only the second source read (S2) shows it. The file
// must then be copied again, and never get its real name while in doubt.
func TestBadSourceReadDuringCopyIsCaught(t *testing.T) {
	for _, always := range []bool{false, true} {
		root := t.TempDir()
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		os.MkdirAll(src, 0o755)
		want := bytes.Repeat([]byte("x"), 300<<10)
		os.WriteFile(filepath.Join(src, "a.mov"), want, 0o644)
		m := newManager(t, filepath.Join(root, "state"))
		bad := 0
		m.CopyRead = func(b []byte) {
			if bad == 0 || always {
				bad++
				b[0] ^= 1 // what a faulty cable might do
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		go m.Run(ctx)
		j, _ := m.Add(Spec{Source: src, Dest: dst})
		v := waitState(t, m, j.st.ID, "finished")
		cancel()
		got, errFinal := os.ReadFile(filepath.Join(dst, "a.mov"))
		_, errKept := os.Stat(filepath.Join(dst, "a.mov.shuttle-failed-copy"))
		if !always && (v.Strong != 1 || errFinal != nil || !bytes.Equal(got, want)) {
			t.Fatalf("one bad read should be copied again and pass: %+v", v)
		}
		if always {
			p := m.Problems(j.st.ID, 10)
			if v.Failed != 1 || errFinal == nil || errKept != nil || len(p) != 1 || !strings.Contains(p[0].Why, "read differently") {
				t.Fatalf("a source that never reads the same must fail, keep no final name, be kept aside: %+v final=%v kept=%v problems=%+v", v, errFinal, errKept, p)
			}
		}
	}
}

func TestNewJobsHashWhileCopyingAndOldJobsKeepTheirScheme(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: filepath.Join(root, "src"), Dest: filepath.Join(root, "dst"), S2: "skip"})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.Hash != HashDuringCopy || j.st.Spec.S2 != "now" {
		t.Fatalf("new job: %+v (hashing while copying needs S2)", j.st.Spec)
	}
	if (Spec{S2: "now"}).hashDuringCopy() {
		t.Fatal("a job saved before 0.2.3 (no hash field) must keep the separate first read")
	}
}

// Stopping a paused copy must finish the stop (nothing is running to do it)
// and remove the partial copy, also when Shuttle was restarted in between.
func TestStopWhilePausedFinishes(t *testing.T) {
	for _, restart := range []bool{false, true} {
		root := t.TempDir()
		src, dst, state := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
		makeTree(t, src, 40<<20)
		m := newManager(t, state)
		ctx, cancel := context.WithCancel(context.Background())
		go m.Run(ctx)
		j, _ := m.Add(Spec{Source: src, Dest: dst})
		for {
			v := waitState(t, m, j.st.ID, "running", "finished")
			if strings.HasPrefix(v.Stage, "Copying") && v.FileSize == 40<<20 && v.FileDone > 1<<20 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		m.Pause(j.st.ID)
		waitState(t, m, j.st.ID, "paused")
		if restart {
			cancel()
			time.Sleep(50 * time.Millisecond)
			m = newManager(t, state)
			ctx, cancel = context.WithCancel(context.Background())
			go m.Run(ctx)
		}
		if err := m.Stop(j.st.ID); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, j.st.ID, "stopped")
		cancel()
		filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
			if err == nil && strings.HasSuffix(p, ".shuttle-part") {
				t.Errorf("restart=%v: partial copy left: %s", restart, p)
			}
			return nil
		})
	}
}
