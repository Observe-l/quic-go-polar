package fec_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go/fec"
)

func TestPolarPerformance_EncodeDecodeFile(t *testing.T) {
	root := repoRoot(t)
	srcPath := filepath.Join(root, "test_data", "train_FD001.txt")
	dstPath := filepath.Join(root, "test_data", "decode_FD001.txt")
	idxPath := filepath.Join(root, "fec", "encoding_index.bin")

	// Load file
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read src: %v", err)
	}

	// Load reliability indices
	encodingIndex, err := fec.LoadEncodingIndex(idxPath)
	if err != nil {
		t.Fatalf("load encoding index: %v", err)
	}

	const frameSize = 512
	const Kbatch = 32 // must be power of two and <= 1024 (codeword bits)

	encTotal := time.Duration(0)
	decTotal := time.Duration(0)

	out := make([]byte, 0, len(src))
	// Process the file in batches of Kbatch frames
	for offset := 0; offset < len(src); {
		// Build up to Kbatch frames for this batch
		frames := make([][]byte, Kbatch)
		framesInBatch := 0
		for i := 0; i < Kbatch; i++ {
			if offset >= len(src) {
				frames[i] = make([]byte, frameSize) // zero padded
				continue
			}
			end := offset + frameSize
			if end > len(src) {
				end = len(src)
			}
			frames[i] = make([]byte, frameSize)
			copy(frames[i], src[offset:end])
			offset = end
			framesInBatch++
		}

		t0 := time.Now()
		packets, randomMap, _, err := fec.EncodeAndBitInterleaveFrames(frames, encodingIndex)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		encTotal += time.Since(t0)

		// Simulate loss: drop exactly 1 packet out of the 32 for this batch (random index)
		dropIdx := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(Kbatch)
		packets[dropIdx] = nil

		t1 := time.Now()
		recFrames, err := fec.DecodeAndRecoverFrames(packets, randomMap, encodingIndex)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		decTotal += time.Since(t1)

		// Append only the frames actually filled from the source
		for i := 0; i < framesInBatch; i++ {
			out = append(out, recFrames[i]...)
		}
	}

	if len(out) > len(src) {
		out = out[:len(src)]
	}
	if err := os.WriteFile(dstPath, out, 0o644); err != nil {
		t.Fatalf("write decoded: %v", err)
	}
	if !bytes.Equal(src, out) {
		t.Fatalf("decoded output mismatches input")
	}

	total := encTotal + decTotal
	t.Logf("Polar encode time (total): %v", encTotal)
	t.Logf("Polar decode time (total): %v", decTotal)
	t.Logf("Total time: %v", total)
	fmt.Printf("Polar encode(total)=%v, decode(total)=%v, total=%v\n", encTotal, decTotal, total)
}
