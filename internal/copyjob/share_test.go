package copyjob

import (
	"os"
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

// A job whose share came back at another mount point follows it there; a
// share found at its old place, or not found, changes nothing (v0.2.112).
func TestJobFollowsReconnectedShare(t *testing.T) {
	root := t.TempDir()
	m := newManager(t, filepath.Join(root, "state"))
	j, err := m.Add(Spec{Source: filepath.Join(root, "src0"), Dest: filepath.Join(root, "dst0"), Paused: true})
	if err != nil {
		os.MkdirAll(filepath.Join(root, "src0"), 0o755)
		if j, err = m.Add(Spec{Source: filepath.Join(root, "src0"), Dest: filepath.Join(root, "dst0"), Paused: true}); err != nil {
			t.Fatal(err)
		}
	}
	from := "//filer.local/Media"
	j.mu.Lock()
	j.st.Spec.Dest, j.st.DestVol = "/Volumes/Media/Day 01", "share:"+from+":/Volumes/Media"
	j.mu.Unlock()
	old := findShare
	t.Cleanup(func() { findShare = old })
	findShare = func(f string) (diskio.Volume, bool) { return diskio.Volume{}, false }
	if m.followShare(j, false) {
		t.Fatal("moved with no share found")
	}
	findShare = func(f string) (diskio.Volume, bool) {
		if f != from {
			return diskio.Volume{}, false
		}
		return diskio.Volume{Mount: "/Volumes/Media-1", Share: diskio.ShareSMB, From: from, ID: "share:" + from + ":/Volumes/Media-1"}, true
	}
	if m.followShare(j, true) {
		t.Fatal("the source is not a share")
	}
	if !m.followShare(j, false) || j.st.Spec.Dest != "/Volumes/Media-1/Day 01" || j.st.DestVol != "share:"+from+":/Volumes/Media-1" {
		t.Fatalf("dest %q vol %q", j.st.Spec.Dest, j.st.DestVol)
	}
	saved, err := loadJob(j.dir)
	if err != nil || saved.st.Spec.Dest != "/Volumes/Media-1/Day 01" {
		t.Fatalf("not saved: %v", err)
	}
	if m.followShare(j, false) {
		t.Fatal("moved again to where it already is")
	}
}
