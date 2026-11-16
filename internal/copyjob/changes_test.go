package copyjob

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/location"
)

func TestCopyOnlyCopiesItsListAndReportsWhatMoved(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 300<<10)
	m := newManager(t, filepath.Join(root, "state"))
	d, err := WriteDraft(context.Background(), location.Folder{Dir: src, System: true}, filepath.Join(root, "draft"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// After the list was made: one file deleted, one changed, one added.
	gone, changed := "card/CLIP00/frame 3.arx", "card/CLIP01/frame 1.arx"
	if err := os.Remove(filepath.Join(src, filepath.FromSlash(gone))); err != nil {
		t.Fatal(err)
	}
	newBody := append(append([]byte{}, files[changed]...), "more"...)
	if err := os.WriteFile(filepath.Join(src, filepath.FromSlash(changed)), newBody, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(filepath.Join(src, filepath.FromSlash(changed)), time.Unix(1800000000, 0), time.Unix(1800000000, 0))
	if err := os.WriteFile(filepath.Join(src, "card", "added.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := m.AddDraft(Spec{Source: src, Dest: dst, S2: "now"}, d)
	if err != nil {
		t.Fatal(err)
	}
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "finished")
	if v.Gone != 1 || v.Changed != 1 || v.Added != 1 || v.Failed != 0 {
		t.Fatalf("counts: gone %d changed %d added %d failed %d", v.Gone, v.Changed, v.Added, v.Failed)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, filepath.FromSlash(changed))); string(got) != string(newBody) {
		t.Fatal("a changed file is copied as it is now")
	}
	if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(gone))); err == nil {
		t.Fatal("a deleted file appeared at the destination")
	}
	if _, err := os.Stat(filepath.Join(dst, "card", "added.txt")); err == nil {
		t.Fatal("a file added after the list was copied by itself")
	}
	if v.S2Warn != 0 {
		t.Fatalf("a file changed before its turn is not a source warning: %d", v.S2Warn)
	}
	kinds := map[string]string{}
	for _, p := range m.Problems(j.st.ID, 50) {
		kinds[p.Path] = p.Kind
	}
	if kinds[gone] != "gone" || kinds[changed] != "changed" {
		t.Fatalf("problems: %v", kinds)
	}
	rows, next, _ := m.AddedList(j.st.ID, 0, 10)
	if len(rows) != 1 || rows[0].Path != "card/added.txt" || next != -1 {
		t.Fatalf("added list: %v", rows)
	}

	// "Copy these too" makes a separate copy of the added file only.
	id, err := m.CopyAdded(j.st.ID)
	if err != nil {
		t.Fatal(err)
	}
	v2 := waitState(t, m, id, "finished")
	if v2.Files != 1 || v2.Failed != 0 {
		t.Fatalf("copy of added: %+v", v2)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "card", "added.txt")); string(got) != "new" {
		t.Fatal("the added file was not copied by Copy these too")
	}

	// "Check source again" finds a later addition.
	os.WriteFile(filepath.Join(src, "card", "later.txt"), []byte("x"), 0o644)
	if err := m.CheckSourceAgain(j.st.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		vs := waitState(t, m, j.st.ID, "finished")
		if vs.Added == 2 && !vs.Checking {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the second source check did not find the later file")
}

func TestDeletedAndChangedFilesInEveryScheme(t *testing.T) {
	for name, spec := range map[string]Spec{"first": {Hash: HashFirst, S2: "now"}, "later": {Hash: HashCheckLater, S2: "now"}, "copy only": {Check: "none"}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			files := makeTree(t, src, 300<<10)
			m := newManager(t, filepath.Join(root, "state"))
			d, _ := WriteDraft(context.Background(), location.Folder{Dir: src, System: true}, filepath.Join(root, "draft"), nil)
			os.Remove(filepath.Join(src, "card", "CLIP00", "frame 3.arx"))
			changed := filepath.Join(src, "card", "CLIP01", "frame 4.arx")
			os.WriteFile(changed, append(files["card/CLIP01/frame 4.arx"], 'x'), 0o644)
			spec.Source, spec.Dest = src, dst
			j, err := m.AddDraft(spec, d)
			if err != nil {
				t.Fatal(err)
			}
			startRun(t, m)
			v := waitState(t, m, j.st.ID, "finished")
			if v.Gone != 1 || v.Changed != 1 || v.Failed != 0 {
				t.Fatalf("gone %d changed %d failed %d", v.Gone, v.Changed, v.Failed)
			}
		})
	}
}
