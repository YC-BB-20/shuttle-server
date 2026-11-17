package copyjob

import (
	"path/filepath"
	"testing"
)

// v0.2.89: a folder of a card copied to two drives shows both copies and each
// file's state, whether it is browsed on the card or on one of the drives.
func TestCopiesOfFolder(t *testing.T) {
	src, dir, state := t.TempDir(), t.TempDir(), t.TempDir()
	files := makeTree(t, src, 300<<10)
	m := newManager(t, state)
	startRun(t, m)
	var ids []string
	for _, d := range []string{"B1", "B2"} {
		j, err := m.Add(Spec{Source: filepath.Join(src, "card"), Dest: filepath.Join(dir, d, "card"), S2: "now"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.st.ID)
	}
	for _, id := range ids {
		waitState(t, m, id, "finished")
	}
	n := 0
	for p := range files {
		if filepath.Dir(p) == "card/CLIP01" {
			n++
		}
	}
	for _, browse := range []string{filepath.Join(src, "card", "CLIP01"), filepath.Join(dir, "B2", "card", "CLIP01")} {
		c := m.CopiesOf(browse)
		if len(c.Places) != 2 || c.Source != filepath.Join(src, "card", "CLIP01") {
			t.Fatalf("%s: places %+v source %q", browse, c.Places, c.Source)
		}
		if len(c.Files) != n {
			t.Fatalf("%s: %d files, want %d", browse, len(c.Files), n)
		}
		for name, f := range c.Files {
			for k, s := range f.Status {
				if s != "strong" || f.I[k] < 0 {
					t.Fatalf("%s: %s in place %d: %q", browse, name, k, s)
				}
			}
		}
	}
	top := m.CopiesOf(filepath.Join(src, "card"))
	if f, ok := top.Folders["CLIP01"]; !ok || f.Files[0] != n || f.Strong[1] != n {
		t.Fatalf("folder counts %+v", top.Folders)
	}
	// The card's parent: the card folder with its copies, one place per disk.
	up := m.CopiesOf(src)
	if f, ok := up.Folders["card"]; !ok || len(up.Places) != 1 || f.Strong[0] != len(files) {
		t.Fatalf("parent: places %+v folders %+v", up.Places, up.Folders)
	}
	if c := m.CopiesOf(t.TempDir()); len(c.Places) != 0 {
		t.Fatal("a folder never copied has copies")
	}
}
