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

// startRun runs the manager until the test ends, and waits for it to stop
// before the test's folders are removed (a job may still be saving).
func startRun(t *testing.T, m *Manager) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func waitState(t *testing.T, m *Manager, id string, want ...string) View {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range m.Views() {
			if v.ID == id {
				for _, w := range want {
					// "asking": finished with failed files, waiting for the owner (v0.2.99)
					if v.State == w || w == "asking" && v.Errors != nil {
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
	v := waitState(t, m, j.st.ID, "asking")
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
	// File info says the identical file was compared, not copied.
	for i := 0; i < len(files); i++ {
		if fi, err := m.FileInfo(j.st.ID, i); err == nil && fi.Path == same && (!fi.Already || fi.Hash.S1 == "" || fi.Hash.C != "") {
			t.Fatalf("already-there file info %+v", fi)
		} else if err == nil && fi.Path != same && fi.Already {
			t.Fatalf("%s marked as already there", fi.Path)
		}
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
		v := waitState(t, m, j.st.ID, "finished", "asking")
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
	// The work itself must stop within the same two seconds (a slow CI disk
	// needs a moment to save each read's place).
	for busy := true; busy; {
		m.mu.Lock()
		busy = m.running != nil
		m.mu.Unlock()
		if busy && time.Since(start) > 2*time.Second {
			t.Fatal("still running two seconds after pause")
		}
		time.Sleep(5 * time.Millisecond)
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
	for _, mode := range []string{HashDuringCopy, HashFirst, HashCheckLater} {
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
		// Copy speed counts the copy pass alone: every byte written once, and
		// only the copying time, with no copy pass left running.
		if cb, cns := j.copyClock(time.Now()); cb != total || cns <= 0 || j.copySince.Load() != 0 {
			t.Fatalf("%s: copy clock %d bytes, %d ns, want %d bytes", mode, cb, cns, total)
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
		startRun(t, m)
		j, _ := m.Add(Spec{Source: src, Dest: dst})
		v := waitState(t, m, j.st.ID, "finished", "asking")
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
	if j.st.Spec.Hash != HashDuringCopy || j.st.Spec.S2 != "skip" {
		t.Fatalf("new job: %+v (since 0.2.15 the source re-read can be left out)", j.st.Spec)
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

func TestChosenFilesOnlyAndFileListAndHistory(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 100)
	os.WriteFile(filepath.Join(src, "card", "a.mov"), []byte("aaaa"), 0o644)
	os.WriteFile(filepath.Join(src, "card", "b.mov"), []byte("bb"), 0o644)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	if _, err := m.Add(Spec{Source: filepath.Join(src, "card"), Dest: dst, Only: []string{"../x"}}); err == nil {
		t.Fatal("a name with a path in it was accepted")
	}
	if _, err := m.Add(Spec{Source: filepath.Join(src, "card"), Dest: dst, Only: []string{"missing.mov"}}); err == nil {
		t.Fatal("a missing name was accepted")
	}
	j, err := m.Add(Spec{Source: filepath.Join(src, "card"), Dest: dst, Only: []string{"a.mov", "CLIP01", "a.mov"}})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "finished")
	want := 1
	for rel := range files {
		if strings.HasPrefix(rel, "card/CLIP01/") {
			want++
		}
	}
	if v.Files != want || v.Strong != want || v.Only != 2 {
		t.Fatalf("want %d files, got %+v", want, v)
	}
	if _, err := os.Stat(filepath.Join(dst, "b.mov")); err == nil {
		t.Fatal("a file that was not chosen was copied")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a.mov")); string(b) != "aaaa" {
		t.Fatal("a.mov not copied")
	}
	// File list, paged, and filtered.
	p, err := m.Files(j.st.ID, "all", 0, 2)
	if err != nil || len(p.Rows) != 2 || p.Next < 0 || p.Rows[0].Status != "strong" {
		t.Fatalf("%+v %v", p, err)
	}
	p2, _ := m.Files(j.st.ID, "all", p.Next, 200)
	if len(p.Rows)+len(p2.Rows) != want || p2.Next != -1 {
		t.Fatalf("pages do not add up: %+v", p2)
	}
	if p3, _ := m.Files(j.st.ID, "problems", 0, 200); len(p3.Rows) != 0 {
		t.Fatalf("%+v", p3)
	}
	if h := m.History(0, 20); len(h) != 1 || h[0].ID != j.st.ID || h[0].State != "finished" {
		t.Fatalf("%+v", h)
	}
	if h := m.History(1, 20); len(h) != 0 {
		t.Fatalf("%+v", h)
	}
	// File info: hashes, story, paths.
	fi, err := m.FileInfo(j.st.ID, p.Rows[0].I)
	if err != nil || fi.Hash.C == "" || fi.Hash.C != fi.Hash.D1 || fi.Hash.D1 != fi.Hash.S2 || !fi.DestExists ||
		fi.DestSize != fi.Size || len(fi.Events) != 1 || fi.Events[0].Attempts != 1 || !strings.HasPrefix(fi.Proof, "Strong") ||
		fi.Dest != filepath.Join(dst, filepath.FromSlash(fi.Path)) {
		t.Fatalf("%+v %v", fi, err)
	}
	if _, err := m.FileInfo(j.st.ID, 9999); err == nil {
		t.Fatal("a file that is not in the copy was found")
	}
	if v.Progress != 1 {
		t.Fatalf("finished but progress %v", v.Progress)
	}
}

func TestProgressCountsEveryPass(t *testing.T) {
	st := state{Phase: "copy", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashDuringCopy, S2: "now"}}
	// Half of file 1 (size 400) copied: 100 of 1000 bytes of work.
	if p := progress(st, counts{}, live{stage: "Copying (the source is hashed as it is read)", size: 400, done: 200}); p != 0.1 {
		t.Fatal(p)
	}
	// File 1 proven, file 2 (600) being read back half way: 400 + 300 + 150.
	if p := progress(st, counts{BytesProven: 400}, live{stage: "Reading back the destination and the source", size: 600, done: 300}); p != 0.85 {
		t.Fatal(p)
	}
	old := state{Phase: "s2", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashFirst, S2: "now"}}
	if p := progress(old, counts{BytesProven: 1000, Verified: 2, Strong: 1}, live{}); p != 0.875 {
		t.Fatal(p)
	}
}

// Copy everything first, then check: every file is copied (hashed as it is
// read) before any read-back; each copy gets its real name as soon as it is
// written (v0.2.16), before its check; a bad copy is copied again in the
// check pass.
func TestCopyThenCheck(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 2<<20)
	m := newManager(t, filepath.Join(root, "state"))
	var copies, unnamed int
	damaged := false
	m.AfterCopy = func(tmp string) {
		copies++
		if copies > len(files) {
			return // the redone copy in the check pass
		}
		// Only the file being copied has a temporary name; earlier copies,
		// though not yet checked, already have their real names.
		filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.Contains(p, ".shuttle-") && p != tmp {
				unnamed++
			}
			return nil
		})
		if !damaged && strings.Contains(tmp, "frame 6") {
			damaged = true
			f, _ := os.OpenFile(tmp, os.O_RDWR, 0)
			f.WriteAt([]byte("y"), 10)
			f.Close()
		}
	}
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.Failed != 0 || v.Copied != 0 || v.Progress != 1 {
		t.Fatalf("%+v", v)
	}
	if unnamed != 0 {
		t.Fatalf("%d copies kept a temporary name after their copy", unnamed)
	}
	if copies != len(files)+1 {
		t.Fatalf("want %d copies (one redone), got %d", len(files)+1, copies)
	}
	checkDest(t, dst, files)
	p, _ := m.Files(j.st.ID, "done", 0, 200)
	if p.Counts["all"] != len(files) || p.Counts["done"] != len(files) || p.Counts["todo"] != 0 || p.Counts["problems"] != 0 {
		t.Fatalf("counts %+v", p.Counts)
	}
}

func TestProgressCopyThenCheck(t *testing.T) {
	st := state{Phase: "copy", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashCheckLater, S2: "now"}}
	if p := progress(st, counts{}, live{stage: "Copying (the source is hashed as it is read)", size: 400, done: 200}); p != 0.1 {
		t.Fatal(p)
	}
	// Both copied (half the work), file 1 being checked half way.
	st.Phase = "check"
	if p := progress(st, counts{BytesCopied: 1000}, live{stage: "Reading back the destination and the source", size: 400, done: 200}); p != 0.6 {
		t.Fatal(p)
	}
}

// Stopping before the check keeps the copies under their real names (they
// show as "Copied · not verified"); only partial copies are removed.
func TestStopCopyThenCheckKeepsNamedCopies(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: dst, Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	// Run only the scan and the copy pass, as if stopped before the check.
	sd, _ := m.Disks.For(src)
	dd, _ := m.Disks.For(dst)
	if err := j.scan(context.Background(), j.source()); err != nil {
		t.Fatal(err)
	}
	jr, _, _ := OpenJournal(j.path("journal.log"), func(Rec) {})
	if err := j.copyPhase(context.Background(), env{budget: m.Budget, src: sd, dst: dd}, jr, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	jr.Close()
	count := func() (named, parts int) {
		filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				if strings.Contains(p, ".shuttle-") {
					parts++
				} else {
					named++
				}
			}
			return nil
		})
		return
	}
	named, parts := count()
	if parts != 0 || named == 0 || j.c.Copied != named {
		t.Fatalf("copy pass: %d named, %d temporary, counted %d copied", named, parts, j.c.Copied)
	}
	m.finishStopped(j)
	if n, p := count(); n != named || p != 0 {
		t.Fatalf("after Stop: %d named (want %d), %d temporary", n, named, p)
	}
}

func TestMissingDiskIsNamed(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1)
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	if w := m.missing(j); w != "" {
		t.Fatalf("both disks are here, got %q", w)
	}
	os.Rename(src, src+"-gone") // as if the card was pulled
	if w := m.missing(j); w != "source" {
		t.Fatalf("want source, got %q", w)
	}
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "waiting")
	if v.Missing != "source" || !strings.HasPrefix(v.Reason, "Source folder not found: "+src) ||
		MissingReason("source", "/Volumes/CARD_A001/DCIM", "/x") != "Source disk not connected: CARD_A001. Once it is back, the copy continues by itself; nothing verified is lost." {
		t.Fatalf("%+v", v)
	}
	os.Rename(src+"-gone", src)
	if v := waitState(t, m, j.st.ID, "finished"); v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
	if n := diskName("/Volumes/CARD_A001/DCIM"); n != "CARD_A001" {
		t.Fatal(n)
	}
	if n := diskName("/media/bb/BACKUP/x"); n != "BACKUP" {
		t.Fatal(n)
	}
}

// A source pulled in the middle of a copy makes the copy wait with a clear
// message; no file is marked failed, and it finishes once it is back.
func TestSourcePulledMidCopyWaitsNotFails(t *testing.T) {
	for _, mode := range []string{HashDuringCopy, HashCheckLater, HashFirst} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			files := makeTree(t, src, 2<<20)
			m := newManager(t, filepath.Join(root, "state"))
			pulled := false
			m.CopyRead = func([]byte) {
				if !pulled {
					pulled = true
					os.Rename(src, src+"-pulled")
				}
			}
			startRun(t, m)
			j, err := m.Add(Spec{Source: src, Dest: dst, Hash: mode, S2: "now"})
			if err != nil {
				t.Fatal(err)
			}
			v := waitState(t, m, j.st.ID, "waiting", "finished")
			if v.State != "waiting" || v.Missing != "source" || v.Failed != 0 {
				p, _ := m.Files(j.st.ID, "problems", 0, 10)
				t.Fatalf("%+v %+v", v, p.Rows)
			}
			os.Rename(src+"-pulled", src)
			if v := waitState(t, m, j.st.ID, "finished"); v.Failed != 0 || v.Verified+v.Identical != len(files) {
				t.Fatalf("%+v", v)
			}
			checkDest(t, dst, files)
		})
	}
}

// A file being copied is visible under a name that is clearly not done, and a
// partial copy under the old hidden name is picked up after an update.
func TestPartialCopyIsVisibleAndOldNameIsPickedUp(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "clip.mov")
	if got := filepath.Base(tempName(final)); got != "clip.mov.shuttle-part" {
		t.Fatalf("temp name %q", got)
	}
	os.WriteFile(oldTempName(final), []byte("partial"), 0o600)
	tmp := tempFor(final)
	if b, err := os.ReadFile(tmp); err != nil || string(b) != "partial" {
		t.Fatalf("old partial not moved: %q %v", b, err)
	}
	if _, err := os.Lstat(oldTempName(final)); err == nil {
		t.Fatal("old hidden partial left behind")
	}
}

// "Read again" in file info reads the source or the copy in full, after the
// copy and never beside one, and records a fingerprint equal to the first.
func TestRecheckReadsAgainAndRecords(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1<<20+5)
	m := newManager(t, filepath.Join(root, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")
	for _, w := range []string{"source", "dest"} {
		if err := m.AskRecheck(j.st.ID, 0, w); err != nil {
			t.Fatal(err)
		}
	}
	if m.AskRecheck(j.st.ID, 0, "elsewhere") == nil {
		t.Fatal("unknown side accepted")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		fi, err := m.FileInfo(j.st.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(fi.Rechecks) == 2 && len(fi.RecheckNow) == 0 {
			for _, r := range fi.Rechecks {
				if r.Hash != fi.Hash.C || r.Error != "" || r.Changed {
					t.Fatalf("recheck %+v, first %s", r, fi.Hash.C)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rechecks not done: %+v %+v", fi.Rechecks, fi.RecheckNow)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// v0.2.30: a finished entry can be hidden from the history and shown again;
// the flag survives a restart, an unfinished job cannot be hidden, and the
// copied files are untouched.
func TestHideHistoryEntry(t *testing.T) {
	root := t.TempDir()
	src, dst, state := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
	makeTree(t, src, 0)
	m := newManager(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	j, _ := m.Add(Spec{Source: src, Dest: dst})
	id := j.st.ID
	if err := m.SetHidden(id, true); err == nil {
		t.Error("an unfinished job was hidden")
	}
	go m.Run(ctx)
	waitState(t, m, id, "finished")
	if err := m.SetHidden(id, true); err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	m = newManager(t, state)
	if h := m.History(0, 20); len(h) != 1 || !h[0].Hidden {
		t.Fatalf("hidden flag lost after restart: %+v", h)
	}
	if err := m.SetHidden(id, false); err != nil {
		t.Fatal(err)
	}
	if h := m.History(0, 20); h[0].Hidden {
		t.Error("entry still hidden after showing it again")
	}
	if n, _ := os.ReadDir(dst); len(n) == 0 {
		t.Error("destination emptied")
	}
}

// The copy speed is the average over all the copying the run has done, shown
// once there has been enough of it to measure, and kept while the job reads
// back or compares (the copy clock only moves while copying).
func TestCopyAverage(t *testing.T) {
	if got := copyAverage(1<<30, int64(time.Second)); got != 0 {
		t.Fatalf("under the measuring time: %d, want 0", got)
	}
	if got := copyAverage(0, int64(10*time.Second)); got != 0 {
		t.Fatalf("nothing copied: %d, want 0", got)
	}
	if got := copyAverage(3400_000_000, int64(10*time.Second)); got != 340_000_000 {
		t.Fatalf("average %d, want 340000000", got)
	}
}

// v0.2.54: a copy's bar has two layers, written and proven, each growing with
// the file being worked on.
func TestLayersWrittenAndProven(t *testing.T) {
	st := state{Phase: "copy", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashDuringCopy, S2: "now"}}
	// File 1 (400) half copied: written 200, nothing proven.
	if c, p := layers(st, counts{}, live{stage: "Copying (the source is hashed as it is read)", size: 400, done: 200}); c != 0.2 || p != 0 {
		t.Fatal(c, p)
	}
	// File 1 written, being read back a quarter: written 400, proven 100.
	if c, p := layers(st, counts{}, live{stage: "Reading back the destination and the source", size: 400, done: 100}); c != 0.4 || p != 0.1 {
		t.Fatal(c, p)
	}
	// File 1 proven, file 2 (600) half copied: written 700, proven 400.
	if c, p := layers(st, counts{BytesProven: 400}, live{stage: "Copying", size: 600, done: 300}); c != 0.7 || p != 0.4 {
		t.Fatal(c, p)
	}
	// Copy then check: both files written first, the check pass grows only proven.
	cl := state{Phase: "check", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashCheckLater, S2: "now"}}
	if c, p := layers(cl, counts{BytesCopied: 1000}, live{stage: "Reading back the destination and the source", size: 400, done: 200}); c != 1 || p != 0.2 {
		t.Fatal(c, p)
	}
	// Copy only: never a proven layer.
	co := state{Phase: "copy", Bytes: 1000, Files: 1, Spec: Spec{Hash: HashDuringCopy, Check: "none"}}
	if c, p := layers(co, counts{BytesCopied: 500}, live{}); c != 0.5 || p != 0 {
		t.Fatal(c, p)
	}
	// A Verify job has no layers: its bar is its progress, in pink.
	if c, p := layers(state{Phase: "verify", Bytes: 1000, Spec: Spec{Kind: KindVerify}}, counts{}, live{}); c != 0 || p != 0 {
		t.Fatal(c, p)
	}
}
