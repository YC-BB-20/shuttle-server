package copyjob

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func digestOfBytes(b []byte) string {
	h := newHasher(AlgoXXH128)
	h.Write(b)
	return h.Sum().String()
}

func TestExportListsEveryFileAndCanBeLimited(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 300<<10)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")

	var all strings.Builder
	n, skipped, err := m.Export(&all, j.st.ID, FormatXXHSum, "", nil)
	if err != nil || n != len(files) || skipped != 0 {
		t.Fatalf("export: %d %d %v", n, skipped, err)
	}
	var got []string
	for _, l := range strings.Split(strings.TrimSuffix(all.String(), "\n"), "\n") {
		h, p, ok := strings.Cut(l, "  ")
		if !ok || h != digestOfBytes(files[p]) {
			t.Fatalf("bad line %q", l)
		}
		got = append(got, p)
	}
	sort.Strings(got)
	if len(got) != len(files) {
		t.Fatalf("%d lines", len(got))
	}

	var some strings.Builder
	n, _, _ = m.Export(&some, j.st.ID, FormatXXHSum, "", []string{"card/CLIP00"})
	for _, l := range strings.Split(strings.TrimSuffix(some.String(), "\n"), "\n") {
		if !strings.Contains(l, "  card/CLIP00/") {
			t.Fatalf("outside the chosen folder: %q", l)
		}
	}
	if n == 0 || n >= len(files) {
		t.Fatalf("limited export wrote %d of %d", n, len(files))
	}

	var c strings.Builder
	if _, _, err := m.Export(&c, j.st.ID, FormatCSV, "", nil); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(c.String())).ReadAll()
	if err != nil || len(rows) != len(files)+1 || rows[0][0] != "path" || rows[1][4] != "Strong" {
		t.Fatalf("csv: %v %v", err, rows[:min(2, len(rows))])
	}
}

func TestWrapperKeepsTheMediaFolderExactAndWritesTheHashListBesideIt(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 300<<10)
	m := newManager(t, filepath.Join(root, "state"))
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now", Wrap: true, WrapName: "A001", WrapHash: true})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")
	media := filepath.Join(dst, "A001", "src")
	checkDest(t, media, files)
	var inside []string
	filepath.Walk(media, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.Contains(p, "hashes") {
			inside = append(inside, p)
		}
		return nil
	})
	if len(inside) != 0 {
		t.Fatalf("a hash file was put inside the media folder: %v", inside)
	}
	b, err := os.ReadFile(filepath.Join(dst, "A001", "src-hashes.txt"))
	if err != nil || strings.Count(string(b), "\n") != len(files) {
		t.Fatalf("hash list beside the media folder: %v %q", err, b)
	}
	// A second copy into the same wrapper never replaces the first list.
	j2, err := m.Add(Spec{Source: src, Dest: dst, S2: "now", Wrap: true, WrapName: "A001", WrapHash: true})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j2.st.ID, "finished")
	if _, err := os.Stat(filepath.Join(dst, "A001", "src-hashes-2.txt")); err != nil {
		t.Fatal("the second list should not overwrite the first")
	}
	if _, err := m.Add(Spec{Source: src, Dest: dst, Wrap: true, WrapName: "../x"}); err == nil {
		t.Fatal("a wrapper name with a slash was accepted")
	}
}
