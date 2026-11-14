package copyjob

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// v0.2.55: buffers are sized to the file and to a share of the budget.
func TestBufPlan(t *testing.T) {
	old := ChunkSize
	ChunkSize = 16 << 20
	defer func() { ChunkSize = old }()
	cases := []struct {
		size, share int64
		want        int
		per         int64
		n           int
	}{
		{10 << 30, 64 << 20, copyBufs, 16 << 20, 4}, // big clip, 128 MB budget
		{10 << 30, 32 << 20, copyBufs, 16 << 20, 2}, // 64 MB budget
		{20 << 10, 64 << 20, copyBufs, 64 << 10, 2}, // small file: small buffers
		{20 << 20, 64 << 20, copyBufs, 16 << 20, 2}, // 20 MB needs two pieces
		{0, 64 << 20, readBufs, 64 << 10, 2},
	}
	for _, c := range cases {
		per, n := bufPlan(c.size, c.want, c.share)
		if per != c.per || n != c.n {
			t.Errorf("bufPlan(%d, %d, %d) = %d×%d, want %d×%d", c.size, c.want, c.share, n, per, c.n, c.per)
		}
	}
	// A tiny budget still gets buffers that fit, aligned.
	for _, share := range []int64{8 << 20, 1 << 20, 200 << 10} {
		per, n := bufPlan(1<<30, copyBufs, share)
		if n < 1 || int64(n)*per > share || per%bufAlign != 0 {
			t.Errorf("share %d: %d×%d", share, n, per)
		}
	}
}

// The read-back reads one buffer while hashing another; its hash, and the
// checkpoint left by a pause in the middle, must cover exactly the bytes in
// order, and every buffer must go back to the budget.
func TestReadBackPipelinedPauseResume(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	os.MkdirAll(src, 0o755)
	data := make([]byte, 1<<20+5)
	rand.New(rand.NewSource(3)).Read(data)
	p := filepath.Join(src, "clip.mov")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	sd, _ := m.Disks.For(src)
	e := env{budget: m.Budget, src: sd, dst: sd}
	want := xxh3.Sum128(data)

	got, err := j.hashFile(context.Background(), e, sd, p, 0, "d1", true)
	if err != nil || got != want {
		t.Fatalf("straight read: %v %v", got, err)
	}

	chunkDelay = 3 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err = j.hashFile(ctx, e, sd, p, 0, "s2", true)
	cancel()
	chunkDelay = 0
	if err == nil {
		t.Fatal("expected the pause to stop the read")
	}
	c, ok := j.checkpoint(0, "s2")
	if !ok || c.Offset <= 0 || c.Offset >= int64(len(data)) || c.Offset%ChunkSize != 0 {
		t.Fatalf("checkpoint after pause: %+v %v", c, ok)
	}
	got, err = j.hashFile(context.Background(), e, sd, p, 0, "s2", true)
	if err != nil || got != want {
		t.Fatalf("resumed from %d: %v %v", c.Offset, got, err)
	}

	// Nothing kept: the whole budget can be taken again at once.
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	l, err := m.Budget.Acquire(ctx, m.Budget.Total())
	if err != nil {
		t.Fatal("budget not fully returned:", err)
	}
	m.Budget.Release(l)
}

// v0.2.55: a finished copy keeps its average copying speed for history.
func TestCopyRateKeptAfterFinish(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	j.copyBytes.Store(500e6)
	j.copyNs.Store(int64(2 * time.Second))
	j.mu.Lock()
	j.st.CopyRate = copyAverage(j.copyClock(time.Now()))
	j.mu.Unlock()
	if v, _, _ := baseView(j); v.CopyRate != 250e6 {
		t.Fatalf("history copy rate %d", v.CopyRate)
	}
}

// v0.2.56: a finished job keeps the time it spent running, saved with it,
// and time between runs (paused, queued) is not counted.
func TestWorkTimeKeptAfterFinish(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	j.st.WorkNs = int64(90 * time.Second) // an earlier run, before a pause
	j.mu.Unlock()
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "finished")
	if v.WorkMs < 90_000 || v.WorkMs > 90_000+30_000 {
		t.Fatalf("work time %d ms", v.WorkMs)
	}
	b, err := os.ReadFile(j.path("job.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil || st.WorkNs <= int64(90*time.Second) {
		t.Fatalf("saved work time %d (%v)", st.WorkNs, err)
	}
	time.Sleep(20 * time.Millisecond)
	if v2, _, _ := baseView(j); v2.WorkMs != st.WorkNs/1e6 {
		t.Fatalf("clock still running after finish: %d vs %d", v2.WorkMs, st.WorkNs/1e6)
	}
}
