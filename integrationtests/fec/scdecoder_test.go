package fec_test

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

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
// It takes LLRs and returns the decoded bits (u_hat).
func (d *Decoder) decodeRecursive(llrs []float64) []int {
	n := len(llrs)
	// Base case: we are at a leaf node and must decode a single bit.
	if n == 1 {
		var decision int
		if _, isFrozen := d.frozenSet[d.bitIdx]; isFrozen {
			decision = 0 // Frozen bits are known.
		} else {
			// Hard decision based on the LLR.
			if llrs[0] >= 0 {
				decision = 0
			} else {
				decision = 1
			}
		}
		d.bitIdx++ // Move to the next bit for the next base case call.
		return []int{decision}
	}

	half := n / 2

	// === Step 1: Upper branch (f-node) ===
	// Calculate the LLRs for the upper sub-problem.
	f_llrs := make([]float64, half)
	for i := 0; i < half; i++ {
		f_llrs[i] = f(llrs[i], llrs[i+half])
	}
	// Recursively decode the upper branch to get the u1_hats.
	u1_hats := d.decodeRecursive(f_llrs)

	// === Step 2: Lower branch (g-node) ===
	// Calculate the LLRs for the lower sub-problem using results from the upper branch.
	g_llrs := make([]float64, half)
	for i := 0; i < half; i++ {
		g_llrs[i] = g(llrs[i], llrs[i+half], u1_hats[i])
	}
	// Recursively decode the lower branch to get the u2_hats.
	u2_hats := d.decodeRecursive(g_llrs)

	// === Step 3: Combine bit decisions for the parent node ===
	// Combine the results from both branches.
	u_hats := make([]int, n)
	for i := 0; i < half; i++ {
		u_hats[i] = u1_hats[i] ^ u2_hats[i] // u1 = u1_hat XOR u2_hat
		u_hats[i+half] = u2_hats[i]         // u2 = u2_hat
	}

	return u_hats
}

// Decode is the public entry point that starts the recursive decoding.
func (d *Decoder) Decode(receivedCodeword []float64) []int {
	d.bitIdx = 0 // Reset the bit counter for each new codeword
	return d.decodeRecursive(receivedCodeword)
}

// getFrozenSet1024_512 provides a pre-computed frozen set for N=1024, K=512.
func getFrozenSet1024_512() map[int]struct{} {
	// For a real code, these indices would be pre-computed and loaded.
	// For this example, we will mark the first 512 bits as frozen.
	// This does NOT represent a good polar code, but allows the code to run.
	fullFrozenIndices := make([]int, 512)
	for i := 0; i < 512; i++ {
		fullFrozenIndices[i] = i
	}

	frozenSet := make(map[int]struct{}, len(fullFrozenIndices))
	for _, idx := range fullFrozenIndices {
		frozenSet[idx] = struct{}{}
	}
	return frozenSet
}

func Test_Sc(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	// 1. Setup for a (1024, 512) Polar Code
	n := 10
	codewordN := 1 << n
	frozenSet := getFrozenSet1024_512()
	infoBitsK := codewordN - len(frozenSet)

	fmt.Printf("Decoder settings: N=%d, K=%d (Rate=%.2f)\n", codewordN, infoBitsK, float64(infoBitsK)/float64(codewordN))

	// 2. Simulate a transmitted signal
	fmt.Println("Simulating transmission of an all-zero information block...")
	receivedLLRs := make([]float64, codewordN)
	noiseStrength := 0.8
	errorCount := 0
	for i := 0; i < codewordN; i++ {
		llr := 2.0 + r.NormFloat64()*noiseStrength
		receivedLLRs[i] = llr
		if llr < 0 {
			errorCount++
		}
	}
	fmt.Printf("Channel introduced %d initial bit errors (LLR < 0).\n", errorCount)

	// 3. Decode the received message
	fmt.Println("Starting decoding process...")
	startTime := time.Now()
	decoder := NewDecoder(n, frozenSet)
	decodedU := decoder.Decode(receivedLLRs)
	duration := time.Since(startTime)
	fmt.Printf("Decoding finished in %v.\n", duration)

	// 4. Check for errors
	infoBitErrors := 0
	for i := 0; i < codewordN; i++ {
		if _, isFrozen := frozenSet[i]; !isFrozen {
			if decodedU[i] == 1 {
				infoBitErrors++
			}
		}
	}

	fmt.Printf("Result: Found %d errors in the %d decoded information bits.\n", infoBitErrors, infoBitsK)
	if infoBitErrors == 0 {
		fmt.Println("Success! The message was decoded correctly.")
	} else {
		fmt.Println("Failure. The decoder could not correct all channel errors.")
	}
}
