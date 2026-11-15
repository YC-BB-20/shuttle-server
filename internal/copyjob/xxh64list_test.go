package copyjob

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/xxh64"
)

// v0.2.57: with XXH64 lists on, a finished copy saves an xxhsum -H1 list
// beside its destination folder (never inside it), listing every proven
// file with paths from that folder's parent; an existing list is never
// replaced, and a copy with no read-back saves none.
func TestXXH64ListBesideDestination(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "backups", "Day 1")
	files := makeTree(t, src, 1<<20+3)
	m := newManager(t, filepath.Join(root, "state"))
	if _, err := m.SetSettings(Settings{SkipSystem: true, XXH64Lists: true}); err != nil {
		t.Fatal(err)
	}
	startRun(t, m)
	want := func() string {
		var lines []string
		for p, b := range files {
			lines = append(lines, fmt.Sprintf("%016x  Day 1/%s", xxh64.Sum(b), p))
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}()
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	list := filepath.Join(root, "backups", "Day 1.xxh64")
	for n, scheme := range []string{HashDuringCopy, HashFirst} {
		j, err := m.Add(Spec{Source: src, Dest: dst, S2: "now", Hash: scheme})
		if err != nil {
			t.Fatal(err)
		}
		v := waitState(t, m, j.st.ID, "finished")
		if n == 1 {
			list = filepath.Join(root, "backups", "Day 1 2.xxh64") // the first list is kept
		}
		if v.X64List != list || v.X64Err != "" {
			t.Fatalf("list %q, error %q", v.X64List, v.X64Err)
		}
		if got := read(list); got != want {
			t.Fatalf("list:\n%s\nwant:\n%s", got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "Day 1.xxh64")); err == nil {
		t.Fatal("a list was written inside the destination folder")
	}
	left, _ := filepath.Glob(filepath.Join(root, "backups", ".*shuttle-part"))
	if len(left) > 0 {
		t.Fatal("temporary list left behind:", left)
	}

	// Copy only: nothing is read back, so there is no XXH64 and no list.
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(root, "only"), Check: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if v := waitState(t, m, j.st.ID, "finished"); v.X64List != "" {
		t.Fatal("list without a read-back:", v.X64List)
	}
}

func TestX64LineEscapesAwkwardNames(t *testing.T) {
	if got := x64Line(0xab, "a/b c.mov"); got != "00000000000000ab  a/b c.mov\n" {
		t.Fatalf("%q", got)
	}
	if got := x64Line(1, "a\\b\nc"); got != "\\0000000000000001  a\\\\b\\nc\n" {
		t.Fatalf("%q", got)
	}
}

// The copy-then-check scheme, and a copy whose check is its own Verify item,
// also save a list: from the check pass, or from the Verify.
func TestXXH64ListFromCheckPassAndVerify(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	files := makeTree(t, src, 70<<10)
	m := newManager(t, filepath.Join(root, "state"))
	m.SetSettings(Settings{SkipSystem: true, XXH64Lists: true})
	startRun(t, m)
	count := func(p string) int {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "\n")
	}
	j, err := m.Add(Spec{Source: src, Dest: filepath.Join(root, "later"), S2: "now", Hash: HashCheckLater})
	if err != nil {
		t.Fatal(err)
	}
	if v := waitState(t, m, j.st.ID, "finished"); v.X64List == "" || count(v.X64List) != len(files) {
		t.Fatalf("check pass: list %q (%s)", v.X64List, v.X64Err)
	}
	j, err = m.Add(Spec{Source: src, Dest: filepath.Join(root, "sep"), Check: "none", Verify: "run"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")
	v := waitState(t, m, j.st.ID+"-verify", "finished")
	if v.X64List != filepath.Join(root, "sep.xxh64") || count(v.X64List) != len(files) {
		t.Fatalf("verify: list %q (%s)", v.X64List, v.X64Err)
	}
}
