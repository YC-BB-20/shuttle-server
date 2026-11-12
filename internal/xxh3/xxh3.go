// Package xxh3 is XXH3-128 (XXH128), written in plain Go from the reference
// xxhash.h, with no seed and the default secret: the hash xxhsum -H2 prints.
// The streaming Hasher can save its state mid-file and continue later, so a
// paused or interrupted hash resumes where it stopped.
package xxh3

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/bits"
)

const (
	p32_1 = 0x9E3779B1
	p32_2 = 0x85EBCA77
	p32_3 = 0xC2B2AE3D
	p64_1 = 0x9E3779B185EBCA87
	p64_2 = 0xC2B2AE3D27D4EB4F
	p64_3 = 0x165667B19E3779F9
	p64_4 = 0x85EBCA77C2B2AE63
	p64_5 = 0x27D4EB2F165667C5
	pMX1  = 0x165667919E3779F9
	pMX2  = 0x9FB21C651E98DF25

	stripeLen      = 64
	secretRate     = 8
	secretSize     = 192
	secretLimit    = secretSize - stripeLen    // 128
	stripesPerBlk  = secretLimit / secretRate  // 16
	blockLen       = stripeLen * stripesPerBlk // 1024
	bufSize        = 256
	midSizeMax     = 240
	lastAccStart   = 7
	mergeAccsStart = 11
	midStartOff    = 3
	midLastOff     = 17
	secretSizeMin  = 136
)

// Hash is a 128-bit digest.
type Hash struct{ Hi, Lo uint64 }

// Bytes is the canonical big-endian form xxhsum prints.
func (h Hash) Bytes() [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], h.Hi)
	binary.BigEndian.PutUint64(b[8:], h.Lo)
	return b
}

// String is the 32-character hex xxhsum prints.
func (h Hash) String() string { b := h.Bytes(); return hex.EncodeToString(b[:]) }

// Parse reads the 32-character hex form.
func Parse(s string) (Hash, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return Hash{}, errors.New("not a 128-bit hash")
	}
	return Hash{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}, nil
}

func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func mul128(a, b uint64) (hi, lo uint64) { return bits.Mul64(a, b) }

func mulFold64(a, b uint64) uint64 { hi, lo := bits.Mul64(a, b); return hi ^ lo }

func avalanche64(h uint64) uint64 { // XXH64_avalanche
	h ^= h >> 33
	h *= p64_2
	h ^= h >> 29
	h *= p64_3
	h ^= h >> 32
	return h
}

func avalanche3(h uint64) uint64 { // XXH3_avalanche
	h ^= h >> 37
	h *= pMX1
	h ^= h >> 32
	return h
}

func mix16(in, sec []byte, seed uint64) uint64 {
	return mulFold64(le64(in)^(le64(sec)+seed), le64(in[8:])^(le64(sec[8:])-seed))
}

func mix32(acc Hash, in1, in2, sec []byte, seed uint64) Hash {
	acc.Lo += mix16(in1, sec, seed)
	acc.Lo ^= le64(in2) + le64(in2[8:])
	acc.Hi += mix16(in2, sec[16:], seed)
	acc.Hi ^= le64(in1) + le64(in1[8:])
	return acc
}

// Sum128 hashes b in one call.
func Sum128(b []byte) Hash {
	n := len(b)
	switch {
	case n <= 16:
		return len0to16(b)
	case n <= 128:
		return len17to128(b)
	case n <= midSizeMax:
		return len129to240(b)
	}
	return hashLong(b)
}

func len0to16(in []byte) Hash {
	s := kSecret[:]
	n := uint64(len(in))
	switch {
	case n > 8:
		bitflipl := le64(s[32:]) ^ le64(s[40:])
		bitfliph := le64(s[48:]) ^ le64(s[56:])
		inLo, inHi := le64(in), le64(in[n-8:])
		mHi, mLo := mul128(inLo^inHi^bitflipl, p64_1)
		mLo += (n - 1) << 54
		inHi ^= bitfliph
		mHi += inHi + uint64(uint32(inHi))*uint64(p32_2-1)
		mLo ^= bits.ReverseBytes64(mHi)
		hHi, hLo := mul128(mLo, p64_2)
		hHi += mHi * p64_2
		return Hash{avalanche3(hHi), avalanche3(hLo)}
	case n >= 4:
		inLo, inHi := le32(in), le32(in[n-4:])
		in64 := uint64(inLo) + uint64(inHi)<<32
		bitflip := le64(s[16:]) ^ le64(s[24:])
		mHi, mLo := mul128(in64^bitflip, p64_1+(n<<2))
		mHi += mLo << 1
		mLo ^= mHi >> 3
		mLo ^= mLo >> 35
		mLo *= pMX2
		mLo ^= mLo >> 28
		return Hash{avalanche3(mHi), mLo}
	case n > 0:
		c1, c2, c3 := uint32(in[0]), uint32(in[n>>1]), uint32(in[n-1])
		combl := c1<<16 | c2<<24 | c3 | uint32(n)<<8
		combh := bits.RotateLeft32(bits.ReverseBytes32(combl), 13)
		bitflipl := uint64(le32(s) ^ le32(s[4:]))
		bitfliph := uint64(le32(s[8:]) ^ le32(s[12:]))
		return Hash{avalanche64(uint64(combh) ^ bitfliph), avalanche64(uint64(combl) ^ bitflipl)}
	}
	return Hash{avalanche64(le64(s[80:]) ^ le64(s[88:])), avalanche64(le64(s[64:]) ^ le64(s[72:]))}
}

func finish(acc Hash, n uint64) Hash {
	lo := acc.Lo + acc.Hi
	hi := acc.Lo*p64_1 + acc.Hi*p64_4 + n*p64_2
	return Hash{0 - avalanche3(hi), avalanche3(lo)}
}

func len17to128(in []byte) Hash {
	s := kSecret[:]
	n := len(in)
	acc := Hash{Lo: uint64(n) * p64_1}
	if n > 32 {
		if n > 64 {
			if n > 96 {
				acc = mix32(acc, in[48:], in[n-64:], s[96:], 0)
			}
			acc = mix32(acc, in[32:], in[n-48:], s[64:], 0)
		}
		acc = mix32(acc, in[16:], in[n-32:], s[32:], 0)
	}
	acc = mix32(acc, in, in[n-16:], s, 0)
	return finish(acc, uint64(n))
}

func len129to240(in []byte) Hash {
	s := kSecret[:]
	n := len(in)
	acc := Hash{Lo: uint64(n) * p64_1}
	for i := 32; i < 160; i += 32 {
		acc = mix32(acc, in[i-32:], in[i-16:], s[i-32:], 0)
	}
	acc.Lo, acc.Hi = avalanche3(acc.Lo), avalanche3(acc.Hi)
	for i := 160; i <= n; i += 32 {
		acc = mix32(acc, in[i-32:], in[i-16:], s[midStartOff+i-160:], 0)
	}
	acc = mix32(acc, in[n-16:], in[n-32:], s[secretSizeMin-midLastOff-16:], 0)
	return finish(acc, uint64(n))
}

func accumulate512(acc *[8]uint64, in, sec []byte) {
	for i := 0; i < 8; i++ {
		dv := le64(in[8*i:])
		dk := dv ^ le64(sec[8*i:])
		acc[i^1] += dv
		acc[i] += uint64(uint32(dk)) * (dk >> 32)
	}
}

func accumulate(acc *[8]uint64, in, sec []byte, stripes int) {
	for n := 0; n < stripes; n++ {
		accumulate512(acc, in[n*stripeLen:], sec[n*secretRate:])
	}
}

func scramble(acc *[8]uint64) {
	sec := kSecret[secretLimit:]
	for i := 0; i < 8; i++ {
		a := acc[i]
		a ^= a >> 47
		a ^= le64(sec[8*i:])
		a *= p32_1
		acc[i] = a
	}
}

func initAcc() [8]uint64 {
	return [8]uint64{p32_3, p64_1, p64_2, p64_3, p64_4, p32_2, p64_5, p32_1}
}

func mergeAccs(acc *[8]uint64, sec []byte, start uint64) uint64 {
	r := start
	for i := 0; i < 4; i++ {
		r += mulFold64(acc[2*i]^le64(sec[16*i:]), acc[2*i+1]^le64(sec[16*i+8:]))
	}
	return avalanche3(r)
}

func finalizeLong(acc *[8]uint64, n uint64) Hash {
	lo := mergeAccs(acc, kSecret[mergeAccsStart:], n*p64_1)
	hi := mergeAccs(acc, kSecret[secretSize-stripeLen-mergeAccsStart:], ^(n * p64_2))
	return Hash{hi, lo}
}

func hashLong(in []byte) Hash {
	acc := initAcc()
	n := len(in)
	blocks := (n - 1) / blockLen
	for b := 0; b < blocks; b++ {
		accumulate(&acc, in[b*blockLen:], kSecret[:], stripesPerBlk)
		scramble(&acc)
	}
	stripes := ((n - 1) - blockLen*blocks) / stripeLen
	accumulate(&acc, in[blocks*blockLen:], kSecret[:], stripes)
	accumulate512(&acc, in[n-stripeLen:], kSecret[secretSize-stripeLen-lastAccStart:])
	return finalizeLong(&acc, uint64(n))
}
