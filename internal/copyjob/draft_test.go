package copyjob

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/location"
)

func TestAdoptDraftSizesAtAddAndCopies(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 300<<10)
	var want int64
	for _, b := range files {
		want += int64(len(b))
	}
	m := newManager(t, filepath.Join(root, "state"))
	set := m.Settings()
	d, err := WriteDraft(context.Background(), location.Folder{Dir: src, System: set.SkipSystem, AppleDouble: set.SkipAppleDouble}, filepath.Join(root, "draft"), nil)
	if err != nil || d.Files != int64(len(files)) || d.Bytes != want {
		t.Fatalf("draft %+v %v (want %d files, %d bytes)", d, err, len(files), want)
	}
	j, err := m.AddDraft(Spec{Source: src, Dest: dst}, d)
	if err != nil {
		t.Fatal(err)
	}
	// Before it runs, the copy is already sized and its list is in place.
	var v View
	for _, x := range m.Views() {
		if x.ID == j.st.ID {
			v = x
		}
	}
	if v.Phase != "copy" || v.Bytes != want || v.Files != len(files) {
		t.Fatalf("not sized at add: phase %q bytes %d files %d", v.Phase, v.Bytes, v.Files)
	}
	if _, err := os.Stat(j.path("manifest.jsonl")); err != nil {
		t.Fatal("manifest not adopted")
	}
	os.RemoveAll(filepath.Join(root, "draft")) // the job keeps its own link
	startRun(t, m)
	waitState(t, m, j.st.ID, "finished")
	checkDest(t, dst, files)
}

func TestDraftForAnotherCopyIsIgnored(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 1000)
	m := newManager(t, filepath.Join(root, "state"))
	d, err := WriteDraft(context.Background(), location.Folder{Dir: src, System: false}, filepath.Join(root, "draft"), nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := m.AddDraft(Spec{Source: src, Dest: dst}, d) // Settings skip system files, the draft did not
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Phase != "scan" || !j.st.ListedAt.IsZero() {
		t.Fatalf("a draft made under other rules was used: %q", j.st.Phase)
	}
}
