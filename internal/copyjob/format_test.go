package copyjob

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v0.2.92: the destination's format is checked when a copy is added
// (counts only), and a file FAT32 cannot hold fails at once.
func TestFormatCheckAsksThenTooBigFailsAtOnce(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := map[string]int{"big.bin": 2 << 20, "a:b.txt": 10, "Clip.mov": 20, "clip.mov": 30, "Dir/x.mov": 40, "dir/x.mov": 50, "ok/fine.mov": 60}
	for rel, n := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := newManager(t, filepath.Join(root, "state"))
	m.formatHook = func(dir string) destFormat {
		if strings.HasPrefix(dir, src) {
			return destFormat{Name: "ext4"}
		}
		return destFormat{Name: "FAT32", MaxFile: 1 << 20, Strict: true, FoldCase: true}
	}
	_, err := m.Add(Spec{Source: src, Dest: dst})
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("want a format question: %v", err)
	}
	if s := fe.Short; s.TooBig != 1 || s.TooBigBytes != 2<<20 || s.Names != 1 || s.Clashes != 2 || s.Format != "FAT32" ||
		!strings.Contains(s.Message, "1 file over 4 GB") || strings.Contains(s.Message, "big.bin") {
		t.Fatalf("counts %+v", s)
	}
	if len(m.Views()) != 0 {
		t.Fatal("added before the owner answered")
	}
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, IgnoreFormat: true})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.IgnoreFormat {
		t.Fatal("the answer was saved on the job")
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Failed != 1 || v.Verified != len(files)-1 {
		t.Fatalf("%+v", v)
	}
	p := m.Problems(j.st.ID, 10)
	if len(p) != 1 || p[0].Path != "big.bin" || !strings.Contains(p[0].Why, "FAT32") {
		t.Fatalf("problems %+v", p)
	}
	if _, err := os.Lstat(filepath.Join(dst, "big.bin")); err == nil {
		t.Fatal("the too-big file was written")
	}
	if ents, _ := os.ReadDir(dst); len(ents) > 0 {
		for _, e := range ents {
			if strings.Contains(e.Name(), "big.bin") {
				t.Fatalf("something of the too-big file was written: %s", e.Name())
			}
		}
	}
}

func TestFormatCheckQuietWhenItFits(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	makeTree(t, src, 100)
	m := newManager(t, filepath.Join(root, "state"))
	m.formatHook = func(string) destFormat { return destFormat{Name: "APFS", FoldCase: true} } // source folds too: no clash possible
	if _, err := m.Add(Spec{Source: src, Dest: filepath.Join(root, "dst")}); err != nil {
		t.Fatal(err)
	}
}

func TestFoldsCaseAsksTheDisk(t *testing.T) {
	d := filepath.Join(t.TempDir(), "Media")
	os.MkdirAll(d, 0o755)
	folds, known := foldsCase(d, "/")
	if !known {
		t.Fatal("a folder with letters gave no answer")
	}
	// Linux temp folders are case-sensitive; a Mac's usually are not.
	_, err := os.Stat(filepath.Join(filepath.Dir(d), "mEDIA"))
	if folds != (err == nil) {
		t.Fatalf("folds %v, flipped lookup %v", folds, err)
	}
	if badName("a/b c.mov") || !badName("a/b:c.mov") || !badName("x?.mov") {
		t.Fatal("badName")
	}
	if unfit(destFormat{Name: "FAT32", MaxFile: fat32Max}, fat32Max) != "" || unfit(destFormat{Name: "FAT32", MaxFile: fat32Max}, fat32Max+1) == "" {
		t.Fatal("4 GB limit")
	}
}

func TestFullDiskWaitSaysWhatToDo(t *testing.T) {
	err := error(noSpace{disk: "BB - backups", need: 3_000_000_000, free: 1_000_000_000})
	if !errors.Is(err, errNoSpace) {
		t.Fatal("not a no-space error")
	}
	w := spaceWait(err).reason
	for _, want := range []string{"Destination disk BB - backups is full", "Make room there", "continues by itself", "nothing copied is lost"} {
		if !strings.Contains(w, want) {
			t.Errorf("%q lacks %q", w, want)
		}
	}
}
