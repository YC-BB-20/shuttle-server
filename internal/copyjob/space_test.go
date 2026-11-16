package copyjob

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func treeSize(files map[string][]byte) int64 {
	var n int64
	for _, b := range files {
		n += int64(len(b))
	}
	return n
}

func TestSpaceFitsAddsTheCopy(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	m := newManager(t, t.TempDir())
	m.FreeHook = func(string) uint64 { return uint64(treeSize(files)) }
	if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "a")}); err != nil {
		t.Fatalf("a copy that just fits was refused: %v", err)
	}
}

func TestSpaceTooBigOnItsOwnAsksAndContinueAnywayAdds(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	m := newManager(t, t.TempDir())
	m.FreeHook = func(string) uint64 { return uint64(treeSize(files)) - 1 }
	_, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "a")})
	var se *SpaceError
	if !errors.As(err, &se) || se.Short.FitsAlone || se.Short.Need != treeSize(files) || se.Short.QueuedJobs != 0 {
		t.Fatalf("want a space question, not fitting alone: %v %+v", err, se)
	}
	if len(m.Views()) != 0 {
		t.Fatal("a copy that does not fit was added before the owner answered")
	}
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "a"), IgnoreSpace: true})
	if err != nil {
		t.Fatal(err)
	}
	if !j.st.SpaceOK || j.st.Spec.IgnoreSpace {
		t.Fatalf("continue anyway not kept on the job: %+v", j.st)
	}
}

func TestSpaceCountsCopiesQueuedToTheSameDisk(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	size := treeSize(files)
	m := newManager(t, t.TempDir()) // not running: the copies stay queued
	m.FreeHook = func(string) uint64 { return uint64(size * 5 / 2) }
	for _, d := range []string{"a", "b"} {
		if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, d)}); err != nil {
			t.Fatalf("copy %s: %v", d, err)
		}
	}
	_, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "c")})
	var se *SpaceError
	if !errors.As(err, &se) || !se.Short.FitsAlone || se.Short.QueuedJobs != 2 || se.Short.Queued != 2*size {
		t.Fatalf("want: fits alone, not with the 2 queued copies: %v %+v", err, se)
	}
	// A stopped copy no longer counts.
	v := m.Views()
	if err := m.Stop(v[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "c")}); err != nil {
		t.Fatalf("with one queued copy stopped it fits: %v", err)
	}
}

func TestSpaceLeavesOutFilesAlreadyThere(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	big := "card/CLIP01/frame 7.arx"
	p := filepath.Join(dst, "a", filepath.FromSlash(big))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, files[big], 0o644)
	m := newManager(t, t.TempDir())
	m.FreeHook = func(string) uint64 { return uint64(treeSize(files) - int64(len(files[big]))) }
	if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "a")}); err != nil {
		t.Fatalf("a file already at the destination was counted: %v", err)
	}
}

func TestSpaceCheckedAgainWhenTheCopyStarts(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	m := newManager(t, t.TempDir())
	var free atomic.Int64
	free.Store(1 << 40)
	m.FreeHook = func(string) uint64 { return uint64(free.Load()) }
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, "a")})
	if err != nil {
		t.Fatal(err)
	}
	free.Store(1000) // the disk filled up while it waited
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "paused")
	if v.Space == nil || v.Space.Message == "" {
		t.Fatalf("paused without the space question: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(dst, "a", "card")); err == nil {
		t.Fatal("something was copied before the owner answered")
	}
	if err := m.ContinueAnyway(j.st.ID); err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, j.st.ID, "finished")
	if v.Space != nil || v.Failed != 0 {
		t.Fatalf("after continue anyway: %+v", v)
	}
	checkDest(t, filepath.Join(dst, "a"), files)
}

func TestCheckSpaceWarnsWithoutAddingAnything(t *testing.T) {
	dst := t.TempDir()
	m := newManager(t, t.TempDir())
	m.FreeHook = func(string) uint64 { return 1000 }
	short, err := m.CheckSpace(filepath.Join(dst, "new", "folder"), 1001)
	if err != nil || short == nil || short.FitsAlone || short.Need != 1001 || short.Free != 1000 {
		t.Fatalf("want a warning for a copy bigger than the free space: %v %+v", err, short)
	}
	if short, err = m.CheckSpace(dst, 1000); err != nil || short != nil {
		t.Fatalf("a copy that just fits must not warn: %v %+v", err, short)
	}
	if len(m.Views()) != 0 {
		t.Fatal("checking the space added a job")
	}
}

func TestCheckSpaceCountsTheWholeQueueToTheSameDisk(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := makeTree(t, src, 1<<20)
	size := treeSize(files)
	m := newManager(t, t.TempDir()) // not running: the copies stay queued
	m.FreeHook = func(string) uint64 { return uint64(size * 5 / 2) }
	for _, d := range []string{"a", "b"} {
		if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(dst, d)}); err != nil {
			t.Fatalf("copy %s: %v", d, err)
		}
	}
	short, err := m.CheckSpace(filepath.Join(dst, "c"), size)
	if err != nil || short == nil || !short.FitsAlone || short.QueuedJobs != 2 || short.Queued != 2*size {
		t.Fatalf("want: fits alone, not with the 2 queued copies: %v %+v", err, short)
	}
}
