package copyjob

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// fakeLucid stands in for LucidLink's client.
type fakeLucid struct {
	mu     sync.Mutex
	left   int64 // bytes waiting to upload
	fail   bool  // Waiting cannot be answered
	drops  int
	copies func() int // copies made, read when the cache is dropped
	atDrop []int
}

func (f *fakeLucid) Waiting(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("LucidLink's command could not say what is left to upload (test)")
	}
	return f.left, nil
}

func (f *fakeLucid) Drop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drops++
	f.atDrop = append(f.atDrop, f.copies())
	return nil
}

func (f *fakeLucid) set(left int64, fail bool) {
	f.mu.Lock()
	f.left, f.fail = left, fail
	f.mu.Unlock()
}

func waitReason(t *testing.T, m *Manager, id, part string) View {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range m.Views() {
			if v.ID == id && v.State == "waiting" && strings.Contains(v.Reason, part) {
				return v
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job never waited with %q: %+v", part, m.Views())
	return View{}
}

// A copy to LucidLink pauses while too much waits to upload, copies every
// file first, reads nothing back while LucidLink cannot be asked, and reads
// back only after LucidLink says nothing is left and its cache is dropped.
func TestCopyToLucidLinkReadsBackAfterUpload(t *testing.T) {
	oldPoll, oldEvery := lucidPoll, lucidEvery
	lucidPoll, lucidEvery = 5*time.Millisecond, 0
	t.Cleanup(func() { lucidPoll, lucidEvery = oldPoll, oldEvery })

	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	copies := 0
	var cmu sync.Mutex
	m.AfterCopy = func(string) { cmu.Lock(); copies++; cmu.Unlock() }
	fl := &fakeLucid{left: 5e9, copies: func() int { cmu.Lock(); defer cmu.Unlock(); return copies }}
	m.Lucid, m.LucidMax = fl, 1e9
	m.shareHook = func(string) string { return diskio.ShareLucid }
	startRun(t, m)

	j, err := m.Add(Spec{Source: src, Dest: dst}) // asked to hash while copying: becomes copy first, check after
	if err != nil {
		t.Fatal(err)
	}
	if !j.st.Spec.Lucid || j.st.Spec.Hash != HashCheckLater || j.st.Spec.DestShare != diskio.ShareLucid {
		t.Fatalf("spec %+v", j.st.Spec)
	}
	waitReason(t, m, j.st.ID, "Waiting for LucidLink to upload")
	cmu.Lock()
	if copies != 0 {
		t.Fatalf("%d files copied while LucidLink was over its limit", copies)
	}
	cmu.Unlock()

	fl.set(0, true) // room again, but LucidLink cannot be asked: copying goes on, the check waits
	v := waitReason(t, m, j.st.ID, "Waiting for LucidLink:")
	cmu.Lock()
	if copies != len(files) {
		t.Fatalf("want every file copied before the check, got %d of %d", copies, len(files))
	}
	cmu.Unlock()
	if v.Strong != 0 || v.Verified != 0 {
		t.Fatalf("copies were called proven before LucidLink was asked: %+v", v)
	}

	fl.set(3e9, false) // asked, but still uploading
	waitReason(t, m, j.st.ID, "finish uploading")
	fl.mu.Lock()
	if fl.drops != 0 {
		t.Fatal("cache dropped while data was waiting to upload")
	}
	fl.mu.Unlock()

	fl.set(0, false)
	v = waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.Failed != 0 {
		t.Fatalf("%+v", v)
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.drops != 1 || fl.atDrop[0] != len(files) {
		t.Fatalf("want one cache drop after all %d copies, got %d at %v", len(files), fl.drops, fl.atDrop)
	}
	checkDest(t, dst, files)
}

// Without LucidLink's client, a copy to LucidLink is never read back.
func TestCopyToLucidLinkWithoutClientWaits(t *testing.T) {
	oldPoll := lucidPoll
	lucidPoll = 5 * time.Millisecond
	t.Cleanup(func() { lucidPoll = oldPoll })
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	makeTree(t, src, 0)
	m := newManager(t, filepath.Join(root, "state"))
	m.shareHook = func(string) string { return diskio.ShareLucid }
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	v := waitReason(t, m, j.st.ID, "cannot ask LucidLink")
	if v.Strong != 0 || v.Verified != 0 {
		t.Fatalf("%+v", v)
	}
}

// Owner's decision of 17 November 2026: the second read of a LucidLink
// source (S2) reads LucidLink's cache as it is. LucidLink is never asked,
// its cache never dropped and nothing downloaded again for it, while a
// copy to a local disk still reads that disk back.
func TestLucidLinkSourceSecondReadUsesCache(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "lucid"), filepath.Join(root, "dst")
	files := makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	fl := &fakeLucid{copies: func() int { return 0 }}
	m.Lucid, m.LucidMax = fl, 1e9
	m.shareHook = func(dir string) string {
		if dir == src {
			return diskio.ShareLucid
		}
		return ""
	}
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.Lucid || j.st.Spec.SourceShare != diskio.ShareLucid || j.st.Spec.S2 != "now" {
		t.Fatalf("spec %+v", j.st.Spec)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.Failed != 0 {
		t.Fatalf("want every file read a second time and matching: %+v", v)
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.drops != 0 {
		t.Fatalf("LucidLink's cache was dropped %d times for a LucidLink source", fl.drops)
	}
	checkDest(t, dst, files)
}

// v0.2.110, "read the cache": a copy to LucidLink is read back at once from
// the cache on this computer, never clears the cache, and is finished only
// when LucidLink says nothing is left to upload.
func TestCopyToLucidLinkFromCache(t *testing.T) {
	oldPoll, oldEvery := lucidPoll, lucidEvery
	lucidPoll, lucidEvery = 5*time.Millisecond, 0
	t.Cleanup(func() { lucidPoll, lucidEvery = oldPoll, oldEvery })
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 1<<20)
	m := newManager(t, filepath.Join(root, "state"))
	fl := &fakeLucid{left: 3e9, copies: func() int { return 0 }}
	m.Lucid, m.LucidMax = fl, 0 // no copy pause: only the upload wait is tested
	m.shareHook = func(string) string { return diskio.ShareLucid }
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: dst, LucidRead: LucidReadCache})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.LucidRead != LucidReadCache {
		t.Fatalf("spec %+v", j.st.Spec)
	}
	v := waitReason(t, m, j.st.ID, "after checking the copies on this Mac")
	if v.Verified != len(files) {
		t.Fatalf("copies not read back from the cache before the upload finished: verified %d strong %d copied %d proven %d phase? stage %q", v.Verified, v.Strong, v.Copied, v.Proven, v.Stage)
	}
	fl.set(0, false)
	v = waitState(t, m, j.st.ID, "finished")
	if v.Strong != len(files) || v.Failed != 0 || v.LucidRead != LucidReadCache {
		t.Fatalf("%+v", v)
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.drops != 0 {
		t.Fatal("the cache was cleared")
	}
	checkDest(t, dst, files)

	// A request that does not say uses the Settings default; not on LucidLink it is empty.
	m.SetSettings(Settings{LucidRead: LucidReadCache})
	if got := lucidReadFor(true, "", m.Settings().LucidRead); got != LucidReadCache {
		t.Fatalf("default %q", got)
	}
	if got := lucidReadFor(false, LucidReadCache, LucidReadCache); got != "" {
		t.Fatalf("not on LucidLink: %q", got)
	}
	if DefaultSettings.LucidRead != LucidReadCloud {
		t.Fatal("the default must stay the download from LucidLink")
	}
}

// The same for a check in its own Verify item after the copy: the choice is
// carried over, the Verify reads the cache at once and finishes only after
// the upload (v0.2.110).
func TestSeparateVerifyOnLucidLinkFromCache(t *testing.T) {
	oldPoll, oldEvery := lucidPoll, lucidEvery
	lucidPoll, lucidEvery = 5*time.Millisecond, 0
	t.Cleanup(func() { lucidPoll, lucidEvery = oldPoll, oldEvery })
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	files := makeTree(t, src, 0)
	m := newManager(t, filepath.Join(root, "state"))
	fl := &fakeLucid{left: 3e9, copies: func() int { return 0 }}
	m.Lucid = fl
	m.shareHook = func(string) string { return diskio.ShareLucid }
	startRun(t, m)
	c, err := m.Add(Spec{Source: src, Dest: dst, Check: "none", Verify: "run", S2: "now", LucidRead: LucidReadCache})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, c.st.ID, "finished")
	var vid string
	for _, v := range m.Views() {
		if v.Kind == KindVerify {
			vid = v.ID
		}
	}
	if vid == "" {
		t.Fatal("no Verify item")
	}
	v := waitReason(t, m, vid, "after checking the copies on this Mac")
	if v.LucidRead != LucidReadCache {
		t.Fatalf("choice not carried over: %+v", v.LucidRead)
	}
	fl.set(0, false)
	v = waitState(t, m, vid, "finished")
	if v.Verified != len(files) || v.Failed != 0 {
		t.Fatalf("verified %d failed %d", v.Verified, v.Failed)
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.drops != 0 {
		t.Fatal("the cache was cleared")
	}
}
