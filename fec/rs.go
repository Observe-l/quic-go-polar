package fec

import "sort"

// Simple systematic RS over GF(256) using a Vandermonde generator.
// We implement packet-level RS: K data chunks, R parity chunks, each ChunkLen bytes.
// Encoding: parity_j = sum_i (alpha_j^i * data_i) over GF(256), where alpha_j are distinct non-zero field elements.
// Decoding: solve linear system for missing data using first m parity rows (m = #missing), assuming sufficient parity present.

// Choose evaluation points alpha_j = j+1 (1..R), avoiding 0.

func encodeRS(blk Block, data [][]byte) (*EncodeResult, error) {
	if blk.R == 0 {
		return &EncodeResult{}, nil
	}
	par := make([][]byte, blk.R)
	for j := 0; j < blk.R; j++ {
		p := make([]byte, blk.ChunkLen)
		alpha := expTable[j+1] // skip 1, use powers of a primitive element (2)
		// Compute powers alpha^i for i=0..K-1
		pow := byte(1)
		for i := 0; i < blk.K; i++ {
			gf256MulAcc(p, data[i], pow)
			pow = gf256Mul(pow, alpha)
		}
		par[j] = p
	}
	return &EncodeResult{Parity: par}, nil
}

func recoverRS(blk Block, data [][]byte, present []bool, parity [][]byte) (int, error) {
	// Identify missing data indices
	miss := make([]int, 0, blk.K)
	for i := 0; i < blk.K; i++ {
		if !present[i] {
			miss = append(miss, i)
		}
	}
	m := len(miss)
	if m == 0 {
		return 0, nil
	}
	// Sort missing indices to build a proper Vandermonde system
	sort.Ints(miss)
	// Collect available parity rows and their original indices j
	rows := make([]int, 0, blk.R)
	for j := 0; j < len(parity); j++ {
		if parity[j] != nil {
			rows = append(rows, j)
		}
	}
	if len(rows) < m {
		return 0, nil
	}
	rows = rows[:m]
	// Build A (m x m) with A[r,c] = (alpha_{rows[r]})^{miss[c]}, alpha_j = (j+1)
	A := make([][]byte, m)
	for r := 0; r < m; r++ {
		A[r] = make([]byte, m)
		alpha := expTable[rows[r]+1]
		pow := byte(1)
		idx := 0
		// iterate exponents 0..max(miss)
		maxexp := miss[m-1]
		for exp := 0; exp <= maxexp; exp++ {
			if idx < m && exp == miss[idx] {
				A[r][idx] = pow
				idx++
			}
			pow = gf256Mul(pow, alpha)
		}
	}
	// Build RHS b_r = parity[rows[r]] - sum_{i present} (alpha_{rows[r]})^i * data_i
	rhs := make([][]byte, m)
	for r := 0; r < m; r++ {
		j := rows[r]
		br := make([]byte, blk.ChunkLen)
		copy(br, parity[j])
		alpha := expTable[j+1]
		pow := byte(1)
		for i := 0; i < blk.K; i++ {
			if present[i] {
				gf256MulAcc(br, data[i], pow)
			}
			pow = gf256Mul(pow, alpha)
		}
		rhs[r] = br
	}
	invA, ok := gf256InvertMatrix(A)
	if !ok {
		return 0, nil
	}
	// Solve per byte position and fill missing data
	sols := make([][]byte, m)
	for j := 0; j < m; j++ {
		sols[j] = make([]byte, blk.ChunkLen)
	}
	for pos := 0; pos < blk.ChunkLen; pos++ {
		col := make([]byte, m)
		for r := 0; r < m; r++ {
			col[r] = rhs[r][pos]
		}
		x := gf256MatVec(invA, col)
		for i := 0; i < m; i++ {
			sols[i][pos] = x[i]
		}
	}
	for i := 0; i < m; i++ {
		idx := miss[i]
		data[idx] = sols[i]
		present[idx] = true
	}
	return m, nil
}
