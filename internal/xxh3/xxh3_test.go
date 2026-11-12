package xxh3

import (
	"bufio"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// sanityBuffer is xxHash's own test buffer (XSUM_fillTestBuffer).
func sanityBuffer(n int) []byte {
	b := make([]byte, n)
	gen := uint64(2654435761)
	for i := range b {
		b[i] = byte(gen >> 56)
		gen *= 11400714785074694797
	}
	return b
}

type vector struct {
	n    int
	want string
}

func vectors(t *testing.T) ([]vector, []byte) {
	f, err := os.Open("testdata/xxh128_seed0.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var vs []vector
	max := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "#") || l == "" {
			continue
		}
		p := strings.Fields(l)
		n, _ := strconv.Atoi(p[0])
		vs = append(vs, vector{n, p[1]})
		if n > max {
			max = n
		}
	}
	if len(vs) < 100 {
		t.Fatalf("only %d vectors", len(vs))
	}
	return vs, sanityBuffer(max)
}

func TestOfficialVectorsOneShot(t *testing.T) {
	vs, buf := vectors(t)
	for _, v := range vs {
		if got := Sum128(buf[:v.n]).String(); got != v.want {
			t.Fatalf("len %d: got %s want %s", v.n, got, v.want)
		}
	}
}

func TestOfficialVectorsStreamingAnyChunking(t *testing.T) {
	vs, buf := vectors(t)
	r := rand.New(rand.NewSource(1))
	for _, v := range vs {
		h := New()
		in := buf[:v.n]
		for len(in) > 0 {
			k := 1 + r.Intn(len(in))
			if r.Intn(4) == 0 {
				k = 1 + r.Intn(70)
			}
			if k > len(in) {
				k = len(in)
			}
			h.Write(in[:k])
			in = in[k:]
		}
		if got := h.Sum128().String(); got != v.want {
			t.Fatalf("len %d streamed: got %s want %s", v.n, got, v.want)
		}
	}
}

// Pausing anywhere, saving, and resuming gives the same hash as one pass.
func TestSaveAndResumeAnywhere(t *testing.T) {
	data := sanityBuffer(200_000)
	want := Sum128(data)
	r := rand.New(rand.NewSource(2))
	for trial := 0; trial < 300; trial++ {
		cut := r.Intn(len(data) + 1)
		h := New()
		h.Write(data[:cut])
		saved, _ := h.MarshalBinary()
		var h2 Hasher
		if err := h2.UnmarshalBinary(saved); err != nil {
			t.Fatal(err)
		}
		if h2.Size() != uint64(cut) {
			t.Fatal("size not restored")
		}
		h2.Write(data[cut:])
		if h2.Sum128() != want {
			t.Fatalf("resume at %d differs", cut)
		}
	}
}

func TestDamagedStateRefused(t *testing.T) {
	h := New()
	h.Write(sanityBuffer(5000))
	b, _ := h.MarshalBinary()
	for _, bad := range [][]byte{b[:len(b)-1], append([]byte("XXXX"), b[4:]...), nil} {
		var h2 Hasher
		if h2.UnmarshalBinary(bad) == nil {
			t.Fatal("damaged state accepted")
		}
	}
	b[4] = 9 // future version
	var h2 Hasher
	if h2.UnmarshalBinary(b) == nil {
		t.Fatal("unknown version accepted")
	}
}

func TestParseRoundTrip(t *testing.T) {
	h := Sum128([]byte("Shuttle"))
	p, err := Parse(h.String())
	if err != nil || p != h {
		t.Fatal("parse round trip")
	}
}

func BenchmarkStream(b *testing.B) {
	buf := sanityBuffer(1 << 20)
	b.SetBytes(int64(len(buf)))
	h := New()
	for i := 0; i < b.N; i++ {
		h.Write(buf)
	}
}
