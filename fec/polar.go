package fec

// --- Polar (structured GF(2) linear code) --- //

// For simplicity, implement a deterministic linear code over GF(2) using rows from G_N = F^{\u2297 n}, with N >= K+R.
// We'll pick the last R rows as parity equations.
func encodePolar(blk Block, data [][]byte) (*EncodeResult, error) {
	N := nextPow2(blk.K + blk.R)
	G := polarGenerator(nLog2(N))
	par := make([][]byte, blk.R)
	meta := make([][]byte, blk.R)
	for r := 0; r < blk.R; r++ {
		row := G[blk.K+r]
		mask := make([]byte, (blk.K+7)/8)
		for i := 0; i < blk.K; i++ {
			if row[i] {
				mask[i/8] |= 1 << (uint(i) % 8)
			}
		}
		meta[r] = mask
		p := make([]byte, blk.ChunkLen)
		for i := 0; i < blk.K; i++ {
			if row[i] {
				xorBytes(p, data[i])
			}
		}
		par[r] = p
	}
	return &EncodeResult{Parity: par, Meta: meta}, nil
}

func recoverPolar(blk Block, data [][]byte, present []bool, parity [][]byte, meta [][]byte) (int, error) {
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
	if m > len(parity) {
		return 0, nil
	}
	A := make([][]bool, m)
	for i := 0; i < m; i++ {
		A[i] = make([]bool, m)
		rowMask := meta[i]
		for j := 0; j < m; j++ {
			bit := (rowMask[missingIdx[j]/8] >> (uint(missingIdx[j]) % 8)) & 1
			A[i][j] = bit == 1
		}
	}
	invA, ok := invertBoolMatrixGF2(A)
	if !ok {
		return 0, nil
	}
	rhs := make([][]byte, m)
	for i := 0; i < m; i++ {
		r := make([]byte, blk.ChunkLen)
		copy(r, parity[i])
		rowMask := meta[i]
		for j := 0; j < blk.K; j++ {
			if present[j] {
				bit := (rowMask[j/8] >> (uint(j) % 8)) & 1
				if bit == 1 {
					xorBytes(r, data[j])
				}
			}
		}
		rhs[i] = r
	}
	sols := make([][]byte, m)
	for j := 0; j < m; j++ {
		sols[j] = make([]byte, blk.ChunkLen)
	}
	for pos := 0; pos < blk.ChunkLen; pos++ {
		for bit := 0; bit < 8; bit++ {
			col := make([]bool, m)
			for i := 0; i < m; i++ {
				col[i] = ((rhs[i][pos] >> uint(bit)) & 1) == 1
			}
			x := matVecGF2(invA, col)
			for i := 0; i < m; i++ {
				if x[i] {
					sols[i][pos] |= 1 << uint(bit)
				}
			}
		}
	}
	for i := 0; i < m; i++ {
		idx := missingIdx[i]
		data[idx] = sols[i]
		present[idx] = true
	}
	return m, nil
}

func nextPow2(x int) int {
	if x <= 1 {
		return 1
	}
	p := 1
	for p < x {
		p <<= 1
	}
	return p
}

func nLog2(n int) int {
	p := 0
	for (1 << p) < n {
		p++
	}
	return p
}

// polarGenerator builds G_N = F^{\u2297 n} where F=[[1,0],[1,1]].
func polarGenerator(n int) [][]bool {
	G := [][]bool{{true}}
	for t := 0; t < n; t++ {
		// Kronecker product with F
		a := len(G)
		b := len(G[0])
		NG := make([][]bool, 2*a)
		for i := 0; i < 2*a; i++ {
			NG[i] = make([]bool, 2*b)
		}
		for i := 0; i < a; i++ {
			for j := 0; j < b; j++ {
				NG[i][j] = G[i][j]     // top-left: 1*G
				NG[a+i][j] = G[i][j]   // bottom-left: 1*G
				NG[a+i][b+j] = G[i][j] // bottom-right: 1*G
				// top-right: 0*G -> false
			}
		}
		G = NG
	}
	return G
}

func invertBoolMatrixGF2(A [][]bool) ([][]bool, bool) {
	n := len(A)
	aug := make([][]bool, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]bool, 2*n)
		copy(aug[i][:n], A[i])
		aug[i][n+i] = true
	}
	row := 0
	for col := 0; col < n && row < n; col++ {
		pivot := -1
		for r := row; r < n; r++ {
			if aug[r][col] {
				pivot = r
				break
			}
		}
		if pivot == -1 {
			continue
		}
		aug[row], aug[pivot] = aug[pivot], aug[row]
		for r := 0; r < n; r++ {
			if r == row {
				continue
			}
			if aug[r][col] {
				for j := 0; j < 2*n; j++ {
					aug[r][j] = aug[r][j] != aug[row][j]
				}
			}
		}
		row++
	}
	if row < n {
		return nil, false
	}
	inv := make([][]bool, n)
	for i := 0; i < n; i++ {
		inv[i] = make([]bool, n)
		copy(inv[i], aug[i][n:])
	}
	return inv, true
}

func matVecGF2(A [][]bool, x []bool) []bool {
	n := len(A)
	y := make([]bool, n)
	for i := 0; i < n; i++ {
		var b bool
		for j := 0; j < n; j++ {
			if A[i][j] && x[j] {
				b = !b
			}
		}
		y[i] = b
	}
	return y
}
