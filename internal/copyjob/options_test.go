package copyjob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The New copy checkboxes (0.2.15): copy only; copy and verify the
// destination; that plus the source read again; each check either in the
// copy itself (per file or after all files) or as its own Verify item.

func TestCopyOnlyNamesFilesWithoutReadBack(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 2<<20)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.Hash != HashDuringCopy || j.st.Spec.Verify != "" {
		t.Fatalf("copy only: %+v", j.st.Spec)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Hashed != len(files) || v.Verified != 0 || v.Failed != 0 || v.Progress != 1 {
		t.Fatalf("%+v", v)
	}
	checkDest(t, dst, files)
	p, _ := m.Files(j.st.ID, "done", 0, 200)
	if p.Counts["done"] != len(files) || p.Counts["todo"] != 0 {
		t.Fatalf("counts %+v", p.Counts)
	}
	fi, _ := m.FileInfo(j.st.ID, 0)
	if !strings.HasPrefix(fi.Proof, "Not verified") || fi.Hash.C == "" || fi.Hash.D1 != "" {
		t.Fatalf("file info %+v", fi)
	}
	for _, x := range m.Views() {
		if x.Kind == KindVerify {
			t.Fatal("copy only must not queue a Verify job")
		}
	}
}

func TestDestinationOnlyCheckPerFileAndAtEnd(t *testing.T) {
	for _, mode := range []string{HashDuringCopy, HashCheckLater} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			files := makeTree(t, src, 1<<20)
			m := newManager(t, filepath.Join(root, "state"))
			startRun(t, m)
			j, err := m.Add(Spec{Source: src, Dest: dst, Hash: mode, S2: "skip"})
			if err != nil {
				t.Fatal(err)
			}
			v := waitState(t, m, j.st.ID, "finished")
			if v.Verified != len(files) || v.Strong != 0 || v.Failed != 0 || v.Progress != 1 {
				t.Fatalf("%+v", v)
			}
			checkDest(t, dst, files)
			fi, _ := m.FileInfo(j.st.ID, 0)
			if fi.Hash.D1 == "" || fi.Hash.S2 != "" {
				t.Fatalf("D1 only: %+v", fi.Hash)
			}
		})
	}
}

func copyThenVerifyItem(t *testing.T, s2 string, badRead bool) (*Manager, View, View, map[string][]byte) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	if badRead {
		m.CopyRead = func(b []byte) {
			if len(b) > 0 {
				b[0] ^= 1 // a faulty card or cable during the copy
			}
		}
	}
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Verify: "paused", S2: s2})
	if err != nil {
		t.Fatal(err)
	}
	cv := waitState(t, m, j.st.ID, "finished")
	for k := 0; k < 500 && cv.VerifyJob == ""; k++ { // recorded just after the copy finishes
		time.Sleep(10 * time.Millisecond)
		cv = waitState(t, m, j.st.ID, "finished")
	}
	if cv.Hashed != len(files) || cv.VerifyJob != j.st.ID+verifyIDSuffix {
		t.Fatalf("copy: %+v", cv)
	}
	if _, err := os.Stat(filepath.Join(dst, "card/CLIP00/frame 0.arx")); err != nil {
		t.Fatal("copied files must have their real names before the Verify item runs")
	}
	vid := cv.VerifyJob
	waitState(t, m, vid, "paused")
	if err := m.Resume(vid); err != nil {
		t.Fatal(err)
	}
	return m, cv, waitState(t, m, vid, "finished"), files
}

func TestSeparateVerifyItemReadsCopyAndSourceAgain(t *testing.T) {
	_, _, v, files := copyThenVerifyItem(t, "now", false)
	if v.Kind != KindVerify || v.Verified != len(files) || v.Strong != len(files) || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestSeparateVerifyItemDestinationOnly(t *testing.T) {
	_, _, v, files := copyThenVerifyItem(t, "skip", false)
	if v.Verified != len(files) || v.Strong != 0 || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
}

// A bad read during the copy goes into both the copy and its hash, so only
// reading the source again shows it. The Verify item reports it and changes
// nothing.
func TestSeparateVerifyItemCatchesBadCopyRead(t *testing.T) {
	m, _, v, files := copyThenVerifyItem(t, "now", true)
	nonEmpty := 0
	for _, b := range files {
		if len(b) > 0 {
			nonEmpty++
		}
	}
	if v.Failed != nonEmpty || v.Strong != len(files)-nonEmpty {
		t.Fatalf("%+v", v)
	}
	p := m.Problems(v.ID, 50)
	if len(p) != nonEmpty || !strings.Contains(p[0].Why, "bad read") {
		t.Fatalf("%+v", p)
	}
}

func TestVerifyItemQueuedOnceAcrossRestart(t *testing.T) {
	root := t.TempDir()
	src, dst, st := filepath.Join(root, "src"), filepath.Join(root, "dst"), filepath.Join(root, "state")
	makeTree(t, src, 1<<10)
	m := newManager(t, st)
	j, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Verify: "run", S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	// As if it finished and Shuttle stopped before the Verify item was saved.
	j.st.Phase = "done"
	j.save()
	for round := 0; round < 2; round++ {
		m2 := newManager(t, st)
		n := 0
		for _, x := range m2.jobs {
			if x.st.Spec.isVerify() {
				n++
				if x.st.Choice != "run" || !x.st.Spec.readsSource() || x.st.Spec.Source != src {
					t.Fatalf("verify job %+v", x.st)
				}
			}
		}
		if n != 1 {
			t.Fatalf("round %d: %d verify jobs", round, n)
		}
	}
}

func TestProgressCopyOnly(t *testing.T) {
	st := state{Phase: "copy", Bytes: 1000, Files: 2, Spec: Spec{Hash: HashDuringCopy, S2: "now", Check: "none"}}
	if p := progress(st, counts{BytesCopied: 400}, live{stage: "Copying (the source is hashed as it is read)", size: 600, done: 100}); p != 0.5 {
		t.Fatal(p)
	}
}

// The separate Verify item is in the queue from the moment the copy is
// added, right under it, and waits for the copy rather than running beside
// it; stopping the copy stops its Verify item too.
func TestVerifyItemShownFromStartAndWaitsForCopy(t *testing.T) {
	for _, choice := range []string{"run", "paused"} {
		root := t.TempDir()
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		makeTree(t, src, 1<<10)
		m := newManager(t, filepath.Join(root, "state"))
		j, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Verify: choice, S2: "now"})
		if err != nil {
			t.Fatal(err)
		}
		vs := m.Views()
		if len(vs) != 2 {
			t.Fatalf("%s: want the copy and its Verify item, got %d jobs", choice, len(vs))
		}
		var fv View
		for _, v := range vs {
			if v.Kind == KindVerify {
				fv = v
			}
		}
		want := map[string]string{"run": "queued", "paused": "paused"}[choice]
		if fv.ID != j.st.ID+verifyIDSuffix || fv.After != j.st.ID || fv.State != want || !fv.Created.After(j.st.Created) {
			t.Fatalf("%s: verify item %+v", choice, fv)
		}
		m.Resume(fv.ID)
		if p := m.pick(); p != j {
			t.Fatalf("%s: the Verify item must wait for its copy", choice)
		}
		m.Stop(j.st.ID)
		f := m.find(fv.ID)
		if f.st.Choice != "stopped" || f.st.Phase != "done" {
			t.Fatalf("%s: after the copy was stopped the Verify item is %s/%s", choice, f.st.Choice, f.st.Phase)
		}
		if p := m.pick(); p != nil {
			t.Fatalf("%s: nothing should run, got %s", choice, p.st.ID)
		}
	}
}

// "Read the source again" works in a Verify item that reads the source, and is
// refused in one that checks the copy only.
func TestVerifyItemRecheckSource(t *testing.T) {
	m, _, v, _ := copyThenVerifyItem(t, "now", false)
	if err := m.AskRecheck(v.ID, 0, "source"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		fi, err := m.FileInfo(v.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(fi.Rechecks) == 1 && len(fi.RecheckNow) == 0 {
			r := fi.Rechecks[0]
			if r.Which != "source" || r.Error != "" || r.Changed || r.Hash == "" || r.Hash != fi.Hash.S2 {
				t.Fatalf("recheck %+v, hashes %+v", r, fi.Hash)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recheck not done: %+v %+v", fi.Rechecks, fi.RecheckNow)
		}
		time.Sleep(50 * time.Millisecond)
	}
	m2, _, v2, _ := copyThenVerifyItem(t, "skip", false)
	if m2.AskRecheck(v2.ID, 0, "source") == nil {
		t.Fatal("source recheck accepted in a copy-only Verify item")
	}
}
