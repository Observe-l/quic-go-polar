package fec

import (
	"errors"
	"math/bits"
	"math/rand"
	"time"

	"gonum.org/v1/gonum/mat"
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

// bitReverse reverses the 'n' least significant bits of an integer 'i'.
func bitReverse(i, n uint) uint {
	// bits.Reverse reverses all bits, so we shift to keep only the 'n' we care about.
	return bits.Reverse(i) >> (bits.UintSize - n)
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
func EncodePolarGN(data []byte, n int) ([]byte, error) {
	if n <= 0 {
		return nil, errors.New("n must be a positive integer")
	}

	N := 1 << n        // N = 2^n, the total length of the codeword in bits.
	K := len(data) * 8 // K is the length of the message data in bits.

	if K > N {
		return nil, errors.New("data length cannot be greater than codeword length N")
	}

	// The encoding is x = u * G_N = u * (B_N * F^{⊗n}) = (u * B_N) * F^{⊗n}.
	// We implement this efficiently in three steps.

	// 1. Construct the initial information vector 'u' of length N.
	// This vector contains the message bits followed by frozen bits (zeros).
	u := make([]bool, N)
	for i := 0; i < len(data); i++ {
		byteVal := data[i]
		for j := 0; j < 8; j++ {
			// Extract the j-th bit from the byte.
			if (byteVal>>j)&1 == 1 {
				u[i*8+j] = true
			}
		}
	}
	// The rest of the 'u' slice remains false (frozen bits).

	// 2. Permute 'u' using bit-reversal to get u_permuted = u * B_N.
	// This is the most efficient way to apply the B_N matrix.
	uPermuted := make([]bool, N)
	for i := 0; i < N; i++ {
		reversedIndex := bitReverse(uint(i), uint(n))
		uPermuted[i] = u[reversedIndex]
	}

	// 3. Encode u_permuted using the fast F^{⊗n} transform to get the final codeword x.
	// This calculates x = u_permuted * F^{⊗n}.
	x := uPermuted // Work in-place on the permuted vector for efficiency.
	for i := 0; i < n; i++ {
		stage := uint(i)
		blockSize := 1 << (stage + 1)
		halfBlock := 1 << stage

		for blockStart := 0; blockStart < N; blockStart += blockSize {
			for j := 0; j < halfBlock; j++ {
				idx1 := blockStart + j
				idx2 := idx1 + halfBlock

				// Apply the core transformation: (a, b) -> (a XOR b, b)
				// The boolean '!=' operator is equivalent to XOR.
				x[idx1] = x[idx1] != x[idx2]
			}
		}
	}
	// The vector 'x' now holds the correctly encoded codeword.

	// 4. Pack the resulting boolean slice back into a byte slice.
	encodedBytes := make([]byte, N/8)
	for i := 0; i < len(encodedBytes); i++ {
		var packedByte byte
		for j := 0; j < 8; j++ {
			bitIndex := i*8 + j
			if x[bitIndex] {
				// Set the j-th bit of the byte.
				packedByte |= (1 << j)
			}
		}
		encodedBytes[i] = packedByte
	}

	return encodedBytes, nil
}

// PolarGeneratorMatrix creates the N x N polar code generator matrix G_N, where N = 2^n.
// This is the standard definition G_N = B_N * (F ⊗ n), where B_N is the
// bit-reversal permutation matrix and F = [[1, 0], [1, 1]].
func PolarGeneratorMatrix(n int) *mat.Dense {
	N := 1 << n // N = 2^n

	// 1. Efficiently compute F_kron_n = F^{\otimes n} using boolean logic.
	// This is adapted from the efficient generator you provided.
	fKronN := [][]bool{{true}}
	for i := 0; i < n; i++ {
		prevSize := 1 << i
		// Create the new, larger matrix (twice the dimensions)
		newFKronN := make([][]bool, 2*prevSize)
		for r := range newFKronN {
			newFKronN[r] = make([]bool, 2*prevSize)
		}

		// Fill the new matrix based on the definition of the Kronecker product with F.
		for r := 0; r < prevSize; r++ {
			for c := 0; c < prevSize; c++ {
				// F = [[1, 0], [1, 1]]
				// Top-left block (1 * G)
				newFKronN[r][c] = fKronN[r][c]
				// Top-right block (0 * G) -> already false from initialization
				// Bottom-left block (1 * G)
				newFKronN[r+prevSize][c] = fKronN[r][c]
				// Bottom-right block (1 * G)
				newFKronN[r+prevSize][c+prevSize] = fKronN[r][c]
			}
		}
		fKronN = newFKronN
	}

	// 2. Generate the bit-reversal permutation order for the columns.
	permutation := make([]int, N)
	for i := 0; i < N; i++ {
		permutation[i] = int(bitReverse(uint(i), uint(n)))
	}

	// 3. Apply the permutation and convert the boolean matrix to a *mat.Dense (float64).
	// The final matrix is initialized with all zeros.
	gN := mat.NewDense(N, N, nil)

	// The k-th column of the final matrix should be the permutation[k]-th column of fKronN.
	for r := 0; r < N; r++ {
		for c := 0; c < N; c++ {
			// Get the value from the un-permuted matrix
			val := fKronN[r][c]
			if val {
				// Place it in the permuted column location
				permutedCol := permutation[c]
				gN.Set(r, permutedCol, 1.0)
			}
		}
	}

	return gN
}

// EncodePacketsBitLevelFEC encodes a set of data packets by treating them as a single
// contiguous bitstream, applying a polar code, and re-segmenting the output.
// This is a high-efficiency implementation.
//
// Parameters:
//   - dataPackets: A slice of K data packets.
//   - R: The number of zero-padding packets to append before encoding.
//
// Returns:
//   - A slice of K+R encoded packets.
//   - An error if the input is invalid.
func EncodePacketsBitLevelFEC(dataPackets [][]byte, R int) ([][]byte, error) {
	K := len(dataPackets)
	if K == 0 {
		return nil, errors.New("input dataPackets cannot be empty")
	}
	if R < 0 {
		return nil, errors.New("number of redundancy packets (R) cannot be negative")
	}
	packetSize := len(dataPackets[0])
	totalPackets := K + R

	// --- 1. Concatenate all packets into a single flat byte slice ---
	totalBytes := totalPackets * packetSize
	flatUBytes := make([]byte, totalBytes)

	for i := 0; i < K; i++ {
		if len(dataPackets[i]) != packetSize {
			return nil, errors.New("all data packets must have the same length")
		}
		offset := i * packetSize
		copy(flatUBytes[offset:offset+packetSize], dataPackets[i])
	}
	// The remaining bytes in flatUBytes (from K*packetSize to the end) are already zero,
	// representing the R padding packets.

	// --- 2. Unpack the entire flat byte slice into a bit-level information vector 'u' ---
	N := totalBytes * 8
	if N == 0 || (N&(N-1)) != 0 {
		return nil, errors.New("total number of bits must be a power of 2")
	}
	n := uint(bits.TrailingZeros(uint(N)))

	u := make([]bool, N)
	for i, byteVal := range flatUBytes {
		for j := 0; j < 8; j++ {
			if (byteVal>>j)&1 == 1 {
				u[i*8+j] = true
			}
		}
	}

	// --- 3. Perform the standard bit-level Polar Encode (G_N = B_N * F^{⊗n}) ---
	// a) Apply Bit-Reversal Permutation
	uPermuted := make([]bool, N)
	for i := 0; i < N; i++ {
		reversedIndex := bitReverse(uint(i), n)
		uPermuted[i] = u[reversedIndex]
	}

	// b) Apply Fast Polar Transform
	x := uPermuted // Work in-place
	for i := uint(0); i < n; i++ {
		blockSize := 1 << (i + 1)
		halfBlock := 1 << i
		for blockStart := 0; blockStart < N; blockStart += blockSize {
			for j := 0; j < halfBlock; j++ {
				idx1 := blockStart + j
				idx2 := idx1 + halfBlock
				x[idx1] = x[idx1] != x[idx2] // XOR
			}
		}
	}

	// --- 4. Pack the encoded bit vector 'x' back into a flat byte slice ---
	encodedFlatBytes := make([]byte, totalBytes)
	for i := 0; i < totalBytes; i++ {
		var packedByte byte
		for j := 0; j < 8; j++ {
			if x[i*8+j] {
				packedByte |= (1 << j)
			}
		}
		encodedFlatBytes[i] = packedByte
	}

	// --- 5. Split the flat encoded slice back into packets ---
	// This is highly efficient as it creates slices that point to the
	// underlying array without copying memory.
	outputPackets := make([][]byte, totalPackets)
	for i := 0; i < totalPackets; i++ {
		start := i * packetSize
		end := start + packetSize
		outputPackets[i] = encodedFlatBytes[start:end]
	}

	return outputPackets, nil
}

// --- Main Pipeline Function with Bit-Level Shuffling ---

// EncodeAndBitInterleaveFrames performs segmentation, polar encoding, and bit-level interleaving.
func EncodeAndBitInterleaveFrames(dataFrames [][]byte) (finalPackets [][]byte, randomMap []int, allCodewordsForVerification [][]byte, err error) {
	K := len(dataFrames)
	if K == 0 || (K&(K-1)) != 0 {
		return nil, nil, nil, errors.New("number of data frames (K) must be a non-zero power of 2")
	}

	const frameSize = 512
	const chunkSize = 64
	const codewordBytes = 128
	const codewordBits = codewordBytes * 8 // 1024
	const chunksPerFrame = frameSize / chunkSize
	const polarN = 10

	// --- 1. Segmentation and Encoding ---
	numCodewords := K * chunksPerFrame
	allCodewords := make([][]byte, 0, numCodewords)
	for _, frame := range dataFrames {
		if len(frame) != frameSize {
			return nil, nil, nil, errors.New("invalid size for frame")
		}
		for i := 0; i < chunksPerFrame; i++ {
			chunk := frame[i*chunkSize : (i+1)*chunkSize]
			encodedChunk, err := EncodePolarGN(chunk, polarN)
			if err != nil {
				return nil, nil, nil, errors.New("failed to encode chunk")
			}
			allCodewords = append(allCodewords, encodedChunk)
		}
	}

	// --- 2. Generate ONE Shared Random Map for Intra-Codeword Shuffling ---
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomMap = r.Perm(codewordBits)

	// --- 3. Shuffle Bits Within Each Codeword and Distribute Subsets ---
	subsetSizeBits := codewordBits / K
	if codewordBits%K != 0 {
		return nil, nil, nil, errors.New("codeword bit length must be divisible by K")
	}

	// Pre-allocate final packets at the bit-level for efficiency
	outputPacketSizeBits := numCodewords * subsetSizeBits // (K*8) * (1024/K) = 8192 bits = 1024 bytes
	outputPacketsBits := make([][]bool, K)
	for i := range outputPacketsBits {
		outputPacketsBits[i] = make([]bool, outputPacketSizeBits)
	}

	codewordBitsUnpacked := make([]bool, codewordBits)
	shuffledBits := make([]bool, codewordBits)

	// For each codeword...
	for cwIdx, codewordBytes := range allCodewords {
		// a) Unpack the current codeword to bits
		for i, byteVal := range codewordBytes {
			for j := 0; j < 8; j++ {
				codewordBitsUnpacked[i*8+j] = (byteVal>>j)&1 == 1
			}
		}

		// b) Shuffle its bits using the shared map
		for i := 0; i < codewordBits; i++ {
			shuffledBits[i] = codewordBitsUnpacked[randomMap[i]]
		}

		// c) Distribute its subsets to the final packets
		for subsetIdx := 0; subsetIdx < K; subsetIdx++ {
			// Source: The subset from the shuffled bits
			subsetStart := subsetIdx * subsetSizeBits
			subset := shuffledBits[subsetStart : subsetStart+subsetSizeBits]

			// Destination: The correct slot in the target packet's bitstream
			destPacketBits := outputPacketsBits[subsetIdx]
			destStartOffset := cwIdx * subsetSizeBits
			copy(destPacketBits[destStartOffset:], subset)
		}
	}

	// --- 4. Pack the Final Bit-Level Packets into Bytes ---
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
			for bitOffset := 0; bitOffset < 8; bitOffset++ {
				packetBits[j*8+bitOffset] = (byteVal>>bitOffset)&1 == 1
			}
		}
		packetsAsBits[i] = packetBits
	}

	// --- 2. Reassemble Shuffled Codewords by Gathering Subsets ---
	subsetSizeBits := codewordBits / K
	reassembledShuffledCodewordsBits := make([][]bool, numCodewords)

	// For each codeword we want to rebuild...
	for cwIdx := 0; cwIdx < numCodewords; cwIdx++ {
		shuffledCodeword := make([]bool, codewordBits)
		// ...gather its subsets from every packet.
		for subsetIdx := 0; subsetIdx < K; subsetIdx++ {
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
		for i := 0; i < codewordBits; i++ {
			deShuffledBits[i] = shuffledBits[inverseRandomMap[i]]
		}

		// b) Pack the de-shuffled bits back into a byte slice
		codewordBytesResult := make([]byte, codewordBytes)
		for i := 0; i < codewordBytes; i++ {
			var packedByte byte
			for bitOffset := 0; bitOffset < 8; bitOffset++ {
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
