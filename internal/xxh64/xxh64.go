// Package xxh64 is XXH64 (seed 0) in plain Go, with a state that can be
// saved and restored, so a hash can continue mid-file after a pause.
//
// Shuttle proves copies with XXH128 (package xxh3). XXH64 is made beside it
// only for other tools: offload apps, MHL readers and `xxhsum -H1` (v0.2.57).
package xxh64

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	p1 uint64 = 0x9E3779B185EBCA87
	p2 uint64 = 0xC2B2AE3D27D4EB4F
	p3 uint64 = 0x165667B19E3779F9
	p4 uint64 = 0x85EBCA77C2B2AE63
	p5 uint64 = 0x27D4EB2F165667C5
)

// Digest is a running XXH64 with seed 0.
type Digest struct {
	v     [4]uint64
	total uint64
	mem   [32]byte
	n     int // bytes in mem
}

// New returns a fresh digest.
func New() *Digest { d := &Digest{}; d.Reset(); return d }

// Reset starts again.
func (d *Digest) Reset() {
	var a, b uint64 = p1, p2 // variables: the sums wrap around, as XXH64 intends
	*d = Digest{v: [4]uint64{a + b, b, 0, 0 - a}}
}

// Size is the number of bytes hashed so far.
func (d *Digest) Size() uint64 { return d.total }

func round(acc, in uint64) uint64 {
	acc += in * p2
	acc = bits.RotateLeft64(acc, 31)
	return acc * p1
}

func merge(acc, v uint64) uint64 {
	acc ^= round(0, v)
	return acc*p1 + p4
}

func u64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }
func u32(b []byte) uint64 { return uint64(binary.LittleEndian.Uint32(b)) }

func (d *Digest) stripes(b []byte) []byte {
	v1, v2, v3, v4 := d.v[0], d.v[1], d.v[2], d.v[3]
	for len(b) >= 32 {
		v1 = round(v1, u64(b[0:8]))
		v2 = round(v2, u64(b[8:16]))
		v3 = round(v3, u64(b[16:24]))
		v4 = round(v4, u64(b[24:32]))
		b = b[32:]
	}
	d.v = [4]uint64{v1, v2, v3, v4}
	return b
}

// Write adds bytes; it never fails.
func (d *Digest) Write(b []byte) (int, error) {
	n := len(b)
	d.total += uint64(n)
	if d.n > 0 {
		c := copy(d.mem[d.n:], b)
		d.n += c
		b = b[c:]
		if d.n < 32 {
			return n, nil
		}
		d.stripes(d.mem[:])
		d.n = 0
	}
	b = d.stripes(b)
	d.n = copy(d.mem[:], b)
	return n, nil
}

// Sum64 is the hash of everything written so far.
func (d *Digest) Sum64() uint64 {
	var h uint64
	if d.total >= 32 {
		v1, v2, v3, v4 := d.v[0], d.v[1], d.v[2], d.v[3]
		h = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		h = merge(h, v1)
		h = merge(h, v2)
		h = merge(h, v3)
		h = merge(h, v4)
	} else {
		h = p5
	}
	h += d.total
	b := d.mem[:d.n]
	for ; len(b) >= 8; b = b[8:] {
		h ^= round(0, u64(b))
		h = bits.RotateLeft64(h, 27)*p1 + p4
	}
	if len(b) >= 4 {
		h ^= u32(b) * p1
		h = bits.RotateLeft64(h, 23)*p2 + p3
		b = b[4:]
	}
	for _, c := range b {
		h ^= uint64(c) * p5
		h = bits.RotateLeft64(h, 11) * p1
	}
	h ^= h >> 33
	h *= p2
	h ^= h >> 29
	h *= p3
	h ^= h >> 32
	return h
}

// Sum is the XXH64 of b.
func Sum(b []byte) uint64 { d := New(); d.Write(b); return d.Sum64() }

const (
	stateMagic   = "X64S"
	stateVersion = 1
	stateSize    = 4 + 1 + 4*8 + 8 + 32 + 1
)

// ErrState means a saved state is damaged or from another version; the hash
// must start again from the beginning of the file.
var ErrState = errors.New("saved XXH64 state not usable; start this file again")

// MarshalBinary saves the state.
func (d *Digest) MarshalBinary() ([]byte, error) {
	b := make([]byte, 0, stateSize)
	b = append(b, stateMagic...)
	b = append(b, stateVersion)
	for _, v := range d.v {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	b = binary.LittleEndian.AppendUint64(b, d.total)
	b = append(b, d.mem[:]...)
	return append(b, byte(d.n)), nil
}

// UnmarshalBinary restores a saved state, refusing anything inconsistent.
func (d *Digest) UnmarshalBinary(b []byte) error {
	if len(b) != stateSize || string(b[:4]) != stateMagic || b[4] != stateVersion {
		return ErrState
	}
	var r Digest
	p := b[5:]
	for i := range r.v {
		r.v[i] = u64(p[i*8:])
	}
	r.total = u64(p[32:])
	copy(r.mem[:], p[40:72])
	r.n = int(p[72])
	if r.n >= 32 || uint64(r.n) != r.total%32 {
		return ErrState
	}
	*d = r
	return nil
}
