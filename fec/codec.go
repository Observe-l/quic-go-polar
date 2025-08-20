package fec

import (
	"errors"
)

// Scheme represents the FEC scheme.
type Scheme string

const (
	XOR   Scheme = "xor"
	RLC   Scheme = "rlc"
	Polar Scheme = "polar"
	None  Scheme = "none"
)

// Block encapsulates a coding block of k data chunks and r parity chunks.
type Block struct {
	ID       uint32
	K        int
	R        int
	ChunkLen int
}

// EncodeResult holds parity and (for RLC/Polar) coefficient metadata per parity.
type EncodeResult struct {
	Parity [][]byte // len == R, each of length ChunkLen
	Coeffs [][]byte // for RLC: len == R, each len K coefficients in GF(256)
	Meta   [][]byte // for Polar: per-parity metadata (e.g., row index bitmask); optional
}

// Encode produces parity chunks for a given scheme.
func Encode(s Scheme, blk Block, data [][]byte) (*EncodeResult, error) {
	if len(data) != blk.K {
		return nil, errors.New("fec: data length != K")
	}
	for i := range data {
		if len(data[i]) != blk.ChunkLen {
			return nil, errors.New("fec: data chunk length mismatch")
		}
	}
	switch s {
	case XOR:
		return encodeXOR(blk, data)
	case RLC:
		return encodeRLC(blk, data)
	case Polar:
		return encodePolar(blk, data)
	case None:
		return &EncodeResult{}, nil
	default:
		return nil, errors.New("fec: unknown scheme")
	}
}

// Recover attempts to reconstruct missing data chunks given received data and parity.
//
// present[i] indicates whether data[i] is present; missing entries in data are ignored.
// parity holds up to R parity chunks. For RLC, coeffs holds per-parity coefficients.
// For Polar, meta holds per-parity metadata (row descriptions).
// Returns number of recovered chunks.
func Recover(s Scheme, blk Block, data [][]byte, present []bool, parity [][]byte, coeffs [][]byte, meta [][]byte) (int, error) {
	switch s {
	case XOR:
		return recoverXOR(blk, data, present, parity)
	case RLC:
		return recoverRLC(blk, data, present, parity, coeffs)
	case Polar:
		return recoverPolar(blk, data, present, parity, meta)
	case None:
		return 0, nil
	default:
		return 0, errors.New("fec: unknown scheme")
	}
}

// Helpers
