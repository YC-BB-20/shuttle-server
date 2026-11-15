package xxh64

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// Published XXH64 values, seed 0. CI also compares `shuttle-server hash -H1`
// with the official `xxhsum -H1` on files of many sizes.
func TestKnownValues(t *testing.T) {
	for in, want := range map[string]uint64{
		"":    0xef46db3751d8e999,
		"a":   0xd24ec4f1a98c6e5b,
		"abc": 0x44bc2cf5ad770999,
		"The quick brown fox jumps over the lazy dog": 0x0b242d361fda71bc,
	} {
		if got := Sum([]byte(in)); got != want {
			t.Errorf("XXH64(%q) = %016x, want %016x", in, got, want)
		}
	}
}

// Writing in pieces of any size, and saving and restoring the state on the
// way, gives the same hash as hashing all at once.
func TestStreamingAndSavedState(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, n := range []int{0, 1, 3, 4, 7, 8, 31, 32, 33, 63, 64, 65, 100, 1000, 100003} {
		b := make([]byte, n)
		r.Read(b)
		want := Sum(b)
		d := New()
		for rest := b; len(rest) > 0; {
			k := min(len(rest), 1+r.Intn(70))
			d.Write(rest[:k])
			rest = rest[k:]
			st, _ := d.MarshalBinary()
			d = New()
			if err := d.UnmarshalBinary(st); err != nil {
				t.Fatal(err)
			}
		}
		if got := d.Sum64(); got != want || d.Size() != uint64(n) {
			t.Fatalf("n=%d: %016x, want %016x", n, got, want)
		}
	}
	st, _ := New().MarshalBinary()
	bad := bytes.Clone(st)
	bad[len(bad)-1] = 5 // buffered count that does not match the total
	if New().UnmarshalBinary(bad) == nil || New().UnmarshalBinary(st[:10]) == nil {
		t.Fatal("damaged state accepted")
	}
}

func BenchmarkXXH64(b *testing.B) {
	buf := make([]byte, 1<<20)
	b.SetBytes(int64(len(buf)))
	d := New()
	for i := 0; i < b.N; i++ {
		d.Write(buf)
	}
	_ = fmt.Sprint(d.Sum64())
}
