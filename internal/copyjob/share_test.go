package copyjob

import (
	"path/filepath"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// An SMB share is copied like a disk, with the share noted for the page.
func TestCopyToSMBShareIsNoted(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 0)
	m := newManager(t, filepath.Join(root, "state"))
	m.shareHook = func(string) string { return diskio.ShareSMB }
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.Hash != HashDuringCopy || j.st.Spec.DestShare != diskio.ShareSMB {
		t.Fatalf("spec %+v", j.st.Spec)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.DestShare != diskio.ShareSMB {
		t.Fatalf("%+v", v)
	}
}

// Until v0.2.98, LucidLink is refused as a destination.
func TestCopyToLucidLinkRefusedFor097(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 0)
	m := newManager(t, filepath.Join(root, "state"))
	m.shareHook = func(string) string { return diskio.ShareLucid }
	if _, err := m.Add(Spec{Source: src, Dest: dst}); err == nil {
		t.Fatal("a copy to LucidLink was accepted")
	}
}
