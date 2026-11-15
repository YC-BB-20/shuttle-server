package copyjob

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
	"github.com/YC-BB-20/shuttle-server/internal/xxh64"
)

// Which hashes a copy makes (v0.2.58), chosen in Settings and kept with each
// job: XXH128 (the default, and every job before v0.2.58), XXH64, or both.
// Every read of a file in a job (C, S1, D1, S2, a Verify, a recheck) makes
// the same kind, so they compare as plain strings.
const (
	AlgoXXH128 = "xxh128"
	AlgoXXH64  = "xxh64"
	AlgoBoth   = "both"
)

// normAlgo turns a saved or sent choice into one of the three; anything
// else, including "" from older jobs and settings, is XXH128.
func normAlgo(a string) string {
	switch a {
	case AlgoXXH64, AlgoBoth:
		return a
	}
	return AlgoXXH128
}

// algoOf says which kind made a stored hash: 32 hex digits are XXH128, 16
// are XXH64, and "XXH128+XXH64" is both. A Verify reads each file with the
// kind its record holds, so copies made with different choices all verify.
func algoOf(h, fallback string) string {
	switch {
	case strings.Contains(h, "+"):
		return AlgoBoth
	case len(h) == 16:
		return AlgoXXH64
	case len(h) == 32:
		return AlgoXXH128
	}
	return normAlgo(fallback)
}

// sum is the result of one read: the hashes the job's choice asks for.
// It is comparable, so two reads can be compared with ==.
type sum struct {
	h128          xxh3.Hash
	h64           uint64
	has128, has64 bool
}

// String is how a sum is stored and compared: XXH128 as 32 hex digits (as
// before v0.2.58), XXH64 as 16, both as "XXH128+XXH64".
func (s sum) String() string {
	switch {
	case s.has128 && s.has64:
		return s.h128.String() + "+" + fmt.Sprintf("%016x", s.h64)
	case s.has64:
		return fmt.Sprintf("%016x", s.h64)
	case s.has128:
		return s.h128.String()
	}
	return ""
}

// hasher makes the hashes of one algo choice over the same bytes.
type hasher struct {
	h *xxh3.Hasher
	x *xxh64.Digest
}

func newHasher(algo string) *hasher {
	var hs hasher
	switch normAlgo(algo) {
	case AlgoXXH64:
		hs.x = xxh64.New()
	case AlgoBoth:
		hs.h, hs.x = xxh3.New(), xxh64.New()
	default:
		hs.h = xxh3.New()
	}
	return &hs
}

func (hs *hasher) Reset() {
	if hs.h != nil {
		hs.h.Reset()
	}
	if hs.x != nil {
		hs.x.Reset()
	}
}

func (hs *hasher) Write(b []byte) {
	if hs.h != nil {
		hs.h.Write(b)
	}
	if hs.x != nil {
		hs.x.Write(b)
	}
}

func (hs *hasher) Size() uint64 {
	if hs.h != nil {
		return hs.h.Size()
	}
	return hs.x.Size()
}

func (hs *hasher) Sum() sum {
	var s sum
	if hs.h != nil {
		s.h128, s.has128 = hs.h.Sum128(), true
	}
	if hs.x != nil {
		s.h64, s.has64 = hs.x.Sum64(), true
	}
	return s
}

// bothMagic starts the saved state of a hasher making both kinds. A hasher
// of one kind saves exactly that kind's state, so XXH128 checkpoints from
// before v0.2.58 still resume. A state of another kind fails to load, and
// that file's read starts again from its beginning.
var bothMagic = []byte("SHB1")

func (hs *hasher) MarshalBinary() ([]byte, error) {
	switch {
	case hs.h != nil && hs.x != nil:
		a, err := hs.h.MarshalBinary()
		if err != nil {
			return nil, err
		}
		b, err := hs.x.MarshalBinary()
		if err != nil {
			return nil, err
		}
		out := append([]byte{}, bothMagic...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(a)))
		out = append(out, a...)
		return append(out, b...), nil
	case hs.h != nil:
		return hs.h.MarshalBinary()
	}
	return hs.x.MarshalBinary()
}

func (hs *hasher) UnmarshalBinary(b []byte) error {
	both := len(b) >= 8 && string(b[:4]) == string(bothMagic)
	switch {
	case hs.h != nil && hs.x != nil:
		if !both {
			return errors.New("saved hash state is of another kind")
		}
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if 8+n > len(b) {
			return errors.New("saved hash state is damaged")
		}
		if err := hs.h.UnmarshalBinary(b[8 : 8+n]); err != nil {
			return err
		}
		if err := hs.x.UnmarshalBinary(b[8+n:]); err != nil {
			return err
		}
		if hs.h.Size() != hs.x.Size() {
			return errors.New("saved hash state is damaged")
		}
		return nil
	case both:
		return errors.New("saved hash state is of another kind")
	case hs.h != nil:
		return hs.h.UnmarshalBinary(b)
	}
	return hs.x.UnmarshalBinary(b)
}
