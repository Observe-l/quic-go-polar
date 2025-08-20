package fec

// --- XOR --- //

func encodeXOR(blk Block, data [][]byte) (*EncodeResult, error) {
	if blk.R < 1 {
		return &EncodeResult{Parity: nil}, nil
	}
	p := make([]byte, blk.ChunkLen)
	for i := 0; i < blk.K; i++ {
		xorBytes(p, data[i])
	}
	// provide only 1 parity for XOR; if R>1, repeat
	out := make([][]byte, blk.R)
	for i := range out {
		b := make([]byte, blk.ChunkLen)
		copy(b, p)
		out[i] = b
	}
	return &EncodeResult{Parity: out}, nil
}

func recoverXOR(blk Block, data [][]byte, present []bool, parity [][]byte) (int, error) {
	// can only recover if exactly 1 is missing and at least 1 parity is available
	if len(parity) == 0 {
		return 0, nil
	}
	missIdx := -1
	for i := 0; i < blk.K; i++ {
		if !present[i] {
			if missIdx >= 0 { // more than one missing
				return 0, nil
			}
			missIdx = i
		}
	}
	if missIdx < 0 {
		return 0, nil
	}
	rec := make([]byte, blk.ChunkLen)
	copy(rec, parity[0])
	for i := 0; i < blk.K; i++ {
		if i == missIdx {
			continue
		}
		xorBytes(rec, data[i])
	}
	data[missIdx] = rec
	present[missIdx] = true
	return 1, nil
}

// byte-wise XOR helper used by multiple schemes
func xorBytes(dst, src []byte) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}
