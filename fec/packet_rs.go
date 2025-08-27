package fec

import "errors"

// EncodeRS generates R parity packets using a Vandermonde generator over GF(256) in systematic form.
// We use evaluation points alpha^j for j=0..R-1 and build parity rows accordingly.
func EncodeRS(src [][]byte, K, R int) ([]Packet, error) {
	if K <= 0 || R < 0 {
		return nil, errors.New("bad K,R")
	}
	L := 0
	if len(src) > 0 {
		L = len(src[0])
	}
	out := make([]Packet, R)
	// For parity index j (0..R-1), parity = sum_i (alpha^{j*i}) * src[i]
	for j := 0; j < R; j++ {
		y := make([]byte, L)
		for i := 0; i < K; i++ {
			coef := alphaPow(j * i)
			gfMulBytes(y, src[i], coef)
		}
		out[j] = Packet{Index: K + j, Data: y}
	}
	return out, nil
}

// DecodeRS recovers sources from any K of N=K+R packets.
// recv includes a mix of sources (Index<i K) and parity (Index >= K).
func DecodeRS(recv []Packet, K, R int) ([][]byte, bool) {
	if len(recv) < K {
		return nil, false
	}
	L := 0
	for _, p := range recv {
		if p.Data != nil {
			L = len(p.Data)
			break
		}
	}
	if L == 0 {
		return nil, false
	}
	// Build KxK matrix A and RHS vector for each byte position via packets.
	// We'll construct A with rows corresponding to received packets; for a source i row is e_i,
	// for a parity j row is [alpha^{j*0}, alpha^{j*1}, ..., alpha^{j*(K-1)}].
	type row struct {
		vec  []byte
		data []byte
	}
	rows := make([]row, 0, len(recv))
	for _, p := range recv {
		if p.Index < K {
			v := make([]byte, K)
			v[p.Index] = 1
			d := make([]byte, L)
			copy(d, p.Data)
			rows = append(rows, row{v, d})
		} else {
			j := p.Index - K
			if j < 0 || j >= R {
				continue
			}
			v := make([]byte, K)
			for i := 0; i < K; i++ {
				v[i] = alphaPow(j * i)
			}
			d := make([]byte, L)
			copy(d, p.Data)
			rows = append(rows, row{v, d})
		}
		if len(rows) == K {
			break
		}
	}
	if len(rows) < K {
		return nil, false
	}
	// Gaussian elimination over GF(256)
	for c, r := 0, 0; c < K && r < K; c++ {
		pr := -1
		for i := r; i < K; i++ {
			if rows[i].vec[c] != 0 {
				pr = i
				break
			}
		}
		if pr == -1 {
			continue
		}
		rows[r], rows[pr] = rows[pr], rows[r]
		inv := gfInv(rows[r].vec[c])
		for j := 0; j < K; j++ {
			rows[r].vec[j] = gfMul(rows[r].vec[j], inv)
		}
		for j := 0; j < L; j++ {
			rows[r].data[j] = gfMul(rows[r].data[j], inv)
		}
		for i := 0; i < K; i++ {
			if i == r {
				continue
			}
			a := rows[i].vec[c]
			if a == 0 {
				continue
			}
			for j := 0; j < K; j++ {
				rows[i].vec[j] = rows[i].vec[j] ^ gfMul(a, rows[r].vec[j])
			}
			// data_i ^= a * data_r
			for j := 0; j < L; j++ {
				rows[i].data[j] ^= gfMul(a, rows[r].data[j])
			}
		}
		r++
	}
	// extract solutions where vec == e_i
	out := make([][]byte, K)
	for i := 0; i < K; i++ {
		found := -1
		for j := 0; j < K; j++ {
			ok := true
			for c := 0; c < K; c++ {
				want := byte(0)
				if c == i {
					want = 1
				}
				if rows[j].vec[c] != want {
					ok = false
					break
				}
			}
			if ok {
				found = j
				break
			}
		}
		if found == -1 {
			return nil, false
		}
		out[i] = rows[found].data
	}
	return out, true
}
