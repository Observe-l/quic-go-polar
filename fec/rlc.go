package fec

import "crypto/rand"

// --- RLC over GF(256) --- //

func encodeRLC(blk Block, data [][]byte) (*EncodeResult, error) {
	if blk.R == 0 {
		return &EncodeResult{}, nil
	}
	coeffs := make([][]byte, blk.R)
	par := make([][]byte, blk.R)
	for r := 0; r < blk.R; r++ {
		coeffs[r] = make([]byte, blk.K)
		// random coefficients, but avoid all-zero
		for {
			if _, err := rand.Read(coeffs[r]); err != nil {
				return nil, err
			}
			nonzero := false
			for _, c := range coeffs[r] {
				if c != 0 {
					nonzero = true
					break
				}
			}
			if nonzero {
				break
			}
		}
		p := make([]byte, blk.ChunkLen)
		for i := 0; i < blk.K; i++ {
			gf256MulAcc(p, data[i], coeffs[r][i])
		}
		par[r] = p
	}
	return &EncodeResult{Parity: par, Coeffs: coeffs}, nil
}

func recoverRLC(blk Block, data [][]byte, present []bool, parity [][]byte, coeffs [][]byte) (int, error) {
	// Setup linear system A*x = b over GF(256), where x are missing chunks byte-wise
	missingIdx := make([]int, 0)
	for i := 0; i < blk.K; i++ {
		if !present[i] {
			missingIdx = append(missingIdx, i)
		}
	}
	m := len(missingIdx)
	if m == 0 {
		return 0, nil
	}
	if m > len(parity) { // insufficient equations
		return 0, nil
	}
	// Build matrix A (m x m)
	A := make([][]byte, m)
	for i := 0; i < m; i++ {
		A[i] = make([]byte, m)
		for j := 0; j < m; j++ {
			A[i][j] = coeffs[i][missingIdx[j]]
		}
	}
	// Precompute RHS: parity minus known contributions
	rhs := make([][]byte, m)
	for i := 0; i < m; i++ {
		r := make([]byte, blk.ChunkLen)
		copy(r, parity[i])
		for j := 0; j < blk.K; j++ {
			if present[j] {
				gf256MulAcc(r, data[j], coeffs[i][j])
			}
		}
		rhs[i] = r
	}
	invA, ok := gf256InvertMatrix(A)
	if !ok {
		return 0, nil
	}
	// Solve per byte position
	sols := make([][]byte, m)
	for j := 0; j < m; j++ {
		sols[j] = make([]byte, blk.ChunkLen)
	}
	for pos := 0; pos < blk.ChunkLen; pos++ {
		col := make([]byte, m)
		for i := 0; i < m; i++ {
			col[i] = rhs[i][pos]
		}
		x := gf256MatVec(invA, col)
		for i := 0; i < m; i++ {
			sols[i][pos] = x[i]
		}
	}
	for i := 0; i < m; i++ {
		idx := missingIdx[i]
		data[idx] = sols[i]
		present[idx] = true
	}
	return m, nil
}

// GF(256) arithmetic backed by a fixed polynomial (0x11b) provided in gf256.go
func gf256MulAcc(dst, src []byte, c byte) {
	if c == 0 {
		return
	}
	for i := range dst {
		dst[i] ^= gf256Mul(src[i], c)
	}
}

func gf256InvertMatrix(A [][]byte) ([][]byte, bool) {
	n := len(A)
	aug := make([][]byte, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]byte, n*2)
		copy(aug[i][:n], A[i])
		aug[i][n+i] = 1
	}
	row := 0
	for col := 0; col < n && row < n; col++ {
		pivot := -1
		for r := row; r < n; r++ {
			if aug[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot == -1 {
			continue
		}
		aug[row], aug[pivot] = aug[pivot], aug[row]
		inv := gf256Inv(aug[row][col])
		for j := 0; j < 2*n; j++ {
			aug[row][j] = gf256Mul(aug[row][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == row {
				continue
			}
			factor := aug[r][col]
			if factor == 0 {
				continue
			}
			for j := 0; j < 2*n; j++ {
				aug[r][j] ^= gf256Mul(aug[row][j], factor)
			}
		}
		row++
	}
	if row < n {
		return nil, false
	}
	invA := make([][]byte, n)
	for i := 0; i < n; i++ {
		invA[i] = make([]byte, n)
		copy(invA[i], aug[i][n:])
	}
	return invA, true
}

func gf256MatVec(A [][]byte, x []byte) []byte {
	n := len(A)
	y := make([]byte, n)
	for i := 0; i < n; i++ {
		var v byte
		for j := 0; j < n; j++ {
			v ^= gf256Mul(A[i][j], x[j])
		}
		y[i] = v
	}
	return y
}
