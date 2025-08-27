package fec

import (
	"errors"
	"math"
)

// Packet-level Polar over GF(2). Systematic code with K sources and R=N-K parities.

type PacketPolarParams struct {
	N, K    int
	Epsilon float64
	MaxLen  int

	// Derived
	n    int
	R    int
	A    []int      // info set indices in [0..N-1], ascending
	Ac   []int      // parity set indices
	Gpar [][]uint64 // R rows, each a bitset over K sources (columns)
}

// NewPacketPolarParams builds params for packet-level polar code at given N,K (N must be power of two).
func NewPacketPolarParams(N, K int, eps float64, maxLen int) (*PacketPolarParams, error) {
	if N <= 0 || K <= 0 || K >= N {
		return nil, errors.New("invalid N,K")
	}
	// check power of two
	if N&(N-1) != 0 {
		return nil, errors.New("N must be power of two")
	}
	// n = log2 N
	n := 0
	for (1 << n) < N {
		n++
	}
	R := N - K
	// 1) Choose info set A via BEC Bhattacharyya recursion
	z := bhattacharyyaBEC(N, eps)
	idx := make([]int, N)
	for i := range idx {
		idx[i] = i
	}
	// partial sort: pick K smallest Z
	// simple selection sort for small N
	for i := 0; i < K; i++ {
		minj := i
		for j := i + 1; j < N; j++ {
			if z[idx[j]] < z[idx[minj]] {
				minj = j
			}
		}
		idx[i], idx[minj] = idx[minj], idx[i]
	}
	A := append([]int(nil), idx[:K]...)
	// sort A ascending for stable column order
	for i := 0; i < K; i++ {
		for j := i + 1; j < K; j++ {
			if A[j] < A[i] {
				A[i], A[j] = A[j], A[i]
			}
		}
	}
	// Build Ac as the complement, ascending
	inA := make([]bool, N)
	for _, v := range A {
		inA[v] = true
	}
	Ac := make([]int, 0, R)
	for i := 0; i < N; i++ {
		if !inA[i] {
			Ac = append(Ac, i)
		}
	}
	// 2) Build G_N (N x N)
	G := polarGeneratorBool(n)
	// 3) Extract G_AA (KxK) and G_AAc (KxR)
	GAA := make([][]bool, K)
	GAAc := make([][]bool, K)
	for i := 0; i < K; i++ {
		row := make([]bool, K)
		for j := 0; j < K; j++ {
			row[j] = G[A[i]][A[j]]
		}
		GAA[i] = row
		row2 := make([]bool, R)
		for j := 0; j < R; j++ {
			row2[j] = G[A[i]][Ac[j]]
		}
		GAAc[i] = row2
	}
	// 4) inv(G_AA)
	invGAA, ok := invertBoolMatrixGF2(GAA)
	if !ok {
		return nil, errors.New("G_AA not invertible")
	}
	// 5) P = inv(G_AA) * G_AAc  => (KxK)*(KxR) = KxR
	P := make([][]bool, K)
	for i := 0; i < K; i++ {
		row := make([]bool, R)
		for j := 0; j < R; j++ {
			s := false
			for t := 0; t < K; t++ {
				if invGAA[i][t] && GAAc[t][j] {
					s = !s
				}
			}
			row[j] = s
		}
		P[i] = row
	}
	// 6) Build Gpar rows (R rows), row j is column j of P (size K)
	wordsK := (K + 63) / 64
	Gpar := make([][]uint64, R)
	for j := 0; j < R; j++ {
		row := make([]uint64, wordsK)
		for i := 0; i < K; i++ {
			if P[i][j] {
				row[i>>6] |= 1 << uint(i&63)
			}
		}
		Gpar[j] = row
	}

	return &PacketPolarParams{N: N, K: K, Epsilon: eps, MaxLen: maxLen, n: n, R: R, A: A, Ac: Ac, Gpar: Gpar}, nil
}

// Encode produces R parity packets (with indices K..N-1 in the canonical order).
func PacketPolarEncode(p *PacketPolarParams, src [][]byte) (parity [][]byte) {
	if len(src) != p.K {
		panic("src length must equal K")
	}
	L := p.MaxLen
	parity = make([][]byte, p.R)
	for r := 0; r < p.R; r++ {
		out := make([]byte, L)
		mask := p.Gpar[r]
		for i := 0; i < p.K; i++ {
			if (mask[i>>6]>>uint(i&63))&1 == 1 {
				xorBytes(out, src[i])
			}
		}
		parity[r] = out
	}
	return parity
}

type Packet struct {
	Index int
	Data  []byte
}

// Decode attempts to recover all K sources from m received packets.
// Canonical indexing: source i has Index=i (0..K-1), parity j has Index=K+j (0..R-1).
// Returns recovered sources in index order [0..K-1].
func PacketPolarDecode(p *PacketPolarParams, recv []Packet) ([][]byte, bool) {
	// Build measurement matrix rows (m x K) and attach data for in-place elimination.
	m := len(recv)
	wordsK := (p.K + 63) / 64
	type row struct {
		vec  []uint64
		data []byte
	}
	rows := make([]row, 0, m)
	for _, pkt := range recv {
		v := make([]uint64, wordsK)
		if pkt.Index < p.K {
			// source row: e_i
			i := pkt.Index
			v[i>>6] |= 1 << uint(i&63)
		} else {
			j := pkt.Index - p.K
			if j < 0 || j >= p.R {
				// invalid index; skip
				continue
			}
			// parity row
			copy(v, p.Gpar[j])
		}
		dataCopy := make([]byte, p.MaxLen)
		copy(dataCopy, pkt.Data)
		rows = append(rows, row{vec: v, data: dataCopy})
	}
	// Gaussian elimination over GF(2), track pivot rows by column
	pivRow := make([]int, p.K)
	for i := range pivRow {
		pivRow[i] = -1
	}
	r := 0
	for c := 0; c < p.K && r < len(rows); c++ {
		// find pivot row with bit in column c
		pr := -1
		for i := r; i < len(rows); i++ {
			if ((rows[i].vec[c>>6] >> uint(c&63)) & 1) == 1 {
				pr = i
				break
			}
		}
		if pr == -1 {
			continue
		}
		rows[r], rows[pr] = rows[pr], rows[r]
		pivRow[c] = r
		// eliminate other rows in column c
		for i := 0; i < len(rows); i++ {
			if i == r {
				continue
			}
			if ((rows[i].vec[c>>6] >> uint(c&63)) & 1) == 1 {
				// vec xor
				for w := 0; w < wordsK; w++ {
					rows[i].vec[w] ^= rows[r].vec[w]
				}
				// data xor
				xorBytes(rows[i].data, rows[r].data)
			}
		}
		r++
	}
	// check full rank
	for c := 0; c < p.K; c++ {
		if pivRow[c] == -1 {
			return nil, false
		}
	}
	// Extract sources: row with pivot at column i holds source i data
	out := make([][]byte, p.K)
	for i := 0; i < p.K; i++ {
		out[i] = make([]byte, p.MaxLen)
		copy(out[i], rows[pivRow[i]].data)
	}
	return out, true
}

// Utilities

func polarGeneratorBool(n int) [][]bool {
	G := [][]bool{{true}}
	for t := 0; t < n; t++ {
		a := len(G)
		b := len(G[0])
		NG := make([][]bool, 2*a)
		for i := 0; i < 2*a; i++ {
			NG[i] = make([]bool, 2*b)
		}
		for i := 0; i < a; i++ {
			for j := 0; j < b; j++ {
				NG[i][j] = G[i][j]
				NG[a+i][j] = G[i][j]
				NG[a+i][b+j] = G[i][j]
				// top-right zeros
			}
		}
		G = NG
	}
	return G
}

func bhattacharyyaBEC(N int, eps float64) []float64 {
	// iterative breadth-first over stages
	// Start with length-1 Z=eps, expand n times with transforms [2z - z^2, z^2]
	levels := [][]float64{{eps}}
	for len(levels) < int(math.Log2(float64(N)))+1 {
		prev := levels[len(levels)-1]
		next := make([]float64, 0, len(prev)*2)
		for _, z := range prev {
			z1 := 2*z - z*z
			z2 := z * z
			next = append(next, z1, z2)
		}
		levels = append(levels, next)
	}
	return levels[len(levels)-1]
}
