package copyjob

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v0.2.92: a finished copy's failed files go back in the same job and are
// copied with the same proof; nothing proven is touched, a restart keeps
// the result, and a file in the way is never overwritten.
func TestRetryFailedCopiesOnlyThem(t *testing.T) {
	for _, mode := range []string{HashDuringCopy, HashCheckLater} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			src, dst, state := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
			files := makeTree(t, src, 300<<10)
			inWay := "card/CLIP02/frame 2.arx"
			os.MkdirAll(filepath.Dir(filepath.Join(dst, inWay)), 0o755)
			os.WriteFile(filepath.Join(dst, inWay), []byte("someone else's file"), 0o644)
			m := newManager(t, state)
			startRun(t, m)
			j, err := m.Add(Spec{Source: src, Dest: dst, Hash: mode, S2: "now"})
			if err != nil {
				t.Fatal(err)
			}
			id := j.st.ID
			v := waitState(t, m, id, "asking")
			if v.Failed != 1 || v.State != "paused" || v.Errors.Failed != 1 || v.Errors.Same != 1 || v.Errors.Reason == "" {
				t.Fatalf("want one failure, and the question: %+v %+v", v, v.Errors)
			}
			if _, err := m.RetryFailed(id, -1); err != nil {
				t.Fatalf("retry: %v", err) // still in the way: fails again, untouched
			}
			v = waitState(t, m, id, "asking") // failed again: asks again
			if b, _ := os.ReadFile(filepath.Join(dst, inWay)); string(b) != "someone else's file" || v.Failed != 1 {
				t.Fatalf("file in the way overwritten, or failure lost: %+v", v)
			}
			if p := m.Problems(id, 10); len(p) != 1 || p[0].Path != inWay {
				t.Fatalf("one line per failed file: %+v", p)
			}
			// The owner moves it away; the retry now copies it.
			os.Rename(filepath.Join(dst, inWay), filepath.Join(root, "moved"))
			if n, err := m.RetryFailed(id, 99); err == nil || n != 0 {
				t.Fatal("a file that did not fail was put back")
			}
			if n, err := m.RetryFailed(id, -1); err != nil || n != 1 {
				t.Fatalf("retry: %d %v", n, err)
			}
			v = waitState(t, m, id, "finished")
			if v.Failed != 0 || v.Verified != len(files) || v.Strong != len(files) {
				t.Fatalf("after retry: %+v", v)
			}
			checkDest(t, dst, files)
			if p := m.Problems(id, 10); len(p) != 0 {
				t.Fatalf("a retried file is still a problem: %+v", p)
			}
			if _, err := m.RetryFailed(id, -1); !errors.Is(err, errNotRetryable) {
				t.Fatalf("nothing left to retry: %v", err)
			}
			// File history keeps the failure and the retry.
			for i := 0; i < len(files); i++ {
				fi, err := m.FileInfo(id, i)
				if err != nil || fi.Path != inWay {
					continue
				}
				var whats []string
				for _, e := range fi.Events {
					whats = append(whats, e.What)
				}
				all := strings.Join(whats, " | ")
				if fi.Why != "" || !strings.Contains(all, "Failed") || !strings.Contains(all, "tried again") {
					t.Fatalf("file story %q, why %q", all, fi.Why)
				}
			}
			// A restart reads the same result from the journal.
			m2 := newManager(t, state)
			for _, v2 := range m2.Views() {
				if v2.ID == id && (v2.Failed != 0 || v2.Verified != len(files) || v2.State != "finished") {
					t.Fatalf("after restart: %+v", v2)
				}
			}
		})
	}
}

func TestRetryRefusesUnfinishedAndVerify(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	makeTree(t, src, 100)
	m := newManager(t, filepath.Join(root, "state")) // not running: stays queued
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(root, "dst")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetryFailed(j.st.ID, -1); !errors.Is(err, errNotRetryable) {
		t.Fatalf("a queued copy was retried: %v", err)
	}
}
