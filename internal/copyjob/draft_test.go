package copyjob

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestRelistDue(t *testing.T) {
	now := time.Now()
	st := state{Phase: "copy", ListedAt: now.Add(-2 * time.Hour)}
	hour := time.Hour
	if !relistDue(st, hour, now) {
		t.Fatal("waited 2 h, limit 1 h: list again")
	}
	if relistDue(st, 0, now) {
		t.Fatal("never means never")
	}
	if relistDue(st, 3*hour, now) {
		t.Fatal("waited less than the limit")
	}
	worked := st
	worked.WorkNs = 1
	if relistDue(worked, hour, now) {
		t.Fatal("a copy that has worked is not listed again")
	}
	if relistDue(state{Phase: "copy"}, hour, now) {
		t.Fatal("a copy that listed its own folder has nothing to expire")
	}
}

func TestRelistSetting(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "state"))
	if m.Settings().RelistMinutes != 60 {
		t.Fatal("default is one hour")
	}
	s := m.Settings()
	s.RelistMinutes = 99999999
	got, err := m.SetSettings(s)
	if err != nil || got.RelistMinutes != 7*24*60 {
		t.Fatalf("not clamped: %d %v", got.RelistMinutes, err)
	}
	s.RelistMinutes = 0
	if got, _ := m.SetSettings(s); got.RelistMinutes != 0 {
		t.Fatal("never must stay never")
	}
}
