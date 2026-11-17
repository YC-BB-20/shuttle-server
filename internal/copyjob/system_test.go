package copyjob

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSystemFiles adds the files macOS and Windows make for themselves.
func writeSystemFiles(t *testing.T, src string) {
	for rel, b := range map[string]string{
		".DS_Store": "finder", "card/.DS_Store": "finder", "card/._frame 0.arx": "appledouble",
		".Spotlight-V100/Store-V2/x": "index", "card/Thumbs.DB": "thumbs",
	} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestSystemFilesLeftOutBySetting(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	files := makeTree(t, src, 1<<20)
	writeSystemFiles(t, src)
	m := newManager(t, filepath.Join(root, "state", "jobs"))
	startRun(t, m)
	if s := m.Settings(); !s.SkipSystem || s.SkipAppleDouble {
		t.Fatalf("defaults: %+v", s)
	}

	// Default: system files left out and counted, "._" files copied.
	d1 := filepath.Join(root, "d1")
	j, err := m.Add(Spec{Source: src, Dest: d1, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Failed != 0 || v.System != 4 || v.Files != len(files)+1 || v.Skipped != 0 {
		t.Fatalf("%+v", v)
	}
	checkDest(t, d1, files)
	for _, rel := range []string{".DS_Store", "card/.DS_Store", ".Spotlight-V100", "card/Thumbs.DB"} {
		if exists(filepath.Join(d1, rel)) {
			t.Errorf("%s was copied", rel)
		}
	}
	if !exists(filepath.Join(d1, "card/._frame 0.arx")) {
		t.Error(`"._" file left out without its switch`)
	}

	// Both switches on, saved and read back after a restart.
	if _, err := m.SetSettings(Settings{SkipSystem: true, SkipAppleDouble: true}); err != nil {
		t.Fatal(err)
	}
	m2 := &Manager{Dir: m.Dir}
	if s := m2.Settings(); !s.SkipSystem || !s.SkipAppleDouble || s.SafeCopies != 2 {
		t.Fatalf("not kept: %+v", s)
	}
	// v0.2.91: the On set copy count: 2 by default, 1 to 4, kept.
	if s, _ := m.SetSettings(Settings{SkipSystem: true, SafeCopies: 3}); s.SafeCopies != 3 {
		t.Fatalf("safe count %d", s.SafeCopies)
	}
	if s := (&Manager{Dir: m.Dir}).Settings(); s.SafeCopies != 3 {
		t.Fatalf("safe count not kept: %d", s.SafeCopies)
	}
	if s, _ := m.SetSettings(Settings{SkipSystem: true, SkipAppleDouble: true, SafeCopies: 9}); s.SafeCopies != 2 {
		t.Fatalf("safe count 9 kept as %d", s.SafeCopies)
	}
	d2 := filepath.Join(root, "d2")
	j, _ = m.Add(Spec{Source: src, Dest: d2, S2: "now"})
	v = waitState(t, m, j.st.ID, "finished")
	if v.Failed != 0 || v.System != 5 || v.Files != len(files) || exists(filepath.Join(d2, "card/._frame 0.arx")) {
		t.Fatalf("%+v", v)
	}

	// Off: everything is copied, as before 0.2.22.
	m.SetSettings(Settings{})
	d3 := filepath.Join(root, "d3")
	j, _ = m.Add(Spec{Source: src, Dest: d3, S2: "now"})
	v = waitState(t, m, j.st.ID, "finished")
	if v.Failed != 0 || v.System != 0 || v.Files != len(files)+5 || !exists(filepath.Join(d3, ".DS_Store")) {
		t.Fatalf("%+v", v)
	}

	// Finder rewrites a copied .DS_Store: a Verify with the switch on
	// leaves it out instead of reporting it as different.
	os.WriteFile(filepath.Join(d3, "card/.DS_Store"), []byte("finder changed this"), 0o644)
	m.SetSettings(Settings{SkipSystem: true})
	vj, err := m.Add(Spec{Kind: KindVerify, Source: d3})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, vj.st.ID, "finished")
	if v.Failed != 0 || v.System != 4 || v.Verified != len(files)+1 {
		t.Fatalf("%+v", v)
	}
}
