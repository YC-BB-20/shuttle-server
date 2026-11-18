package copyjob

import (
	"os"
	"path/filepath"
	"testing"
)

// After a Move in Files, earlier copies' records follow it, so Files still
// shows the moved folder's copies (v0.2.116).
func TestMovedRecordsFollow(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "Card"), filepath.Join(root, "T5", "Backups")
	files := makeTree(t, src, 0)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")
	if c := m.CopiesOf(src); len(c.Places) != 1 {
		t.Fatalf("before: %d places", len(c.Places))
	}

	// The source folder moves into Archive.
	os.MkdirAll(filepath.Join(root, "Archive"), 0o755)
	if err := os.Rename(src, filepath.Join(root, "Archive", "Card")); err != nil {
		t.Fatal(err)
	}
	if up, part := m.Moved(root, []string{"Card"}, filepath.Join(root, "Archive")); up != 1 || part != 0 {
		t.Fatalf("updated %d partial %d", up, part)
	}
	moved := filepath.Join(root, "Archive", "Card")
	c := m.CopiesOf(moved)
	if len(c.Places) != 1 || c.Source != moved || len(c.Files)+len(c.Folders) == 0 {
		t.Fatalf("after the source moved: %+v", c)
	}
	if c := m.CopiesOf(src); len(c.Places) != 0 {
		t.Fatal("the old place still shows copies")
	}

	// The backup folder moves too: the copy's destination follows.
	os.MkdirAll(filepath.Join(root, "T5", "Old"), 0o755)
	os.Rename(dst, filepath.Join(root, "T5", "Old", "Backups"))
	if up, _ := m.Moved(filepath.Join(root, "T5"), []string{"Backups"}, filepath.Join(root, "T5", "Old")); up != 1 {
		t.Fatalf("dest: updated %d", up)
	}
	if c := m.CopiesOf(moved); len(c.Places) != 1 || c.Places[0].Dest != filepath.Join(root, "T5", "Old", "Backups") {
		t.Fatalf("dest not followed: %+v", c.Places)
	}
	saved, err := loadJob(j.dir)
	if err != nil || saved.st.Spec.Source != moved {
		t.Fatalf("not saved: %v %q", err, saved.st.Spec.Source)
	}
	_ = files

	// A copy of chosen items follows only when all of them moved.
	pick := filepath.Join(root, "Pick")
	os.MkdirAll(filepath.Join(pick, "A"), 0o755)
	os.WriteFile(filepath.Join(pick, "A", "a.mov"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(pick, "b.mov"), []byte("b"), 0o644)
	k, err := m.Add(Spec{Source: pick, Only: []string{"A", "b.mov"}, Dest: filepath.Join(root, "Out")})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, k.st.ID, "finished")
	if up, part := m.Moved(pick, []string{"A"}, filepath.Join(root, "Elsewhere")); up != 0 || part != 1 {
		t.Fatalf("part of a choice: updated %d partial %d", up, part)
	}
	if up, _ := m.Moved(pick, []string{"A", "b.mov"}, filepath.Join(root, "Elsewhere")); up != 1 {
		t.Fatalf("whole choice: updated %d", up)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.st.Spec.Source != filepath.Join(root, "Elsewhere") {
		t.Fatalf("choice source %q", k.st.Spec.Source)
	}
}
