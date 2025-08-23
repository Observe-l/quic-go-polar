package fec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"os"
	"time"
)

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
			destIndex := encodingIndex[bitCounter]
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
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomMap = r.Perm(codewordBits)
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

// decodeRecursive is the clean, correct recursive core of the decoder.
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
	f_llrs := make([]float64, half)
	for i := 0; i < half; i++ {
		f_llrs[i] = f(llrs[i], llrs[i+half])
	}
	u1_hats := d.decodeRecursive(f_llrs)

	g_llrs := make([]float64, half)
	for i := 0; i < half; i++ {
		g_llrs[i] = g(llrs[i], llrs[i+half], u1_hats[i])
	}
	u2_hats := d.decodeRecursive(g_llrs)

	u_hats := make([]int, n)
	for i := 0; i < half; i++ {
		u_hats[i] = u1_hats[i] ^ u2_hats[i]
		u_hats[i+half] = u2_hats[i]
	}
	return u_hats
}

// Decode is the public entry point that starts the recursive decoding.
func (d *Decoder) Decode(receivedCodeword []float64) []int {
	d.bitIdx = 0 // Reset the bit counter
	return d.decodeRecursive(receivedCodeword)
}

func DecodeAndRecoverFrames(interleavedPackets [][]byte, randomMap, encodingIndex []int) ([][]byte, error) {
	K := len(interleavedPackets)
	const frameSize = 512
	const polarN = 10
	const numDataBits = 512
	originalCodewords, err := DeinterleaveAndReassemble(interleavedPackets, randomMap)
	if err != nil {
		return nil, errors.New("failed to reassemble")
	}
	frozenSet := make(map[int]struct{})
	isDataIndex := make(map[int]struct{}, numDataBits)
	for i := range numDataBits {
		isDataIndex[encodingIndex[i]] = struct{}{}
	}
	for i := range 1 << polarN {
		if _, isData := isDataIndex[i]; !isData {
			frozenSet[i] = struct{}{}
		}
	}

	// Create an instance of YOUR decoder.
	decoder := NewDecoder(polarN, frozenSet)
	allDataBits := make([]int, 0, K*frameSize*8)

	for _, codeword := range originalCodewords {
		initialLLRs := make([]float64, len(codeword)*8)
		for i, byteVal := range codeword {
			for j := range 8 {
				bit := (byteVal >> j) & 1
				softValue := 1.0 - 2.0*float64(bit)
				initialLLRs[i*8+j] = 2.0 * softValue
			}
		}
		// Run YOUR decoder.
		decodedInfoVector := decoder.Decode(initialLLRs)
		for i := range numDataBits {
			dataIndex := encodingIndex[i]
			allDataBits = append(allDataBits, decodedInfoVector[dataIndex])
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
