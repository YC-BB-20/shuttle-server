package soak

import (
	"io"
	"testing"
	"time"
)

func TestShortSoakStaysFlat(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	s, err := Run(Options{Dir: t.TempDir(), Files: 3000, BigFiles: 2, BigSize: 16 << 20,
		Duration: 12 * time.Second, Every: time.Second, Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	// The point is that memory, threads and files stay flat, not speed: slow
	// CI runners prove a few hundred files in this time, a laptop thousands.
	if s[len(s)-1].Proven < 200 {
		t.Fatalf("too little work done: %+v", s[len(s)-1])
	}
	ok, why := Flat(s)
	if !ok {
		t.Fatal(why)
	}
	t.Log(why, "; last:", s[len(s)-1])
}
