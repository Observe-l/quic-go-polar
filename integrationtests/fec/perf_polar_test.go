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
	srcPath := filepath.Join(root, "test_data", "test_FD001.txt")
	dstPath := filepath.Join(root, "test_data", "decode_FD001.txt")
	idxPath := filepath.Join(root, "fec", "encoding_index.bin")
	mapPath := filepath.Join(root, "fec", "random_map_1024.bin")
	// Packet LUT persisted file (tied to n=10, kinfo, K; internal CRC binds to map contents)

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

	const Kbatch = 32                       // packets per batch
	const numDataBits = 8                   // 16 bytes per codeword
	const msgSize = numDataBits / 8         // 16 bytes
	const numMsgsPerBatch = 1024 / 128 * 32 // keeps packet size = 1024 bytes (256 * 32 bits)
	const drop_num = 9

	encTotal := time.Duration(0)
	decTotal := time.Duration(0)

	out := make([]byte, 0, len(src))
	// Try to load a persisted random map; if missing, we'll save after first encode
	var persistedMap []int
	if rm, err := fec.LoadRandomMap(mapPath, 1024); err == nil {
		persistedMap = rm
	}
	// Process the file in batches of messages
	for offset := 0; offset < len(src); {
		// Build up to numMsgsPerBatch messages (16 bytes each)
		msgs := make([][]byte, numMsgsPerBatch)
		msgsInBatch := 0
		for i := 0; i < numMsgsPerBatch; i++ {
			if offset >= len(src) {
				msgs[i] = make([]byte, msgSize) // zero padded
				continue
			}
			end := offset + msgSize
			if end > len(src) {
				end = len(src)
			}
			msg := make([]byte, msgSize)
			copy(msg, src[offset:end])
			msgs[i] = msg
			offset = end
			msgsInBatch++
		}

		t0 := time.Now()
		packets, randomMap, err := fec.EncodeMsgsAndBitInterleave(msgs, encodingIndex, persistedMap, Kbatch)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if persistedMap == nil {
			// Save the map for future runs
			if err := fec.SaveRandomMap(mapPath, randomMap); err == nil {
				persistedMap = randomMap
			}
		}
		encTotal += time.Since(t0)

		// Simulate loss: drop exactly 10 distinct packets out of 32 for this batch
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		perm := r.Perm(Kbatch)
		for i := 0; i < drop_num; i++ {
			packets[perm[i]] = nil
		}

		t1 := time.Now()
		recMsgs, err := fec.DecodeMsgs(packets, randomMap, encodingIndex, numDataBits)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		decTotal += time.Since(t1)

		// Append only the messages actually filled from the source
		for i := 0; i < msgsInBatch; i++ {
			out = append(out, recMsgs[i]...)
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
	t.Logf("Polar decode time (total) [%d drops of 32, 128 bits]: %v", drop_num, decTotal)
	t.Logf("Total time: %v", total)
	fmt.Printf("Polar encode(total)=%v, decode(total)=%v, total=%v\n", encTotal, decTotal, total)
}
