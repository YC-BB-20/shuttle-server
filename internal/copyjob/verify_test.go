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
