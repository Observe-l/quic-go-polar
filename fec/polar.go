package fec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"math/bits"
	"math/rand"
	"os"
)

// cache for info columns of G to avoid recomputation across batches
var (
	cachedGcols      [][]bool
	cachedInfoCols   []int
	cachedPolarN     int
	cachedPackedCols [][]uint64
	// Inverse cache for multi-packet erasure keyed by loss mask and info size
	invCacheMap = make(map[struct {
		mask  uint32
		kinfo int
	}]struct {
		inv      [][]uint64
		usedRows []int
	})
	// Cached random map to keep interleaver stable across batches (enables cache hits)
	cachedRandMap  []int
	cachedRandMapK int
	// Cached interleave plan derived from randomMap for faster packet assembly
	cachedPlanMap []int
	cachedPlanK   int
	cachedPlan    [][][8]struct {
		srcByte int
		srcMask byte
	}
	// Packet-level LUT: for each message byte position (by) and byte value v (0..255),
	// a 128-byte slice representing the XOR contribution to all K packet bytes across subsets
	// (concatenated in subset order; each subset contributes subsetBytes = 1024/(8*K) bytes).
	cachedPacketLUT       [][]byte // indexed as [by*256 + v]
	cachedPacketLUTN      int
	cachedPacketLUTBits   int
	cachedPacketLUTK      int
	cachedPacketLUTMapRef []int
)

func getInfoColsAndG(n int, encodingIndex []int, numDataBits int) ([][]bool, []int) {
	if cachedGcols != nil && cachedPolarN == n && len(cachedInfoCols) == numDataBits {
		return cachedGcols, cachedInfoCols
	}
	infoCols := make([]int, numDataBits)
	for i := 0; i < numDataBits; i++ {
		infoCols[i] = bitReverseN(encodingIndex[i], n)
	}
	N := 1 << n
	Gcols := make([][]bool, numDataBits)
	for c := 0; c < numDataBits; c++ {
		j := infoCols[c]
		u := make([]bool, N)
		u[j] = true
		for s := 0; s < n; s++ {
			block := 1 << (s + 1)
			half := 1 << s
			for start := 0; start < N; start += block {
				for k := 0; k < half; k++ {
					i1 := start + k
					i2 := i1 + half
					u[i1] = u[i1] != u[i2]
				}
			}
		}
		Gcols[c] = u
	}
	cachedGcols = Gcols
	cachedInfoCols = infoCols
	cachedPolarN = n
	// Invalidate packed cache when the shape changes
	cachedPackedCols = nil
	return Gcols, infoCols
}

// getPackedInfoCols returns the packed columns (uint64 words) for fast encoding.
func getPackedInfoCols(n int, encodingIndex []int, numDataBits int) ([][]uint64, []int) {
	if cachedPackedCols != nil && cachedPolarN == n && len(cachedInfoCols) == numDataBits {
		return cachedPackedCols, cachedInfoCols
	}
	Gcols, infoCols := getInfoColsAndG(n, encodingIndex, numDataBits)
	packed := make([][]uint64, numDataBits)
	for i := 0; i < numDataBits; i++ {
		packed[i] = packBoolVec(Gcols[i])
	}
	cachedPackedCols = packed
	return packed, infoCols
}

// getByteLUT builds a LUT per message byte position: for each possible byte value (0..255),
// precompute the XOR of the 8 corresponding packed columns. Layout: lut[pos][val*words + w].
// getByteLUT removed in favor of packet-level LUT fused encoder path

func getStableRandomMap(codewordBits, K int) []int {
	if cachedRandMap != nil && cachedRandMapK == K {
		return cachedRandMap
	}
	r := rand.New(rand.NewSource(1)) // fixed seed for stability
	m := r.Perm(codewordBits)
	cachedRandMap = m
	cachedRandMapK = K
	// Invalidate interleave plan cache
	cachedPlanMap = nil
	cachedPlan = nil
	return m
}

// buildInterleavePlan precomputes, for each subset and each subset byte position,
// the 8 (srcByte,srcMask) pairs to assemble that destination byte from the codeword bytes.
func buildInterleavePlan(randomMap []int, K int) [][][8]struct {
	srcByte int
	srcMask byte
} {
	if cachedPlanMap != nil && cachedPlanK == K && len(cachedPlanMap) == len(randomMap) {
		same := true
		for i := range randomMap {
			if randomMap[i] != cachedPlanMap[i] {
				same = false
				break
			}
		}
		if same {
			return cachedPlan
		}
	}
	const codewordBits = 1024
	subsetSizeBits := codewordBits / K
	subsetBytes := subsetSizeBits / 8
	plan := make([][][8]struct {
		srcByte int
		srcMask byte
	}, K)
	for s := 0; s < K; s++ {
		arr := make([][8]struct {
			srcByte int
			srcMask byte
		}, subsetBytes)
		for j := 0; j < subsetBytes; j++ {
			var e [8]struct {
				srcByte int
				srcMask byte
			}
			for b := 0; b < 8; b++ {
				destBit := s*subsetSizeBits + j*8 + b
				src := randomMap[destBit]
				e[b].srcByte = src >> 3
				e[b].srcMask = 1 << uint(src&7)
			}
			arr[j] = e
		}
		plan[s] = arr
	}
	cachedPlanMap = append([]int(nil), randomMap...)
	cachedPlanK = K
	cachedPlan = plan
	return plan
}

// computeMapCRC returns a CRC32 over the randomMap contents (LE uint32 per entry).
func computeMapCRC(randomMap []int) uint32 {
	h := crc32.NewIEEE()
	buf := make([]byte, 4)
	for _, v := range randomMap {
		u := uint32(v)
		buf[0] = byte(u)
		buf[1] = byte(u >> 8)
		buf[2] = byte(u >> 16)
		buf[3] = byte(u >> 24)
		_, _ = h.Write(buf)
	}
	return h.Sum32()
}

// getPacketLUT builds or returns a cached fused encode-to-packets LUT.
// For each message byte position 'by' and each byte value 'v', it precomputes the 128-byte
// contribution across all K subsets (concatenated), so encoding becomes XOR of these slices.
func getPacketLUT(n int, encodingIndex []int, numDataBits int, randomMap []int, K int) ([][]byte, error) {
	if cachedPacketLUT != nil && cachedPacketLUTN == n && cachedPacketLUTBits == numDataBits && cachedPacketLUTK == K &&
		cachedPacketLUTMapRef != nil && len(cachedPacketLUTMapRef) == len(randomMap) {
		same := true
		for i := range randomMap {
			if randomMap[i] != cachedPacketLUTMapRef[i] {
				same = false
				break
			}
		}
		if same {
			return cachedPacketLUT, nil
		}
	}
	const codewordBits = 1 << 10 // N=1024 for n=10
	if n != 10 {
		return nil, errors.New("only n=10 supported here")
	}
	if numDataBits%8 != 0 {
		return nil, errors.New("numDataBits must be multiple of 8")
	}
	msgBytes := numDataBits / 8
	packedCols, _ := getPackedInfoCols(n, encodingIndex, numDataBits)
	words := codewordBits / 64
	// Precompute mapping from destBit i to source word/mask via randomMap
	type wm struct {
		w int
		m uint64
	}
	wmt := make([]wm, codewordBits)
	for i := 0; i < codewordBits; i++ {
		src := randomMap[i]
		wmt[i] = wm{w: src >> 6, m: 1 << uint(src&63)}
	}
	lut := make([][]byte, msgBytes*256)
	acc := make([]uint64, words)
	for by := 0; by < msgBytes; by++ {
		base := by * 8
		for v := 0; v < 256; v++ {
			row := make([]byte, codewordBits/8)
			if v != 0 {
				// build acc = XOR of selected columns
				for i := 0; i < words; i++ {
					acc[i] = 0
				}
				vv := v
				for bit := 0; bit < 8; bit++ {
					if (vv>>bit)&1 == 1 {
						col := packedCols[base+bit]
						for w := 0; w < words; w++ {
							acc[w] ^= col[w]
						}
					}
				}
				// map to destination ordering and pack into bytes
				for i := 0; i < codewordBits; i++ {
					wm := wmt[i]
					if acc[wm.w]&wm.m != 0 {
						row[i>>3] |= 1 << uint(i&7)
					}
				}
			}
			lut[by*256+v] = row
		}
	}
	cachedPacketLUT = lut
	cachedPacketLUTN = n
	cachedPacketLUTBits = numDataBits
	cachedPacketLUTK = K
	cachedPacketLUTMapRef = append([]int(nil), randomMap...)
	return lut, nil
}

// LoadRandomMap reads a binary file of little-endian int64 entries for the random map.
func LoadRandomMap(filePath string, N int) ([]int, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if len(b)%8 != 0 {
		return nil, errors.New("random map file size invalid")
	}
	cnt := len(b) / 8
	if cnt != N {
		return nil, errors.New("random map length mismatch")
	}
	vals64 := make([]int64, cnt)
	if err := binary.Read(bytes.NewReader(b), binary.LittleEndian, &vals64); err != nil {
		return nil, err
	}
	vals := make([]int, cnt)
	for i, v := range vals64 {
		vals[i] = int(v)
	}
	return vals, nil
}

// SaveRandomMap writes the random map to a file as little-endian int64 values.
func SaveRandomMap(filePath string, m []int) error {
	vals64 := make([]int64, len(m))
	for i, v := range m {
		vals64[i] = int64(v)
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, vals64); err != nil {
		return err
	}
	return os.WriteFile(filePath, buf.Bytes(), 0o644)
}

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

// packBoolRows packs a boolean matrix into uint64 word rows (LSB-first within a word).

func packBoolVec(v []bool) []uint64 {
	w := (len(v) + 63) / 64
	out := make([]uint64, w)
	for i, b := range v {
		if b {
			out[i>>6] |= 1 << (uint(i) & 63)
		}
	}
	return out
}

// matVecGF2Packed computes y = M * v over GF(2), with M rows and K columns, packed in uint64.
// (matVecGF2Packed removed: unused)

// invertBoolMatrixGF2Packed inverts a KxK boolean matrix using packed 64-bit words.
func invertBoolMatrixGF2Packed(A [][]bool) ([][]uint64, bool) {
	K := len(A)
	if K == 0 {
		return nil, false
	}
	w := (K + 63) / 64
	// Build augmented matrix [A | I] packed into uint64 words per row (2*w words per row)
	aug := make([][]uint64, K)
	for i := 0; i < K; i++ {
		row := make([]uint64, 2*w)
		// left A
		for j, v := range A[i] {
			if v {
				row[j>>6] |= 1 << (uint(j) & 63)
			}
		}
		// right I
		row[w+(i>>6)] |= 1 << (uint(i) & 63)
		aug[i] = row
	}
	r := 0
	for c := 0; c < K && r < K; c++ {
		wordIdx := c >> 6
		bitMask := uint64(1) << (uint(c) & 63)
		// find pivot
		p := -1
		for i := r; i < K; i++ {
			if aug[i][wordIdx]&bitMask != 0 {
				p = i
				break
			}
		}
		if p == -1 {
			continue
		}
		// swap rows
		aug[r], aug[p] = aug[p], aug[r]
		// eliminate this column in all other rows
		for i := 0; i < K; i++ {
			if i == r {
				continue
			}
			if aug[i][wordIdx]&bitMask != 0 {
				for wj := 0; wj < 2*w; wj++ {
					aug[i][wj] ^= aug[r][wj]
				}
			}
		}
		r++
	}
	if r < K {
		return nil, false
	}
	// Extract right side as inverse
	inv := make([][]uint64, K)
	for i := 0; i < K; i++ {
		row := make([]uint64, w)
		copy(row, aug[i][w:])
		inv[i] = row
	}
	return inv, true
}

// selectPivotRows chooses K linearly independent rows from A (MxK) using Gaussian elimination,
// and returns their indices relative to A. Returns ok=false if rank < K.
func selectPivotRows(A [][]bool, K int) ([]int, bool) {
	M := len(A)
	if M < K {
		return nil, false
	}
	// Make a copy so we can eliminate without mutating the original rows.
	work := make([][]bool, M)
	idxs := make([]int, M)
	for i := 0; i < M; i++ {
		work[i] = append([]bool(nil), A[i]...)
		idxs[i] = i
	}
	r := 0
	pivots := make([]int, 0, K)
	for c := 0; c < K && r < M; c++ {
		p := -1
		for i := r; i < M; i++ {
			if work[i][c] {
				p = i
				break
			}
		}
		if p == -1 {
			continue
		}
		work[r], work[p] = work[p], work[r]
		idxs[r], idxs[p] = idxs[p], idxs[r]
		for i := 0; i < M; i++ {
			if i == r {
				continue
			}
			if work[i][c] {
				for j := c; j < K; j++ {
					work[i][j] = work[i][j] != work[r][j]
				}
			}
		}
		pivots = append(pivots, idxs[r])
		r++
	}
	if len(pivots) < K {
		return nil, false
	}
	return pivots, true
}

// LoadEncodingIndex reads a binary file containing a sequence of little-endian int64 values
// and converts them to a slice of int for use as array indices.
func LoadEncodingIndex(filePath string) ([]int, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errors.New("failed to read encoding index file")
	}

	int64Size := 8
	if len(fileBytes)%int64Size != 0 {
		return nil, errors.New("file size is not a multiple of int64 size")
	}

	arrayLen := len(fileBytes) / int64Size
	goSlice64 := make([]int64, arrayLen)
	reader := bytes.NewReader(fileBytes)
	if err := binary.Read(reader, binary.LittleEndian, &goSlice64); err != nil {
		return nil, errors.New("failed to decode binary data")
	}

	// Convert []int64 to []int for direct use as slice indices.
	goSliceInt := make([]int, arrayLen)
	for i, v := range goSlice64 {
		goSliceInt[i] = int(v)
	}
	return goSliceInt, nil
}

// EncodePolarGN encodes the input data using the standard polar generator matrix G_N = B_N * F^{⊗n}.
//
// Parameters:
//   - data: The input message data to encode (64 bytes = 512 bits in your case).
//   - n: The exponent for the polar code (n=10 in your case).
//
// Returns:
//   - A byte slice containing the encoded codeword of length N/8.
//   - An error if the input parameters are invalid.
func EncodePolarGN(data []byte, n int, encodingIndex []int) ([]byte, error) {
	dataBits := len(data) * 8
	N := 1 << n
	if len(encodingIndex) < dataBits {
		return nil, errors.New("encoding index length error")
	}
	// Create the information vector 'u' using the reliability sequence.
	u := make([]bool, N)
	bitCounter := 0
	for _, byteVal := range data {
		for j := range 8 {
			// Our encoder applies only F^{⊗n} (no bit-reversal permutation B_N).
			// Many reliability sequences are given in the B_N*F^{⊗n} domain.
			// Align by bit-reversing the index into our u-domain.
			destIndex := bitReverseN(encodingIndex[bitCounter], n)
			bitValue := (byteVal>>j)&1 == 1
			u[destIndex] = bitValue
			bitCounter++
		}
	}
	// We apply the transform directly to the information vector 'u'.
	x := u
	for i := uint(0); i < uint(n); i++ {
		blockSize := 1 << (i + 1)
		halfBlock := 1 << i
		for blockStart := 0; blockStart < N; blockStart += blockSize {
			for j := 0; j < halfBlock; j++ {
				idx1 := blockStart + j
				idx2 := idx1 + halfBlock
				x[idx1] = x[idx1] != x[idx2]
			}
		}
	}
	// Pack the bits into bytes.
	encodedBytes := make([]byte, N/8)
	for i := range encodedBytes {
		var packedByte byte
		for j := range 8 {
			if x[i*8+j] {
				packedByte |= (1 << j)
			}
		}
		encodedBytes[i] = packedByte
	}
	return encodedBytes, nil
}

// --- Main Pipeline Function with Bit-Level Shuffling ---

// EncodeAndBitInterleaveFrames performs segmentation, polar encoding, and bit-level interleaving.
func EncodeAndBitInterleaveFrames(dataFrames [][]byte, encodingIndex []int) (finalPackets [][]byte, randomMap []int, allCodewordsForVerification [][]byte, err error) {
	K := len(dataFrames)
	if K == 0 || (K&(K-1)) != 0 {
		return nil, nil, nil, errors.New("k must be a non-zero power of 2")
	}
	const frameSize = 512
	const chunkSize = 64
	const codewordBytes = 128
	const codewordBits = codewordBytes * 8
	const chunksPerFrame = frameSize / chunkSize
	const polarN = 10
	numCodewords := K * chunksPerFrame
	allCodewords := make([][]byte, 0, numCodewords)
	for _, frame := range dataFrames {
		if len(frame) != frameSize {
			return nil, nil, nil, errors.New("invalid size for frame")
		}
		for i := 0; i < chunksPerFrame; i++ {
			chunk := frame[i*chunkSize : (i+1)*chunkSize]
			encodedChunk, err := EncodePolarGN(chunk, polarN, encodingIndex)
			if err != nil {
				return nil, nil, nil, errors.New("encoding failed")
			}
			allCodewords = append(allCodewords, encodedChunk)
		}
	}
	// Use a stable random map so the missing packet index corresponds to a fixed row set across batches
	randomMap = getStableRandomMap(codewordBits, K)
	subsetSizeBits := codewordBits / K
	outputPacketSizeBits := numCodewords * subsetSizeBits
	outputPacketsBits := make([][]bool, K)
	for i := range outputPacketsBits {
		outputPacketsBits[i] = make([]bool, outputPacketSizeBits)
	}
	codewordBitsUnpacked := make([]bool, codewordBits)
	shuffledBits := make([]bool, codewordBits)
	for cwIdx, codewordBytes := range allCodewords {
		for i, byteVal := range codewordBytes {
			for j := 0; j < 8; j++ {
				codewordBitsUnpacked[i*8+j] = (byteVal>>j)&1 == 1
			}
		}
		for i := 0; i < codewordBits; i++ {
			shuffledBits[i] = codewordBitsUnpacked[randomMap[i]]
		}
		for subsetIdx := 0; subsetIdx < K; subsetIdx++ {
			subsetStart := subsetIdx * subsetSizeBits
			subset := shuffledBits[subsetStart : subsetStart+subsetSizeBits]
			destPacketBits := outputPacketsBits[subsetIdx]
			destStartOffset := cwIdx * subsetSizeBits
			copy(destPacketBits[destStartOffset:], subset)
		}
	}
	finalPackets = make([][]byte, K)
	outputPacketSizeBytes := outputPacketSizeBits / 8
	for i := 0; i < K; i++ {
		packetBytes := make([]byte, outputPacketSizeBytes)
		for j := 0; j < outputPacketSizeBytes; j++ {
			var packedByte byte
			for bitOffset := 0; bitOffset < 8; bitOffset++ {
				if outputPacketsBits[i][j*8+bitOffset] {
					packedByte |= (1 << bitOffset)
				}
			}
			packetBytes[j] = packedByte
		}
		finalPackets[i] = packetBytes
	}
	return finalPackets, randomMap, allCodewords, nil
}

// EncodeMsgsAndBitInterleave encodes arbitrary-size messages (len in bytes, multiple of 1 byte) into 1024-bit codewords
// and interleaves bits across K packets according to randomMap. If randomMap is nil, a stable one is generated.
func EncodeMsgsAndBitInterleave(msgs [][]byte, encodingIndex []int, randomMap []int, K int) ([][]byte, []int, error) {
	if K == 0 || (K&(K-1)) != 0 {
		return nil, nil, errors.New("k must be power of 2")
	}
	const polarN = 10
	const codewordBits = 1 << polarN
	numCodewords := len(msgs)
	codewords := make([][]byte, numCodewords)
	// Fast path: if all messages have same length, precompute packed columns and XOR
	uniform := numCodewords > 0
	msgBits := 0
	if uniform {
		msgBits = len(msgs[0]) * 8
	}
	// for i := 1; i < numCodewords && uniform; i++ {
	// 	if len(msgs[i]) != len(msgs[0]) {
	// 		uniform = false
	// 	}
	// }
	if uniform && msgBits > 0 {
		if msgBits > len(encodingIndex) {
			return nil, nil, errors.New("encodingIndex too short for msg")
		}
		// Ensure randomMap is available for fused encoder path
		if randomMap == nil {
			randomMap = getStableRandomMap(codewordBits, K)
		}
		// Fused path: directly XOR packet LUT rows into packets, skipping codeword materialization
		subsetSizeBits := codewordBits / K
		subsetBytes := subsetSizeBits / 8
		outBytes := (numCodewords * subsetSizeBits) / 8
		packets := make([][]byte, K)
		for i := 0; i < K; i++ {
			packets[i] = make([]byte, outBytes)
		}
		pktLUT, err := getPacketLUT(polarN, encodingIndex, msgBits, randomMap, K)
		if err != nil {
			return nil, nil, err
		}
		msgBytes := msgBits / 8
		for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
			m := msgs[cwIdx]
			baseOff := cwIdx * subsetBytes
			for by := 0; by < msgBytes; by++ {
				v := int(m[by])
				if v == 0 {
					continue
				}
				row := pktLUT[by*256+v] // 128 bytes across K subsets
				// XOR into each subset at codeword offset
				for s := 0; s < K; s++ {
					dst := packets[s][baseOff : baseOff+subsetBytes]
					src := row[s*subsetBytes : (s+1)*subsetBytes]
					for j := 0; j < subsetBytes; j++ {
						dst[j] ^= src[j]
					}
				}
			}
		}
		return packets, randomMap, nil
	} else {
		for i, m := range msgs {
			if (len(m) * 8) > len(encodingIndex) {
				return nil, nil, errors.New("encodingIndex too short for msg")
			}
			cw, err := EncodePolarGN(m, polarN, encodingIndex)
			if err != nil {
				return nil, nil, err
			}
			codewords[i] = cw
		}
	}
	if randomMap == nil {
		randomMap = getStableRandomMap(codewordBits, K)
	}
	subsetSizeBits := codewordBits / K
	outputPacketSizeBits := numCodewords * subsetSizeBits
	// Prepare byte-wise packet buffers
	packets := make([][]byte, K)
	outBytes := outputPacketSizeBits / 8
	for i := 0; i < K; i++ {
		packets[i] = make([]byte, outBytes)
	}
	// Use a precomputed interleave plan to assemble packet bytes efficiently
	plan := buildInterleavePlan(randomMap, K)
	subsetBytes := subsetSizeBits / 8
	for subset := 0; subset < K; subset++ {
		rows := plan[subset]
		dst := packets[subset]
		for cwIdx, cw := range codewords {
			dstBase := cwIdx * subsetBytes
			for j := 0; j < subsetBytes; j++ {
				var outb byte
				e := rows[j]
				if cw[e[0].srcByte]&e[0].srcMask != 0 {
					outb |= 1 << 0
				}
				if cw[e[1].srcByte]&e[1].srcMask != 0 {
					outb |= 1 << 1
				}
				if cw[e[2].srcByte]&e[2].srcMask != 0 {
					outb |= 1 << 2
				}
				if cw[e[3].srcByte]&e[3].srcMask != 0 {
					outb |= 1 << 3
				}
				if cw[e[4].srcByte]&e[4].srcMask != 0 {
					outb |= 1 << 4
				}
				if cw[e[5].srcByte]&e[5].srcMask != 0 {
					outb |= 1 << 5
				}
				if cw[e[6].srcByte]&e[6].srcMask != 0 {
					outb |= 1 << 6
				}
				if cw[e[7].srcByte]&e[7].srcMask != 0 {
					outb |= 1 << 7
				}
				dst[dstBase+j] = outb
			}
		}
	}
	return packets, randomMap, nil
}

// DeinterleaveKnownGeneric reconstructs codeword bits from packets for arbitrary batch size.
func DeinterleaveKnownGeneric(interleavedPackets [][]byte, randomMap []int) ([][]bool, error) {
	K := len(interleavedPackets)
	if K == 0 {
		return nil, errors.New("no packets")
	}
	const codewordBits = 1024
	subsetSizeBits := codewordBits / K
	// infer numCodewords from packet size
	var pktLen int
	for _, p := range interleavedPackets {
		if len(p) > 0 {
			pktLen = len(p)
			break
		}
	}
	if pktLen == 0 {
		return nil, errors.New("all packets missing")
	}
	totalBits := pktLen * 8
	if totalBits%subsetSizeBits != 0 {
		return nil, errors.New("packet size invalid for K")
	}
	numCodewords := totalBits / subsetSizeBits
	// unpack
	pktBits := make([][]bool, K)
	present := make([]bool, K)
	for i, p := range interleavedPackets {
		if len(p) == 0 {
			present[i] = false
			continue
		}
		present[i] = true
		bits := make([]bool, len(p)*8)
		for j, by := range p {
			for b := 0; b < 8; b++ {
				bits[j*8+b] = ((by >> b) & 1) == 1
			}
		}
		pktBits[i] = bits
	}
	// gather shuffled
	shuffled := make([][]bool, numCodewords)
	for cw := 0; cw < numCodewords; cw++ {
		s := make([]bool, codewordBits)
		for subset := 0; subset < K; subset++ {
			start := subset * subsetSizeBits
			if present[subset] {
				src := pktBits[subset]
				off := cw * subsetSizeBits
				copy(s[start:start+subsetSizeBits], src[off:off+subsetSizeBits])
			}
		}
		shuffled[cw] = s
	}
	// de-shuffle
	inv := invertMap(randomMap)
	out := make([][]bool, numCodewords)
	for cw := 0; cw < numCodewords; cw++ {
		b := make([]bool, codewordBits)
		for i := 0; i < codewordBits; i++ {
			b[i] = shuffled[cw][inv[i]]
		}
		out[cw] = b
	}
	return out, nil
}

// DecodeMsgs decodes variable-size messages (numDataBits) from interleaved packets using algebraic erasure decoding.
func DecodeMsgs(interleavedPackets [][]byte, randomMap, encodingIndex []int, numDataBits int) ([][]byte, error) {
	K := len(interleavedPackets)
	const polarN = 10
	// We route both loss and no-loss cases through the packed algebraic path for speed and consistency.
	// Build info columns
	Gcols, _ := getInfoColsAndG(polarN, encodingIndex, numDataBits)
	N := 1 << polarN
	// Build presence and known rows using inverse map
	subsetSizeBits := N / K
	var mask uint32
	present := make([]bool, K)
	for i, p := range interleavedPackets {
		if len(p) == 0 {
			mask |= 1 << uint(i)
		} else {
			present[i] = true
		}
	}
	inv := invertMap(randomMap)
	// We don't need to materialize the full list of known rows here; we'll select rows on the fly.
	// Helper to build B directly from packets for selected rows
	buildB := func(usedRows []int) (B [][]uint64, numCW int) {
		// infer num codewords from any present packet
		var pktLen int
		for i := 0; i < K; i++ {
			if present[i] {
				pktLen = len(interleavedPackets[i])
				break
			}
		}
		totalBits := pktLen * 8
		numCW = totalBits / subsetSizeBits
		wordsCW := (numCW + 63) / 64
		B = make([][]uint64, numDataBits)
		for c := 0; c < numDataBits; c++ {
			r := usedRows[c]
			s := inv[r]
			subset := s / subsetSizeBits
			within := s % subsetSizeBits
			row := make([]uint64, wordsCW)
			if present[subset] {
				pkt := interleavedPackets[subset]
				for cw := 0; cw < numCW; cw++ {
					bitIdx := cw*subsetSizeBits + within
					if ((pkt[bitIdx>>3] >> uint(bitIdx&7)) & 1) == 1 {
						row[cw>>6] |= 1 << uint(cw&63)
					}
				}
			}
			B[c] = row
		}
		return B, numCW
	}
	// Check cache
	if e, ok := invCacheMap[struct {
		mask  uint32
		kinfo int
	}{mask: mask, kinfo: numDataBits}]; ok {
		invAsqPacked := e.inv
		usedRows := e.usedRows
		// Build B directly from packets
		B, numCW := buildB(usedRows)
		wordsCW := (numCW + 63) / 64
		Out := make([][]uint64, numDataBits)
		for i := 0; i < numDataBits; i++ {
			acc := make([]uint64, wordsCW)
			for pb := 0; pb < len(invAsqPacked[i]); pb++ {
				m := invAsqPacked[i][pb]
				for m != 0 {
					tz := bits.TrailingZeros64(m)
					col := (pb << 6) + tz
					brow := B[col]
					for w := 0; w < wordsCW; w++ {
						acc[w] ^= brow[w]
					}
					m &= m - 1
				}
			}
			Out[i] = acc
		}
		// Pack
		allDataBits := make([]int, 0, numCW*numDataBits)
		for cw := 0; cw < numCW; cw++ {
			w := cw >> 6
			b := uint(cw) & 63
			for i := 0; i < numDataBits; i++ {
				if ((Out[i][w] >> b) & 1) == 1 {
					allDataBits = append(allDataBits, 1)
				} else {
					allDataBits = append(allDataBits, 0)
				}
			}
		}
		numMsgs := len(allDataBits) / numDataBits
		out := make([][]byte, numMsgs)
		bit := 0
		for i := 0; i < numMsgs; i++ {
			bts := make([]byte, numDataBits/8)
			for j := 0; j < len(bts); j++ {
				var x byte
				for k := 0; k < 8; k++ {
					if allDataBits[bit] == 1 {
						x |= 1 << k
					}
					bit++
				}
				bts[j] = x
			}
			out[i] = bts
		}
		return out, nil
	}
	// Deterministic row selection independent of number of known rows.
	// Build a KxK basis by scanning all rows and adding present rows that increase rank.
	packedCols, _ := getPackedInfoCols(polarN, encodingIndex, numDataBits)
	wordsK := (numDataBits + 63) / 64
	basisRows := make([][]uint64, 0, numDataBits)
	basisPivot := make([]int, 0, numDataBits)
	usedRows := make([]int, 0, numDataBits)
	// helper: test bit in packed column c at row r
	getColBit := func(c, r int) bool { w := r >> 6; b := uint(r & 63); return ((packedCols[c][w] >> b) & 1) == 1 }
	for r := 0; r < N && len(basisRows) < numDataBits; r++ {
		// skip if this row is not present
		s := inv[r]
		subset := s / subsetSizeBits
		if !present[subset] {
			continue
		}
		// build row vector across K columns
		v := make([]uint64, wordsK)
		for c := 0; c < numDataBits; c++ {
			if getColBit(c, r) {
				v[c>>6] |= 1 << uint(c&63)
			}
		}
		// eliminate using existing basis
		for i := 0; i < len(basisRows); i++ {
			pcol := basisPivot[i]
			if ((v[pcol>>6] >> uint(pcol&63)) & 1) == 1 {
				for w := 0; w < wordsK; w++ {
					v[w] ^= basisRows[i][w]
				}
			}
		}
		// find pivot in v
		pivot := -1
		for w := 0; w < wordsK; w++ {
			if v[w] != 0 {
				tz := bits.TrailingZeros64(v[w])
				pivot = (w << 6) + tz
				break
			}
		}
		if pivot == -1 {
			continue
		}
		// normalize by XORing to clear any future conflicts (we keep as-is; elimination handles it)
		basisRows = append(basisRows, v)
		basisPivot = append(basisPivot, pivot)
		usedRows = append(usedRows, r)
	}
	if len(usedRows) != numDataBits {
		return nil, errors.New("insufficient rank for erasure recovery")
	}
	// Build Asq (KxK) only from selected rows for inversion
	Asq := make([][]bool, numDataBits)
	for i := 0; i < numDataBits; i++ {
		r := usedRows[i]
		row := make([]bool, numDataBits)
		for c := 0; c < numDataBits; c++ {
			row[c] = Gcols[c][r]
		}
		Asq[i] = row
	}
	invAsqPacked, ok := invertBoolMatrixGF2Packed(Asq)
	if !ok {
		return nil, errors.New("polar erasure inversion failed")
	}
	// usedRows already set by basis selection
	invCacheMap[struct {
		mask  uint32
		kinfo int
	}{mask: mask, kinfo: numDataBits}] = struct {
		inv      [][]uint64
		usedRows []int
	}{inv: invAsqPacked, usedRows: usedRows}
	// Build B and batch solve
	B, numCW := buildB(usedRows)
	wordsCW := (numCW + 63) / 64
	Out := make([][]uint64, numDataBits)
	for i := 0; i < numDataBits; i++ {
		acc := make([]uint64, wordsCW)
		for pb := 0; pb < len(invAsqPacked[i]); pb++ {
			m := invAsqPacked[i][pb]
			for m != 0 {
				tz := bits.TrailingZeros64(m)
				col := (pb << 6) + tz
				brow := B[col]
				for w := 0; w < wordsCW; w++ {
					acc[w] ^= brow[w]
				}
				m &= m - 1
			}
		}
		Out[i] = acc
	}
	allDataBits := make([]int, 0, numCW*numDataBits)
	for cw := 0; cw < numCW; cw++ {
		w := cw >> 6
		b := uint(cw) & 63
		for i := 0; i < numDataBits; i++ {
			if ((Out[i][w] >> b) & 1) == 1 {
				allDataBits = append(allDataBits, 1)
			} else {
				allDataBits = append(allDataBits, 0)
			}
		}
	}
	numMsgs := len(allDataBits) / numDataBits
	out := make([][]byte, numMsgs)
	bit := 0
	for i := 0; i < numMsgs; i++ {
		bts := make([]byte, numDataBits/8)
		for j := 0; j < len(bts); j++ {
			var x byte
			for k := 0; k < 8; k++ {
				if allDataBits[bit] == 1 {
					x |= 1 << k
				}
				bit++
			}
			bts[j] = x
		}
		out[i] = bts
	}
	return out, nil
}

// If map[new_idx] = old_idx, then invMap[old_idx] = new_idx.
func invertMap(forwardMap []int) []int {
	n := len(forwardMap)
	inverseMap := make([]int, n)
	for newIndex, oldIndex := range forwardMap {
		inverseMap[oldIndex] = newIndex
	}
	return inverseMap
}

// DeinterleaveAndReassemble recovers the original codewords from the interleaved packets.
func DeinterleaveAndReassemble(interleavedPackets [][]byte, randomMap []int) (originalCodewords [][]byte, err error) {
	K := len(interleavedPackets)
	if K == 0 {
		return nil, errors.New("input packets cannot be empty")
	}

	// --- Constants must match the encoder ---
	const outputPacketSizeBytes = 1024
	const codewordBytes = 128
	const codewordBits = codewordBytes * 8 // 1024
	numCodewords := K * 8

	// --- 1. Unpack all input packets into bit-level representations ---
	packetsAsBits := make([][]bool, K)
	for i, packet := range interleavedPackets {
		if len(packet) != outputPacketSizeBytes {
			return nil, errors.New("invalid packet size for packet")
		}
		packetBits := make([]bool, outputPacketSizeBytes*8)
		for j, byteVal := range packet {
			for bitOffset := range 8 {
				packetBits[j*8+bitOffset] = (byteVal>>bitOffset)&1 == 1
			}
		}
		packetsAsBits[i] = packetBits
	}

	// --- 2. Reassemble Shuffled Codewords by Gathering Subsets ---
	subsetSizeBits := codewordBits / K
	reassembledShuffledCodewordsBits := make([][]bool, numCodewords)

	// For each codeword we want to rebuild...
	for cwIdx := range numCodewords {
		shuffledCodeword := make([]bool, codewordBits)
		// ...gather its subsets from every packet.
		for subsetIdx := range K {
			// Source: The correct chunk from the packet's bitstream
			sourcePacketBits := packetsAsBits[subsetIdx]
			sourceStartOffset := cwIdx * subsetSizeBits
			subset := sourcePacketBits[sourceStartOffset : sourceStartOffset+subsetSizeBits]

			// Destination: The correct slot in the codeword we are building
			destStartOffset := subsetIdx * subsetSizeBits
			copy(shuffledCodeword[destStartOffset:], subset)
		}
		reassembledShuffledCodewordsBits[cwIdx] = shuffledCodeword
	}

	// --- 3. De-shuffle Bits of Each Codeword and Pack to Bytes ---
	inverseRandomMap := invertMap(randomMap)
	originalCodewords = make([][]byte, numCodewords)
	deShuffledBits := make([]bool, codewordBits)

	for cwIdx, shuffledBits := range reassembledShuffledCodewordsBits {
		// a) De-shuffle using the inverse map
		for i := range codewordBits {
			deShuffledBits[i] = shuffledBits[inverseRandomMap[i]]
		}

		// b) Pack the de-shuffled bits back into a byte slice
		codewordBytesResult := make([]byte, codewordBytes)
		for i := range codewordBytes {
			var packedByte byte
			for bitOffset := range 8 {
				if deShuffledBits[i*8+bitOffset] {
					packedByte |= (1 << bitOffset)
				}
			}
			codewordBytesResult[i] = packedByte
		}
		originalCodewords[cwIdx] = codewordBytesResult
	}

	return originalCodewords, nil
}

// DeinterleaveAndReassembleLLRs reconstructs codeword LLRs directly, treating any missing packet
// (nil or empty slice) as an erasure. Present bits get +/- baseLLR, missing bits get 0.
func DeinterleaveAndReassembleLLRs(interleavedPackets [][]byte, randomMap []int, baseLLR float64) ([][]float64, error) {
	K := len(interleavedPackets)
	if K == 0 {
		return nil, errors.New("input packets cannot be empty")
	}

	const outputPacketSizeBytes = 1024
	const codewordBytes = 128
	const codewordBits = codewordBytes * 8 // 1024
	numCodewords := K * 8

	// Unpack present packets to bit arrays; mark missing ones.
	packetsAsBits := make([][]bool, K)
	present := make([]bool, K)
	for i, packet := range interleavedPackets {
		if len(packet) == 0 {
			present[i] = false
			continue
		}
		if len(packet) != outputPacketSizeBytes {
			return nil, errors.New("invalid packet size for packet")
		}
		present[i] = true
		packetBits := make([]bool, outputPacketSizeBytes*8)
		for j, byteVal := range packet {
			for bitOffset := 0; bitOffset < 8; bitOffset++ {
				packetBits[j*8+bitOffset] = (byteVal>>bitOffset)&1 == 1
			}
		}
		packetsAsBits[i] = packetBits
	}

	// Gather subsets for each codeword, tracking presence mask.
	subsetSizeBits := codewordBits / K
	reassembledShuffledBits := make([][]bool, numCodewords)
	reassembledShuffledMask := make([][]bool, numCodewords)
	for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
		sbits := make([]bool, codewordBits)
		smask := make([]bool, codewordBits)
		for subsetIdx := 0; subsetIdx < K; subsetIdx++ {
			destStart := subsetIdx * subsetSizeBits
			if present[subsetIdx] {
				srcBits := packetsAsBits[subsetIdx]
				srcStart := cwIdx * subsetSizeBits
				copy(sbits[destStart:destStart+subsetSizeBits], srcBits[srcStart:srcStart+subsetSizeBits])
				for i := 0; i < subsetSizeBits; i++ {
					smask[destStart+i] = true
				}
			} else {
				// missing packet: leave bits default false and mask false
			}
		}
		reassembledShuffledBits[cwIdx] = sbits
		reassembledShuffledMask[cwIdx] = smask
	}

	// De-shuffle and convert to LLRs
	inverseRandomMap := invertMap(randomMap)
	llrs := make([][]float64, numCodewords)
	for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
		llr := make([]float64, codewordBits)
		for i := 0; i < codewordBits; i++ {
			src := inverseRandomMap[i]
			if reassembledShuffledMask[cwIdx][src] {
				bit := reassembledShuffledBits[cwIdx][src]
				if bit {
					llr[i] = -baseLLR // 1 -> negative
				} else {
					llr[i] = baseLLR // 0 -> positive
				}
			} else {
				llr[i] = 0 // erasure
			}
		}
		llrs[cwIdx] = llr
	}
	return llrs, nil
}

// DeinterleaveAndReassembleKnown returns de-shuffled codeword bits and a mask indicating presence.
func DeinterleaveAndReassembleKnown(interleavedPackets [][]byte, randomMap []int) ([][]bool, [][]bool, error) {
	K := len(interleavedPackets)
	if K == 0 {
		return nil, nil, errors.New("input packets cannot be empty")
	}
	const outputPacketSizeBytes = 1024
	const codewordBytes = 128
	const codewordBits = codewordBytes * 8
	numCodewords := K * 8

	packetsAsBits := make([][]bool, K)
	present := make([]bool, K)
	for i, packet := range interleavedPackets {
		if len(packet) == 0 {
			present[i] = false
			continue
		}
		if len(packet) != outputPacketSizeBytes {
			return nil, nil, errors.New("invalid packet size for packet")
		}
		present[i] = true
		bits := make([]bool, outputPacketSizeBytes*8)
		for j, b := range packet {
			for bit := 0; bit < 8; bit++ {
				bits[j*8+bit] = (b>>bit)&1 == 1
			}
		}
		packetsAsBits[i] = bits
	}

	subsetSizeBits := codewordBits / K
	shuffledBits := make([][]bool, numCodewords)
	shuffledMask := make([][]bool, numCodewords)
	for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
		sb := make([]bool, codewordBits)
		sm := make([]bool, codewordBits)
		for subsetIdx := 0; subsetIdx < K; subsetIdx++ {
			destStart := subsetIdx * subsetSizeBits
			if present[subsetIdx] {
				src := packetsAsBits[subsetIdx]
				srcStart := cwIdx * subsetSizeBits
				copy(sb[destStart:destStart+subsetSizeBits], src[srcStart:srcStart+subsetSizeBits])
				for i := 0; i < subsetSizeBits; i++ {
					sm[destStart+i] = true
				}
			}
		}
		shuffledBits[cwIdx] = sb
		shuffledMask[cwIdx] = sm
	}

	inv := invertMap(randomMap)
	bits := make([][]bool, numCodewords)
	masks := make([][]bool, numCodewords)
	for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
		b := make([]bool, codewordBits)
		m := make([]bool, codewordBits)
		for i := 0; i < codewordBits; i++ {
			src := inv[i]
			b[i] = shuffledBits[cwIdx][src]
			m[i] = shuffledMask[cwIdx][src]
		}
		bits[cwIdx] = b
		masks[cwIdx] = m
	}
	return bits, masks, nil
}

// buildPolarColumns constructs the N columns of G = F^{\otimes n} used by the encoder.

// solveGF2FullRank solves A x = b over GF(2), where A is MxK with rank K.
// solveGF2FullRank removed; using precomputed inverse approach for performance.

// Decoder holds the state for the successive cancellation decoder.
type Decoder struct {
	n         int // log2(N)
	codewordN int // N
	frozenSet map[int]struct{}
	bitIdx    int // Tracks the current global bit index being decoded
}

// NewDecoder initializes a new Decoder.
func NewDecoder(n int, frozenSet map[int]struct{}) *Decoder {
	codewordN := 1 << n
	return &Decoder{
		n:         n,
		codewordN: codewordN,
		frozenSet: frozenSet,
		bitIdx:    0,
	}
}

// f performs the check node operation using the min-sum approximation.
func f(a, b float64) float64 {
	return math.Copysign(1.0, a) * math.Copysign(1.0, b) * math.Min(math.Abs(a), math.Abs(b))
}

// g performs the repetition node operation in the LLR domain.
func g(a, b float64, u int) float64 {
	if u == 0 {
		return a + b
	}
	return b - a
}

func (d *Decoder) decodeRecursive(llrs []float64) []int {
	n := len(llrs)
	if n == 1 {
		var decision int
		if _, isFrozen := d.frozenSet[d.bitIdx]; isFrozen {
			decision = 0
		} else {
			if llrs[0] >= 0 {
				decision = 0
			} else {
				decision = 1
			}
		}
		d.bitIdx++
		return []int{decision}
	}
	half := n / 2
	fllr := make([]float64, half)
	for i := 0; i < half; i++ {
		fllr[i] = f(llrs[i], llrs[i+half])
	}
	u1 := d.decodeRecursive(fllr)
	gllr := make([]float64, half)
	for i := 0; i < half; i++ {
		gllr[i] = g(llrs[i], llrs[i+half], u1[i])
	}
	u2 := d.decodeRecursive(gllr)
	u := make([]int, n)
	for i := 0; i < half; i++ {
		u[i] = u1[i] ^ u2[i]
		u[i+half] = u2[i]
	}
	return u
}

// Decode runs SC decoding on the provided LLR vector.
func (d *Decoder) Decode(receivedCodeword []float64) []int {
	d.bitIdx = 0
	return d.decodeRecursive(receivedCodeword)
}

// bitReverseN returns the integer formed by reversing the lower n bits of x.
func bitReverseN(x int, n int) int {
	var r int
	for i := 0; i < n; i++ {
		r = (r << 1) | ((x >> i) & 1)
	}
	return r
}

func DecodeAndRecoverFrames(interleavedPackets [][]byte, randomMap, encodingIndex []int) ([][]byte, error) {
	K := len(interleavedPackets)
	const frameSize = 512
	const polarN = 10
	const numDataBits = 512
	// Recover known codeword bits and presence mask
	cwBits, _, err := DeinterleaveAndReassembleKnown(interleavedPackets, randomMap)
	if err != nil {
		return nil, errors.New("failed to reassemble")
	}
	frozenSet := make(map[int]struct{})
	// Build data set in our decoder's u-domain by bit-reversing indices.
	isDataIndex := make(map[int]struct{}, numDataBits)
	for i := range numDataBits {
		isDataIndex[bitReverseN(encodingIndex[i], polarN)] = struct{}{}
	}
	for i := range 1 << polarN {
		if _, isData := isDataIndex[i]; !isData {
			frozenSet[i] = struct{}{}
		}
	}
	// Fast path: if no erasure (all bits known), use involutory transform to invert F^{\otimes n}
	// Fast path: no packet loss
	dropIdx := -1
	for i, p := range interleavedPackets {
		if len(p) == 0 {
			dropIdx = i
			break
		}
	}
	allKnown := dropIdx == -1
	if allKnown {
		allDataBits := make([]int, 0, K*frameSize*8)
		N := 1 << polarN
		dataIdx := make([]int, numDataBits)
		for i := 0; i < numDataBits; i++ {
			dataIdx[i] = bitReverseN(encodingIndex[i], polarN)
		}
		u := make([]bool, N)
		for cw := 0; cw < len(cwBits); cw++ {
			// copy x
			copy(u, cwBits[cw])
			// apply F^{\otimes n} again (self-inverse) to get u
			for s := 0; s < polarN; s++ {
				block := 1 << (s + 1)
				half := 1 << s
				for start := 0; start < N; start += block {
					for k := 0; k < half; k++ {
						i1 := start + k
						i2 := i1 + half
						u[i1] = u[i1] != u[i2]
					}
				}
			}
			for i := 0; i < numDataBits; i++ {
				if u[dataIdx[i]] {
					allDataBits = append(allDataBits, 1)
				} else {
					allDataBits = append(allDataBits, 0)
				}
			}
		}
		numFrames := len(allDataBits) / (frameSize * 8)
		recoveredFrames := make([][]byte, numFrames)
		bitCounter := 0
		for i := range numFrames {
			frameBytes := make([]byte, frameSize)
			for j := range frameSize {
				var b byte
				for k := 0; k < 8; k++ {
					if allDataBits[bitCounter] == 1 {
						b |= 1 << k
					}
					bitCounter++
				}
				frameBytes[j] = b
			}
			recoveredFrames[i] = frameBytes
		}
		return recoveredFrames, nil
	}

	// Erasure path: Build only the K=512 info columns using butterfly
	Gcols, infoCols := getInfoColsAndG(polarN, encodingIndex, numDataBits)
	N := 1 << polarN

	// Build known rows from randomMap using dropIdx
	subsetSizeBits := N / K
	presentRow := make([]bool, N)
	for i := 0; i < N; i++ {
		presentRow[i] = true
	}
	if dropIdx >= 0 {
		start := dropIdx * subsetSizeBits
		for pos := start; pos < start+subsetSizeBits; pos++ {
			presentRow[randomMap[pos]] = false
		}
	}
	knownRowIdx := make([]int, 0, N)
	for r := 0; r < N; r++ {
		if presentRow[r] {
			knownRowIdx = append(knownRowIdx, r)
		}
	}

	// Use inverse cache indexed by loss mask and info size if available
	var mask uint32
	if dropIdx >= 0 {
		mask = 1 << uint(dropIdx)
	}
	if dropIdx >= 0 {
		if e, ok := invCacheMap[struct {
			mask  uint32
			kinfo int
		}{mask: mask, kinfo: numDataBits}]; ok {
			invAsqPacked := e.inv
			usedRows := e.usedRows
			// Batch solve using cached inverse
			numCW := len(cwBits)
			wordsCW := (numCW + 63) / 64
			B := make([][]uint64, numDataBits)
			for c := 0; c < numDataBits; c++ {
				row := make([]uint64, wordsCW)
				for cw := 0; cw < numCW; cw++ {
					if cwBits[cw][usedRows[c]] {
						row[cw>>6] |= 1 << (uint(cw) & 63)
					}
				}
				B[c] = row
			}
			Out := make([][]uint64, numDataBits)
			for i := 0; i < numDataBits; i++ {
				acc := make([]uint64, wordsCW)
				for pb := 0; pb < len(invAsqPacked[i]); pb++ {
					m := invAsqPacked[i][pb]
					for m != 0 {
						tz := bits.TrailingZeros64(m)
						col := (pb << 6) + tz
						brow := B[col]
						for w := 0; w < wordsCW; w++ {
							acc[w] ^= brow[w]
						}
						m &= m - 1
					}
				}
				Out[i] = acc
			}
			allDataBits := make([]int, 0, K*frameSize*8)
			for cw := 0; cw < numCW; cw++ {
				w := cw >> 6
				b := uint(cw) & 63
				for i := 0; i < numDataBits; i++ {
					if ((Out[i][w] >> b) & 1) == 1 {
						allDataBits = append(allDataBits, 1)
					} else {
						allDataBits = append(allDataBits, 0)
					}
				}
			}
			// Pack frames
			numFrames := len(allDataBits) / (frameSize * 8)
			recoveredFrames := make([][]byte, numFrames)
			bitCounter := 0
			for i := range numFrames {
				frameBytes := make([]byte, frameSize)
				for j := range frameSize {
					var b byte
					for k := 0; k < 8; k++ {
						if allDataBits[bitCounter] == 1 {
							b |= 1 << k
						}
						bitCounter++
					}
					frameBytes[j] = b
				}
				recoveredFrames[i] = frameBytes
			}
			return recoveredFrames, nil
		}
	}
	// Assemble A rows once
	rowsA := make([][]bool, 0, len(knownRowIdx))
	for _, r := range knownRowIdx {
		arow := make([]bool, numDataBits)
		for c := 0; c < numDataBits; c++ {
			arow[c] = Gcols[c][r]
		}
		rowsA = append(rowsA, arow)
	}
	// Prefer a greedy per-column pivot row assignment using (r & j)==j to avoid rank issues
	used := make([]bool, len(knownRowIdx))
	colRow := make([]int, numDataBits)
	for c := 0; c < numDataBits; c++ {
		colRow[c] = -1
	}
	for c := 0; c < numDataBits; c++ {
		j := infoCols[c]
		for ridx, r := range knownRowIdx {
			if used[ridx] {
				continue
			}
			if (r & j) == j {
				colRow[c] = ridx
				used[ridx] = true
				break
			}
		}
	}
	haveAll := true
	for c := 0; c < numDataBits; c++ {
		if colRow[c] == -1 {
			haveAll = false
			break
		}
	}
	var Asq [][]bool
	if haveAll {
		Asq = make([][]bool, numDataBits)
		for i := 0; i < numDataBits; i++ {
			Asq[i] = append([]bool(nil), rowsA[colRow[i]]...)
		}
	} else {
		// Fallback: choose K independent rows via elimination on all known rows
		pivotRows, ok := selectPivotRows(rowsA, numDataBits)
		if !ok {
			return nil, errors.New("polar erasure rank deficiency")
		}
		Asq = make([][]bool, numDataBits)
		for i := 0; i < numDataBits; i++ {
			Asq[i] = append([]bool(nil), rowsA[pivotRows[i]]...)
		}
	}
	invAsqPacked, ok := invertBoolMatrixGF2Packed(Asq)
	if !ok {
		return nil, errors.New("polar erasure inversion failed")
	}
	// Cache inverse for this mask
	if dropIdx >= 0 {
		usedRows := make([]int, numDataBits)
		if haveAll {
			for i := 0; i < numDataBits; i++ {
				usedRows[i] = knownRowIdx[colRow[i]]
			}
		} else {
			pr, _ := selectPivotRows(rowsA, numDataBits)
			for i := 0; i < numDataBits; i++ {
				usedRows[i] = knownRowIdx[pr[i]]
			}
		}
		invCacheMap[struct {
			mask  uint32
			kinfo int
		}{mask: mask, kinfo: numDataBits}] = struct {
			inv      [][]uint64
			usedRows []int
		}{inv: invAsqPacked, usedRows: usedRows}
	}

	// Determine which knownRowIdx rows were used in Asq
	usedRows := make([]int, numDataBits)
	if haveAll {
		for i := 0; i < numDataBits; i++ {
			usedRows[i] = knownRowIdx[colRow[i]]
		}
	} else {
		// Recompute pivot rows used by selectPivotRows over rowsA to map back to knownRowIdx
		// Since Asq was constructed from rowsA[pivotRows[i]], recreate that mapping
		pr, _ := selectPivotRows(rowsA, numDataBits)
		for i := 0; i < numDataBits; i++ {
			usedRows[i] = knownRowIdx[pr[i]]
		}
	}

	// Batch solve for all codewords at once: Out = invAsq * B over GF(2), packed across codewords
	numCW := len(cwBits)
	wordsCW := (numCW + 63) / 64
	// Build B: K rows x wordsCW words, where B[c][w] holds cw-bits for column c
	B := make([][]uint64, numDataBits)
	for c := 0; c < numDataBits; c++ {
		row := make([]uint64, wordsCW)
		for cw := 0; cw < numCW; cw++ {
			if cwBits[cw][usedRows[c]] {
				row[cw>>6] |= 1 << (uint(cw) & 63)
			}
		}
		B[c] = row
	}
	// Out = invAsqPacked (KxK) * B (KxW)
	Out := make([][]uint64, numDataBits)
	for i := 0; i < numDataBits; i++ {
		acc := make([]uint64, wordsCW)
		for pb := 0; pb < len(invAsqPacked[i]); pb++ {
			m := invAsqPacked[i][pb]
			for m != 0 {
				tz := bits.TrailingZeros64(m)
				col := (pb << 6) + tz
				// XOR the whole word-vector for this column
				brow := B[col]
				for w := 0; w < wordsCW; w++ {
					acc[w] ^= brow[w]
				}
				m &= m - 1
			}
		}
		Out[i] = acc
	}
	// Extract per-codeword info bits
	allDataBits := make([]int, 0, K*frameSize*8)
	for cw := 0; cw < numCW; cw++ {
		w := cw >> 6
		b := uint(cw) & 63
		for i := 0; i < numDataBits; i++ {
			if ((Out[i][w] >> b) & 1) == 1 {
				allDataBits = append(allDataBits, 1)
			} else {
				allDataBits = append(allDataBits, 0)
			}
		}
	}
	numFrames := len(allDataBits) / (frameSize * 8)
	recoveredFrames := make([][]byte, numFrames)
	bitCounter := 0
	for i := range numFrames {
		frameBytes := make([]byte, frameSize)
		for j := range frameSize {
			var packedByte byte
			for k := range 8 {
				if allDataBits[bitCounter] == 1 {
					packedByte |= (1 << k)
				}
				bitCounter++
			}
			frameBytes[j] = packedByte
		}
		recoveredFrames[i] = frameBytes
	}
	return recoveredFrames, nil
}
