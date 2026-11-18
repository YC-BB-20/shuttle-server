package copyjob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// memServer is a server in memory: files by path, folders implied.
type memServer struct {
	mu      sync.Mutex
	files   map[string][]byte
	damage  func(p string, b []byte) // changes what a download gives, once per call
	opens   int
	renames int
	slow    time.Duration // a pause after every 64 KB a download gives
	resumed int64         // the furthest offset a download was opened at
}

type slowReader struct {
	r    io.Reader
	wait time.Duration
}

func (s slowReader) Read(b []byte) (int, error) {
	time.Sleep(s.wait)
	return s.r.Read(b[:min(len(b), 64<<10)])
}

func newMem() *memServer { return &memServer{files: map[string][]byte{}} }

func (s *memServer) Check(context.Context) (remote.Info, error) { return remote.Info{Free: -1}, nil }

func (s *memServer) List(ctx context.Context, dir string, fn func(remote.Entry) error) error {
	s.mu.Lock()
	seen := map[string]bool{}
	var out []remote.Entry
	pre := strings.Trim(dir, "/")
	if pre != "" {
		pre += "/"
	}
	for p, b := range s.files {
		rest, ok := strings.CutPrefix(p, pre)
		if !ok {
			continue
		}
		name, _, sub := strings.Cut(rest, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, remote.Entry{Path: pre + name, Dir: sub, Size: int64(len(b)), Mod: time.Unix(1700000000, 0)})
	}
	s.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	for _, e := range out {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *memServer) Stat(_ context.Context, p string) (remote.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[p]
	if !ok {
		return remote.Entry{}, remote.ErrNotFound
	}
	return remote.Entry{Path: p, Size: int64(len(b)), Mod: time.Unix(1700000000, 0)}, nil
}

func (s *memServer) Open(_ context.Context, p string, off int64) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[p]
	if !ok {
		return nil, remote.ErrNotFound
	}
	s.opens++
	b = append([]byte(nil), b...)
	if s.damage != nil {
		s.damage(p, b)
	}
	s.resumed = max(s.resumed, off)
	if s.slow > 0 {
		return io.NopCloser(slowReader{bytes.NewReader(b[off:]), s.slow}), nil
	}
	return io.NopCloser(bytes.NewReader(b[off:])), nil
}

func (s *memServer) Mkdir(context.Context, string) error { return nil }

func (s *memServer) Upload(ctx context.Context, p string, size int64, src io.ReaderAt, st *remote.UploadState, save func(remote.UploadState) error, done func(int64)) error {
	s.mu.Lock()
	_, there := s.files[p]
	s.mu.Unlock()
	if there {
		return remote.ErrExists
	}
	out := make([]byte, 0, size)
	buf := make([]byte, 100<<10)
	for off := int64(0); off < size; {
		n, err := src.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
		out = append(out, buf[:n]...)
		off += int64(n)
		if done != nil {
			done(off)
		}
		if err != nil && off < size {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.files[p] = out
	s.mu.Unlock()
	return nil
}

func (s *memServer) Abort(context.Context, remote.UploadState) error { return nil }

func (s *memServer) Rename(_ context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[to]; ok {
		return remote.ErrExists
	}
	b, ok := s.files[from]
	if !ok {
		return remote.ErrNotFound
	}
	s.renames++
	s.files[to] = b
	delete(s.files, from)
	return nil
}

func (s *memServer) Resumable() bool { return false }

func (s *memServer) RemoveTemp(_ context.Context, p string) error {
	if !remote.IsTemp(p) {
		return errors.New("not ours")
	}
	s.mu.Lock()
	delete(s.files, p)
	s.mu.Unlock()
	return nil
}

type memServers map[string]*memServer

func (ms memServers) Open(id string) (remote.Remote, remote.Public, error) {
	s, ok := ms[id]
	if !ok {
		return nil, remote.Public{}, errors.New("no such server")
	}
	return s, remote.Public{ID: id, Name: "Server " + id}, nil
}

func TestServerCopiesUpDownAndAcross(t *testing.T) {
	root := t.TempDir()
	src, back := filepath.Join(root, "src"), filepath.Join(root, "back")
	files := makeTree(t, src, 2<<20+77)
	a, b := newMem(), newMem()
	m := newManager(t, filepath.Join(root, "state"))
	m.Servers = memServers{"a": a, "b": b}
	startRun(t, m)

	// Up: this computer to server a, into "Shoot".
	j, err := m.Add(Spec{Source: src, Dest: "srv:a/Shoot", Only: []string{"card"}})
	if err != nil {
		t.Fatal(err)
	}
	if !j.st.Spec.isServer() || j.st.Spec.DestServer != "Server a" {
		t.Fatalf("not a server copy: %+v", j.st.Spec)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Verified != len(files) || v.Failed != 0 {
		t.Fatalf("up: %+v", v)
	}
	for rel, want := range files {
		if got := a.files["Shoot/"+rel]; !bytes.Equal(got, want) {
			t.Fatalf("up: %s differs on the server", rel)
		}
	}
	for p := range a.files {
		if remote.IsTemp(p) {
			t.Fatalf("up: temporary upload left: %s", p)
		}
	}

	// Across: server a to server b.
	j, err = m.Add(Spec{Source: "srv:a/Shoot", Dest: "srv:b"})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, j.st.ID, "finished")
	if v.Verified != len(files) || v.Failed != 0 {
		t.Fatalf("across: %+v", v)
	}
	for rel, want := range files {
		if !bytes.Equal(b.files[rel], want) {
			t.Fatalf("across: %s differs", rel)
		}
	}

	// Down: server b back to this computer, with S2.
	j, err = m.Add(Spec{Source: "srv:b", Dest: back})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, j.st.ID, "finished")
	if v.Verified != len(files) || v.Strong != len(files) || v.Failed != 0 {
		t.Fatalf("down: %+v", v)
	}
	checkDest(t, back, files)

	// Again: everything is already there, compared and never replaced.
	j, err = m.Add(Spec{Source: src, Dest: "srv:a/Shoot", Only: []string{"card"}})
	if err != nil {
		t.Fatal(err)
	}
	v = waitState(t, m, j.st.ID, "finished")
	if v.Identical != len(files) || v.Failed != 0 {
		t.Fatalf("again: %+v", v)
	}
	if _, err := m.Add(Spec{Source: "srv:a/Shoot", Dest: "srv:a/Shoot/card"}); err == nil {
		t.Fatal("a copy into its own source was accepted")
	}
	if _, err := m.Add(Spec{Source: src, Dest: "srv:nope/x"}); err == nil {
		t.Fatal("a server not in Settings was accepted")
	}
}

func TestServerBadReadBackFailsAndNeverGetsTheName(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(src, 0o755)
	data := bytes.Repeat([]byte("frame"), 50000)
	os.WriteFile(filepath.Join(src, "a.mov"), data, 0o644)
	os.WriteFile(filepath.Join(src, "b.mov"), data[:1000], 0o644)
	s := newMem()
	s.files["Shoot/b.mov"] = []byte("someone else's file")
	s.damage = func(p string, b []byte) {
		if path.Base(p) == "a.mov.shuttle-part" {
			b[100] ^= 1 // the server always gives one bad byte back
		}
	}
	m := newManager(t, filepath.Join(root, "state"))
	m.Servers = memServers{"s": s}
	startRun(t, m)
	j, err := m.Add(Spec{Source: src, Dest: "srv:s/Shoot"})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "asking")
	if v.Failed != 2 || v.Verified != 0 {
		t.Fatalf("%+v", v)
	}
	if _, ok := s.files["Shoot/a.mov"]; ok {
		t.Fatal("an unproven upload got its real name")
	}
	if string(s.files["Shoot/b.mov"]) != "someone else's file" {
		t.Fatal("an existing file on the server was replaced")
	}
	if _, ok := s.files["Shoot/a.mov.shuttle-part"]; !ok {
		t.Fatal("the failed upload was not left for inspection")
	}
}

func TestServerPauseResumesDownload(t *testing.T) {
	root := t.TempDir()
	s := newMem()
	big := bytes.Repeat([]byte("0123456789abcdef"), 2<<20>>4*3) // 6 MB
	s.files["card/big.mov"] = big
	s.slow = time.Millisecond
	m := newManager(t, filepath.Join(root, "state"))
	m.Servers = memServers{"s": s}
	startRun(t, m)
	dst := filepath.Join(root, "dst")
	j, err := m.Add(Spec{Source: "srv:s", Dest: dst, S2: "skip"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		time.Sleep(60 * time.Millisecond)
		m.Pause(j.st.ID)
		waitState(t, m, j.st.ID, "paused", "finished")
		m.Resume(j.st.ID)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.Verified != 1 || v.Strong != 0 {
		t.Fatalf("%+v", v)
	}
	checkDest(t, dst, map[string][]byte{"card/big.mov": big})
	if s.resumed == 0 {
		t.Error("the download started again from zero after a pause")
	}
}
