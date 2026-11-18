package copyjob

import (
	"path/filepath"
	"sync/atomic"
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
	if j.st.Spec.Lucid || j.st.Spec.Hash != HashDuringCopy || j.st.Spec.DestShare != diskio.ShareSMB {
		t.Fatalf("spec %+v", j.st.Spec)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.DestShare != diskio.ShareSMB {
		t.Fatalf("%+v", v)
	}
}

// A copy onto a share is flushed as it goes, so its progress follows what
// has really been sent; a copy onto a disk is not (v0.2.103).
func TestCopyToShareFlushesAsItGoes(t *testing.T) {
	defer func(n int64) { ShareFlushEvery, shareFlushed = n, nil }(ShareFlushEvery)
	ShareFlushEvery = 128 << 10
	var n atomic.Int64
	shareFlushed = func() { n.Add(1) }
	for _, share := range []string{diskio.ShareLucid, ""} {
		n.Store(0)
		root := t.TempDir()
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		files := makeTree(t, src, 0)
		m := newManager(t, filepath.Join(root, "state"))
		m.shareHook = func(string) string { return share }
		if share == diskio.ShareLucid {
			m.Lucid = &fakeLucid{copies: func() int { return 0 }}
		}
		startRun(t, m)
		j, err := m.Add(Spec{Source: src, Dest: dst})
		if err != nil {
			t.Fatal(err)
		}
		v := waitState(t, m, j.st.ID, "finished")
		if v.Strong+v.Verified < len(files) && v.Copied < len(files) {
			t.Fatalf("share %q: %+v", share, v)
		}
		if got := n.Load(); share != "" && got < 4 || share == "" && got != 0 {
			t.Fatalf("share %q: %d flushes along the way", share, got)
		}
	}
}
