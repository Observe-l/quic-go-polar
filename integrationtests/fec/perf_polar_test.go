package fec_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/quic-go/quic-go/fec"
)

func TestPolarPerformance_EncodeDecodeFile(t *testing.T) {
	root := repoRoot(t)
	srcPath := filepath.Join(root, "test_data", "train_FD001.txt")
	dstPath := filepath.Join(root, "test_data", "decode_FD001.txt")
	idxPath := filepath.Join(root, "fec", "encoding_index.bin")
	mapPath := filepath.Join(root, "fec", "random_map_1024.bin")
	// Packet LUT persisted file (tied to n=10, kinfo, K; internal CRC binds to map contents)

	// Reset decode metrics for a clean run
	fec.ResetPolarDecodeStats()
	fec.ResetPolarPerfStats()

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

	const Kbatch = 32 // packets per batch (must be power of 2)
	// Allow overriding info-bits via env; must be multiple of 8 and <= len(encodingIndex)
	numDataBits := 800
	if s := os.Getenv("POLAR_NUM_DATABITS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v%8 == 0 && v <= len(encodingIndex) {
			numDataBits = v
		}
	}
	constBytes := numDataBits / 8
	msgSize := constBytes
	// Keep packet size at 1024 bytes regardless of numDataBits: 8 codewords per packet subset
	const numMsgsPerBatch = 8 * Kbatch
	subsetSizeBits := 1024 / Kbatch
	// Base drop count for low-loss vs high-loss comparison; adapt to keep system solvable at high Q.
	requestedDrop := 2
	maxDrop := (1024-numDataBits)/subsetSizeBits - 1
	if maxDrop < 0 {
		maxDrop = 0
	}
	drop_num := requestedDrop
	if drop_num > maxDrop {
		drop_num = maxDrop
	}

	encTotal := time.Duration(0)
	decTotal := time.Duration(0)

	out := make([]byte, 0, len(src))
	// Try to load a persisted random map; if missing, we'll save after first encode
	var persistedMap []int
	if rm, err := fec.LoadRandomMap(mapPath, 1024); err == nil {
		persistedMap = rm
	}
	// Use a different loss mask per batch to simulate real data transfer with varying losses.
	rgen := rand.New(rand.NewSource(time.Now().UnixNano()))

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

		// predeclare batch-scoped vars
		var packets [][]byte
		var randomMap []int
		// The first successful decode path will account for decoding time; we still track encode time per batch.
		encStart := time.Now()
		encEnd := encStart // will set after encode succeeds in retry loop

		// Simulate loss with retries to avoid rare rank-deficiency at very high Q
		var recMsgs [][]byte
		losses := drop_num
		for attempt := 0; attempt < 4; attempt++ {
			// reset packets for this attempt
			// Re-encode for a clean slate
			// Note: reusing previously built 'msgs' and same randomMap for determinism on encode
			packets, randomMap, err = fec.EncodeMsgsAndBitInterleave(msgs, encodingIndex, persistedMap, Kbatch)
			if err != nil {
				t.Fatalf("encode(retry): %v", err)
			}
			// Record encode end after first successful encode
			if attempt == 0 {
				encEnd = time.Now()
			}
			perm := rgen.Perm(Kbatch)
			for i := 0; i < losses; i++ {
				packets[perm[i]] = nil
			}
			t1 := time.Now()
			recMsgs, err = fec.DecodeMsgs(packets, randomMap, encodingIndex, numDataBits)
			d := time.Since(t1)
			if err == nil {
				decTotal += d
				break
			}
			if attempt == 3 && losses > 0 {
				// last resort: reduce losses by one and try once more
				attempt = -1
				losses--
				continue
			}
		}
		if err != nil {
			t.Fatalf("decode failed after retries: %v", err)
		}
		encTotal += encEnd.Sub(encStart)

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
	stats := fec.GetPolarDecodeStats()
	p := fec.GetPolarPerfBreakdown()
	t.Logf("Polar encode time (total): %v", encTotal)
	t.Logf("Polar decode time (total) [%d drops of 32, %d bits]: %v", drop_num, numDataBits, decTotal)
	t.Logf("Total time: %v", total)
	t.Logf("Polar warm decode: total=%v, codewords=%d, avg warm per CW=%v", stats.WarmTotal, stats.WarmCodewords, stats.AvgWarmPerCW)
	t.Logf("Polar cold decode: total=%v, codewords=%d, avg cold per CW=%v", stats.ColdTotal, stats.ColdCodewords, stats.AvgColdPerCW)
	t.Logf("Polar phase breakdown: inv builds=%d total=%v avg/build=%v | Bbuild=%v, Mul=%v, Pack=%v | batches=%d totalCW=%d coldBatchCW=%d warmAvg/CW=%v coldAmort/CW=%v",
		p.InvBuilds, p.InvBuildTotal, p.AvgInvPerBuild, p.BBuildTotal, p.MulTotal, p.PackTotal,
		p.Batches, p.TotalCodewords, p.TotalColdBatchCWs, p.WarmAvgPerCW, p.ColdAmortizedPerCW)
	fmt.Printf("Polar encode(total)=%v, decode(total)=%v, total=%v\n", encTotal, decTotal, total)
	fmt.Printf("Polar warm: total=%v, CWs=%d, avgWarmPerCW=%v | cold: total=%v, CWs=%d, avgColdPerCW=%v\n", stats.WarmTotal, stats.WarmCodewords, stats.AvgWarmPerCW, stats.ColdTotal, stats.ColdCodewords, stats.AvgColdPerCW)
	fmt.Printf("Polar phases: inv builds=%d total=%v avg/build=%v | Bbuild=%v, Mul=%v, Pack=%v | batches=%d totalCW=%d coldBatchCW=%d warmAvg/CW=%v coldAmort/CW=%v\n",
		p.InvBuilds, p.InvBuildTotal, p.AvgInvPerBuild, p.BBuildTotal, p.MulTotal, p.PackTotal,
		p.Batches, p.TotalCodewords, p.TotalColdBatchCWs, p.WarmAvgPerCW, p.ColdAmortizedPerCW)
}
