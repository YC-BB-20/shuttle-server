package copyjob

import (
	"os"
	"path/filepath"
	"testing"
)

// v0.2.59: the queue names a job by what was chosen and shows each place as
// disk → path on it.
func TestPlaceOfAndChosenNames(t *testing.T) {
	home, _ := os.UserHomeDir()
	for p, want := range map[string][2]string{
		"/Volumes/BB - backups/TestDest/TESTSQ13": {"BB - backups", "/TestDest/TESTSQ13"},
		"/Volumes/Card/":                          {"Card", "/"},
		filepath.Join(home, "Desktop/TESTFOLDER"): {"This computer", "Desktop/TESTFOLDER"},
		"/opt/data":                               {"This computer", "/opt/data"},
	} {
		if d, r := placeOf(p); d != want[0] || r != want[1] {
			t.Errorf("%s: %q %q", p, d, r)
		}
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	files := makeTree(t, src, 10)
	_ = files
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	ents, _ := os.ReadDir(src)
	var dir string
	for _, e := range ents {
		if e.IsDir() {
			dir = e.Name()
			break
		}
	}
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(root, "d1"), Only: []string{dir, "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := baseView(j)
	if v.Only != 2 || v.OnlyDirs == nil || *v.OnlyDirs != 1 || v.OnlyName != "" || v.FromDisk != "This computer" {
		t.Fatalf("%+v", v)
	}
	j, _ = m.Add(Spec{Source: src, Dest: filepath.Join(root, "d2"), Only: []string{"a.txt"}})
	if v, _, _ := baseView(j); v.OnlyName != "a.txt" || *v.OnlyDirs != 0 {
		t.Fatalf("%+v", v)
	}
}
