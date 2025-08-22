package fec

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	// quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/fec"
)

func TestPolar(t *testing.T) {
	// n := 4
	// gnMatrix := fec.PolarGeneratorMatrix(n)
	// formatted := mat.Formatted(gnMatrix, mat.Prefix(""), mat.Squeeze())
	// t.Logf("Generator matrix for n=%d:\n%s", n, formatted)
	// // fmt.Printf("Polar Generator Matrix G_N for N=%d (n=%d):\n%.0f\n", 1<<n, n, formatted)
	// N := 1 << n
	// dataSize := 1 // 1 byte = 8 bits
	// inputData := make([]byte, dataSize)

	// // Use crypto/rand for high-quality random data.
	// _, err := rand.Read(inputData)
	// if err != nil {
	// 	t.Fatalf("Failed to generate random input data: %v", err)
	// }
	// fmt.Printf("Encoding %d bytes of random data with n=%d.\n", dataSize, n)
	// fmt.Printf("This will be padded with %d zero-bytes to fill the 16-bit block.\n", N/8-dataSize)
	// // Print a sample of the random input data
	// fmt.Printf("Random input data (first 16 bytes is): %x\n", inputData)

	// // 3. Encode the data. The EncodePolarGN function will handle the padding automatically.
	// encodedData, err := fec.EncodePolarGN(inputData, n)
	// if err != nil {
	// 	t.Fatalf("Failed to encode data: %v", err)
	// }

	// // 4. Print the results. The output is a full 128-byte codeword.
	// fmt.Printf("Successfully encoded the data.\n")
	// fmt.Printf("Encoded codeword length: %d bytes (%d bits)\n", len(encodedData), len(encodedData)*8)
	// // Print a sample of the encoded output data
	// fmt.Printf("Encoded data (first 16 bytes) is: %x\n", encodedData)

	K := 32 // Number of data frames
	const frameSize = 512

	// 1. Generate K random data frames.
	fmt.Printf("Generating K=%d random data frames...\n\n", K)
	dataFrames := make([][]byte, K)
	for i := 0; i < K; i++ {
		dataFrames[i] = make([]byte, frameSize)
		_, err := rand.Read(dataFrames[i])
		if err != nil {
			t.Fatalf("Failed to generate random frame %d: %v", i, err)
		}
	}

	// 2. Run the full encoding pipeline.
	finalPackets, randomMap, originalCodewordsForVerification, err := fec.EncodeAndBitInterleaveFrames(dataFrames)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("\n--- Encoding Complete ---")
	fmt.Printf("Output: %d packets, %d bytes/packet\n", len(finalPackets), len(finalPackets[0]))
	fmt.Printf("Random INTRA-CODEWORD map size: %d elements\n", len(randomMap))

	// --- 3. Run the Recovery Pipeline ---
	fmt.Println("\n--- Starting Recovery ---")
	recoveredCodewords, err := fec.DeinterleaveAndReassemble(finalPackets, randomMap)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("\n--- Recovery Complete ---")
	fmt.Printf("Reassembled %d codewords of %d bytes each.\n", len(recoveredCodewords), len(recoveredCodewords[0]))

	// --- 4. Verification ---
	fmt.Println("\n--- Verification ---")
	if len(originalCodewordsForVerification) != len(recoveredCodewords) {
		t.Fatalf("Verification failed: Mismatched number of codewords.")
	}

	// Compare the first and last codeword as a sanity check
	if !bytes.Equal(originalCodewordsForVerification[0], recoveredCodewords[0]) {
		t.Fatalf("Verification FAILED: First original and recovered codewords do not match!")
	}
	lastIdx := len(recoveredCodewords) - 1
	if !bytes.Equal(originalCodewordsForVerification[lastIdx], recoveredCodewords[lastIdx]) {
		t.Fatalf("Verification FAILED: Last original and recovered codewords do not match!")
	}

	fmt.Println("Verification PASSED: Original and recovered codewords match successfully!")
}
