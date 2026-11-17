package copyjob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// v0.2.99 (owner, 17 November 2026): a copy that ends with failed files
// waits for an answer. Only that job waits: the next copy still runs, and
// the copy's own Verify item waits with it until the owner answers. The
// question survives a restart; "leave them failed" releases the Verify item.
func TestFailedFilesAskOnlyThatJobWaits(t *testing.T) {
	root := t.TempDir()
	src, dst, st := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
	makeTree(t, src, 100<<10)
	inWay := "card/CLIP02/frame 2.arx"
	os.MkdirAll(filepath.Dir(filepath.Join(dst, inWay)), 0o755)
	os.WriteFile(filepath.Join(dst, inWay), []byte("someone else's file"), 0o644)
	src2, dst2 := filepath.Join(root, "src2"), filepath.Join(root, "dst2")
	files2 := makeTree(t, src2, 10<<10)

	m := newManager(t, st)
	bad, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Verify: "run", S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	good, err := m.Add(Spec{Source: src2, Dest: dst2, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	vid := bad.st.VerifyJob
	if vid == "" {
		t.Fatal("no Verify item queued with the copy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	v := waitState(t, m, bad.st.ID, "asking")
	if v.State != "paused" || v.Errors.Failed != 1 || v.Errors.Same != 1 || v.Errors.Reason == "" {
		t.Fatalf("the question: %+v %+v", v, v.Errors)
	}
	waitState(t, m, good.st.ID, "finished") // the rest of the queue carries on
	checkDest(t, dst2, files2)
	if vv := waitState(t, m, vid, "queued"); vv.After == "" {
		t.Fatalf("the Verify item should wait for the answer: %+v", vv)
	}
	for _, h := range m.History(0, 50) {
		if h.ID == bad.st.ID {
			t.Fatal("a copy waiting for an answer is in the history")
		}
	}
	cancel()
	<-done

	// Restart: still asking, the Verify item still waiting.
	m2 := newManager(t, st)
	if j := m2.find(bad.st.ID); j == nil || j.st.ErrorsAsk == nil {
		t.Fatal("the question was lost in a restart")
	}
	if err := m2.KeepFailed(good.st.ID); !errors.Is(err, errNoQuestion) {
		t.Fatalf("a copy without failures answered: %v", err)
	}
	if err := m2.KeepFailed(bad.st.ID); err != nil {
		t.Fatal(err)
	}
	startRun(t, m2)
	if v := waitState(t, m2, bad.st.ID, "finished"); v.Errors != nil || v.Failed != 1 {
		t.Fatalf("left failed: %+v", v)
	}
	waitState(t, m2, vid, "finished", "asking")
	if b, _ := os.ReadFile(filepath.Join(dst, inWay)); string(b) != "someone else's file" {
		t.Fatal("the file in the way was touched")
	}
}

func TestReasonKeyDropsThePath(t *testing.T) {
	for in, want := range map[string]string{
		"open /Volumes/A/card/x.mov: permission denied": "permission denied",
		"a file with this name is already there":        "a file with this name is already there",
		"too big for FAT32: 5.0 GB":                     "too big for FAT32: 5.0 GB",
	} {
		if got := reasonKey(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
