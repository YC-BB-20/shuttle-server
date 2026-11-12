package diskio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStalledDiskRefusesMoreWork(t *testing.T) {
	d := NewDisk("t", "/", "x", 1, 50*time.Millisecond)
	release := make(chan struct{})
	go d.Do(context.Background(), func() error { <-release; return nil })
	time.Sleep(100 * time.Millisecond)
	d.Check(time.Now())
	if !d.Stalled() {
		t.Fatal("hung operation not noticed")
	}
	if err := d.Do(context.Background(), func() error { return nil }); err != ErrStalled {
		t.Fatalf("got %v, want ErrStalled", err)
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	d.Check(time.Now())
	if d.Stalled() {
		t.Fatal("not cleared after the operation returned")
	}
}

func TestReadChunkAndVolume(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("hello world"), 0o600)
	reg := NewRegistry(1, time.Minute)
	d, err := reg.For(p)
	if err != nil || d.ID == "" || !Present(d) {
		t.Fatalf("volume: %+v %v", d, err)
	}
	if d2, _ := reg.For(dir); d2 != d {
		t.Fatal("same volume gave two disks")
	}
	f, _ := OpenRead(p)
	defer f.Close()
	buf := make([]byte, 8)
	n, err := ReadChunk(context.Background(), d, f, buf, 0)
	if err != nil || n != 8 || string(buf) != "hello wo" {
		t.Fatalf("%d %v %q", n, err, buf)
	}
	n, err = ReadChunk(context.Background(), d, f, buf, 8)
	if err != nil || n != 3 {
		t.Fatalf("tail %d %v", n, err)
	}
	if _, err := VolumeOf(filepath.Join(dir, "not", "yet")); err != nil {
		t.Fatal("parent of a missing path not used")
	}
}

func TestUnmountedVolumePathRefused(t *testing.T) {
	if VolumeMounted("/Volumes/SHUTTLE_NOT_A_REAL_DISK_123/Day 1") {
		t.Fatal("a path on a missing disk looked mounted")
	}
	if !VolumeMounted(t.TempDir()) {
		t.Fatal("an ordinary folder looked unmounted")
	}
}
