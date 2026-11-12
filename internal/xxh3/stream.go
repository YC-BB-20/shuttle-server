package xxh3

import (
	"encoding/binary"
	"errors"
)

// Hasher hashes a stream. Its whole state is a few hundred bytes and can be
// saved with MarshalBinary and restored with UnmarshalBinary, so a hash can
// stop mid-file and continue later with an identical result.
type Hasher struct {
	acc          [8]uint64
	buf          [bufSize]byte
	buffered     int
	stripesSoFar int
	total        uint64
}

// New returns a fresh hasher.
func New() *Hasher { h := &Hasher{}; h.Reset(); return h }

// Reset starts over.
func (h *Hasher) Reset() { *h = Hasher{acc: initAcc()} }

// Size is the number of bytes hashed so far.
func (h *Hasher) Size() uint64 { return h.total }

func (h *Hasher) consume(acc *[8]uint64, soFar *int, in []byte, stripes int) []byte {
	sec := kSecret[*soFar*secretRate:]
	if stripes >= stripesPerBlk-*soFar {
		this := stripesPerBlk - *soFar
		for {
			accumulate(acc, in, sec, this)
			scramble(acc)
			in = in[this*stripeLen:]
			stripes -= this
			this = stripesPerBlk
			sec = kSecret[:]
			if stripes < stripesPerBlk {
				break
			}
		}
		*soFar = 0
	}
	if stripes > 0 {
		accumulate(acc, in, sec, stripes)
		in = in[stripes*stripeLen:]
		*soFar += stripes
	}
	return in
}

// Write adds p to the hash. It never fails.
func (h *Hasher) Write(p []byte) (int, error) {
	n := len(p)
	h.total += uint64(n)
	if n <= bufSize-h.buffered {
		copy(h.buf[h.buffered:], p)
		h.buffered += n
		return n, nil
	}
	if h.buffered > 0 {
		load := bufSize - h.buffered
		copy(h.buf[h.buffered:], p[:load])
		p = p[load:]
		h.consume(&h.acc, &h.stripesSoFar, h.buf[:], bufSize/stripeLen)
		h.buffered = 0
	}
	if len(p) > bufSize {
		stripes := (len(p) - 1) / stripeLen
		rest := h.consume(&h.acc, &h.stripesSoFar, p, stripes)
		consumed := len(p) - len(rest)
		copy(h.buf[bufSize-stripeLen:], p[consumed-stripeLen:consumed])
		p = rest
	}
	copy(h.buf[:], p)
	h.buffered = len(p)
	return n, nil
}

// Sum128 returns the hash of everything written so far. The hasher can keep
// going afterwards.
func (h *Hasher) Sum128() Hash {
	if h.total <= midSizeMax {
		return Sum128(h.buf[:h.total])
	}
	acc := h.acc
	var last []byte
	if h.buffered >= stripeLen {
		stripes := (h.buffered - 1) / stripeLen
		soFar := h.stripesSoFar
		h.consume(&acc, &soFar, h.buf[:], stripes)
		last = h.buf[h.buffered-stripeLen : h.buffered]
	} else {
		var tmp [stripeLen]byte
		catchup := stripeLen - h.buffered
		copy(tmp[:], h.buf[bufSize-catchup:])
		copy(tmp[catchup:], h.buf[:h.buffered])
		last = tmp[:]
	}
	accumulate512(&acc, last, kSecret[secretLimit-lastAccStart:])
	return finalizeLong(&acc, h.total)
}

// State format: magic "XH3S", version 1, then fields little-endian.
const (
	stateMagic   = "XH3S"
	stateVersion = 1
	stateSize    = 4 + 1 + 8*8 + bufSize + 4 + 4 + 8
)

// ErrState means a saved state is damaged or from another version; the hash
// must start again from the beginning of the file.
var ErrState = errors.New("saved hash state not usable; start this file again")

// MarshalBinary saves the state.
func (h *Hasher) MarshalBinary() ([]byte, error) {
	b := make([]byte, 0, stateSize)
	b = append(b, stateMagic...)
	b = append(b, stateVersion)
	for _, a := range h.acc {
		b = binary.LittleEndian.AppendUint64(b, a)
	}
	b = append(b, h.buf[:]...)
	b = binary.LittleEndian.AppendUint32(b, uint32(h.buffered))
	b = binary.LittleEndian.AppendUint32(b, uint32(h.stripesSoFar))
	b = binary.LittleEndian.AppendUint64(b, h.total)
	return b, nil
}

// UnmarshalBinary restores a saved state, refusing anything inconsistent.
func (h *Hasher) UnmarshalBinary(b []byte) error {
	if len(b) != stateSize || string(b[:4]) != stateMagic || b[4] != stateVersion {
		return ErrState
	}
	var n Hasher
	p := b[5:]
	for i := range n.acc {
		n.acc[i] = binary.LittleEndian.Uint64(p[8*i:])
	}
	p = p[64:]
	copy(n.buf[:], p[:bufSize])
	p = p[bufSize:]
	n.buffered = int(binary.LittleEndian.Uint32(p))
	n.stripesSoFar = int(binary.LittleEndian.Uint32(p[4:]))
	n.total = binary.LittleEndian.Uint64(p[8:])
	if n.buffered > bufSize || n.stripesSoFar >= stripesPerBlk || uint64(n.buffered) > n.total {
		return ErrState
	}
	*h = n
	return nil
}
