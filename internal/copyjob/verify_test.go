package copyjob

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyJobFolderAndFile(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 2<<20+77)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)

	// Nothing copied there yet: refused in plain words.
	os.MkdirAll(dst, 0o755)
	if _, err := m.Add(Spec{Kind: KindVerify, Source: dst}); err == nil || !strings.Contains(err.Error(), "no stored hashes") {
		t.Fatalf("want refusal, got %v", err)
	}
	c, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, c.st.ID, "finished")

	// The whole destination matches.
	v1, err := m.Add(Spec{Kind: KindVerify, Source: dst})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, v1.st.ID, "finished")
	if v.Kind != KindVerify || v.Files != len(files) || v.Verified != len(files) || v.Failed != 0 || v.Progress != 1 {
		t.Fatalf("%+v", v)
	}

	// Damage one copy, remove another, shorten a third; check one subfolder.
	bad := filepath.Join(dst, "card/CLIP01/frame 4.arx") // 64 KiB + 1
	b, _ := os.ReadFile(bad)
	b[100] ^= 1
	os.WriteFile(bad, b, 0o644)
	os.Remove(filepath.Join(dst, "card/CLIP01/frame 1.arx"))
	os.WriteFile(filepath.Join(dst, "card/CLIP01/frame 7.arx"), []byte("short"), 0o644)
	v2, err := m.Add(Spec{Kind: KindVerify, Source: filepath.Join(dst, "card/CLIP01")})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, v2.st.ID, "finished")
	if v.Files != 3 || v.Failed != 3 || v.Verified != 0 {
		t.Fatalf("%+v", v)
	}
	why := map[string]string{}
	for _, p := range m.Problems(v2.st.ID, 10) {
		why[p.Path] = p.Why
	}
	if !strings.HasPrefix(why["frame 4.arx"], "differs") || !strings.HasPrefix(why["frame 1.arx"], "missing") || !strings.HasPrefix(why["frame 7.arx"], "wrong size") {
		t.Fatalf("reasons: %v", why)
	}
	// Nothing was changed or removed by the check.
	if got, _ := os.ReadFile(bad); !bytes.Equal(got, b) {
		t.Fatal("verify changed a file")
	}

	// One file on its own.
	v3, err := m.Add(Spec{Kind: KindVerify, Source: filepath.Join(dst, "card/CLIP00/frame 0.arx")})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, v3.st.ID, "finished")
	if v.Files != 1 || v.Verified != 1 || v.VerifyFile != "frame 0.arx" {
		t.Fatalf("%+v", v)
	}
	fi, err := m.FileInfo(v3.st.ID, 0)
	if err != nil || fi.Kind != KindVerify || fi.Hash.C == "" || fi.Hash.C != fi.Hash.D1 {
		t.Fatalf("%+v %v", fi, err)
	}

	// A second copy into the same place: each file is checked once.
	if c2, err := m.Add(Spec{Source: src, Dest: dst}); err != nil {
		t.Fatal(err)
	} else {
		waitState(t, m, c2.st.ID, "finished")
	}
	v4, _ := m.Add(Spec{Kind: KindVerify, Source: filepath.Join(dst, "card/CLIP02")})
	v = waitState(t, m, v4.st.ID, "finished")
	if v.Files != 2 || v.Verified != 2 {
		t.Fatalf("duplicates or misses: %+v", v)
	}
}

// v0.2.26: "Verify copy" of a copy-only job, destinations first, then the
// source read again after the whole job; added paused.
func TestVerifySourceAfter(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 2<<20+77)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	c, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, c.st.ID, "finished")
	p, _ := m.Files(c.st.ID, "done", 0, 200)
	if p.Counts["done"] != 0 || p.Counts["unverified"] != len(files) {
		t.Fatalf("copy only counts as verified: %+v", p.Counts)
	}
	vj, err := m.Add(Spec{Kind: KindVerify, Source: dst, S2: S2After, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	if vj.st.Spec.Source != src || vj.st.Spec.S2 != S2After || vj.st.Choice != "paused" {
		t.Fatalf("spec %+v choice %s", vj.st.Spec, vj.st.Choice)
	}
	waitState(t, m, vj.st.ID, "paused")
	m.Resume(vj.st.ID)
	v := waitState(t, m, vj.st.ID, "finished")
	if v.Verified != len(files) || v.Strong != len(files) || v.Failed != 0 || v.Error != "" || v.Progress != 1 {
		t.Fatalf("%+v", v)
	}
	// A subfolder maps to the same subfolder of the source.
	sub, err := m.Add(Spec{Kind: KindVerify, Source: filepath.Join(dst, "card"), S2: S2After})
	if err != nil {
		t.Fatal(err)
	}
	if sub.st.Spec.Source != filepath.Join(src, "card") {
		t.Fatalf("source %s", sub.st.Spec.Source)
	}
	waitState(t, m, sub.st.ID, "finished")
}

// v0.2.26: a Verify job whose folder is missing fails at once, naming it,
// instead of waiting like a copy.
func TestVerifyMissingFolderFails(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	c, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, c.st.ID, "finished")
	for _, tc := range []struct{ gone, want string }{{src, "Source folder missing: " + src}, {dst, "Destination folder missing: " + dst}} {
		vj, err := m.Add(Spec{Kind: KindVerify, Source: dst, S2: S2After, Paused: true})
		if err != nil {
			t.Fatal(err)
		}
		os.Rename(tc.gone, tc.gone+".away")
		m.Resume(vj.st.ID)
		v := waitState(t, m, vj.st.ID, "finished")
		if !strings.HasPrefix(v.Error, tc.want) {
			t.Fatalf("error %q, want %q", v.Error, tc.want)
		}
		os.Rename(tc.gone+".away", tc.gone)
	}
	// Nothing was changed: a plain verify still matches.
	vj, _ := m.Add(Spec{Kind: KindVerify, Source: dst})
	if v := waitState(t, m, vj.st.ID, "finished"); v.Error != "" || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
}

// v0.2.27: a copy's Checks say what was read back since, and a source-only
// Verify reads only the source.
func TestCopyChecksAndSourceOnly(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	c, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, c.st.ID, "finished")
	checks := func() *Checks {
		for _, x := range m.Views() {
			if x.ID == c.st.ID {
				return x.Checks
			}
		}
		return nil
	}
	if k := checks(); k == nil || k.Of != v.Files || k.Dest != 0 || k.Source != 0 {
		t.Fatalf("copy only: %+v", k)
	}
	so, err := m.Add(Spec{Kind: KindVerify, Source: dst, S2: S2Only})
	if err != nil {
		t.Fatal(err)
	}
	sv := waitState(t, m, so.st.ID, "finished")
	if sv.Strong != len(files) || sv.Verified != 0 || sv.Failed != 0 || sv.Progress != 1 {
		t.Fatalf("source only: %+v", sv)
	}
	if k := checks(); k.Dest != 0 || k.Source != len(files) || k.SourceAt.IsZero() {
		t.Fatalf("after source only: %+v", k)
	}
	fi, _ := m.FileInfo(so.st.ID, 0)
	if fi.Status != "source" || fi.Hash.D1 != "" {
		t.Fatalf("file info %+v", fi)
	}
	d, _ := m.Add(Spec{Kind: KindVerify, Source: dst})
	waitState(t, m, d.st.ID, "finished")
	if k := checks(); k.Dest != len(files) || k.Source != len(files) || k.Queued {
		t.Fatalf("after both: %+v", k)
	}
}

// v0.2.28: two copies from different sources into one folder; a Verify asked
// from one copy's row reads only that copy and its own source.
func TestVerifyFromOneCopyOfSeveral(t *testing.T) {
	root := t.TempDir()
	a, b, dst := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "dst")
	fa := makeTree(t, a, 1<<20)
	os.MkdirAll(filepath.Join(b, "other"), 0o755)
	os.WriteFile(filepath.Join(b, "other", "x.mov"), []byte("another source"), 0o644)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	ca, _ := m.Add(Spec{Source: a, Dest: dst, Check: "none", Hash: HashCheckLater})
	waitState(t, m, ca.st.ID, "finished")
	cb, _ := m.Add(Spec{Source: b, Dest: dst, Check: "none", Hash: HashCheckLater})
	waitState(t, m, cb.st.ID, "finished")
	if _, err := m.Add(Spec{Kind: KindVerify, Source: dst, S2: S2After}); err == nil || !strings.Contains(err.Error(), "different sources") {
		t.Fatalf("whole folder: want refusal, got %v", err)
	}
	vj, err := m.Add(Spec{Kind: KindVerify, Source: dst, S2: S2After, From: []string{ca.st.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if vj.st.Spec.Source != a || len(vj.st.Spec.From) != 1 {
		t.Fatalf("spec %+v", vj.st.Spec)
	}
	v := waitState(t, m, vj.st.ID, "finished")
	if v.Files != len(fa) || v.Strong != len(fa) || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
}
