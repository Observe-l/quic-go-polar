package fec_test

import (
	"bytes"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go/fec"
)

func TestPacketLevelPolar_Perf(t *testing.T) {
	root := repoRoot(t)
	srcPath := filepath.Join(root, "test_data", "train_FD001.txt")
	dstPath := filepath.Join(root, "test_data", "decode_packet_level.txt")

	// --- Editable parameters ---
	N := 32         // total packets per block (power of two)
	K := 16         // source packets per block
	L := 1500       // bytes per packet
	drops := 4      // packets dropped per block
	epsilon := 0.13 // BEC epsilon for A selection
	// ---------------------------

	if N&(N-1) != 0 {
		t.Fatalf("N must be power of two")
	}
	if K <= 0 || K >= N {
		t.Fatalf("invalid K for N")
	}
	R := N - K

	t.Logf("Packet-Polar Params: N=%d, K=%d, R=%d, L=%d, drops=%d, eps=%.3f", N, K, R, L, drops, epsilon)
	fmt.Printf("Packet-Polar Params: N=%d, K=%d, R=%d, L=%d, drops=%d, eps=%.3f\n", N, K, R, L, drops, epsilon)

	// Load file
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read src: %v", err)
	}

	// Build params
	p, err := fec.NewPacketPolarParams(N, K, epsilon, L)
	if err != nil {
		t.Fatalf("params: %v", err)
	}

	encTotal := time.Duration(0)
	decTotal := time.Duration(0)
	blocks := 0

	out := make([]byte, 0, len(src))
	rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))

	// Process the file in blocks of K*L bytes
	for off := 0; off < len(src); {
		// Prepare K source packets
		srcPkts := make([][]byte, K)
		blockBytes := 0
		for i := 0; i < K; i++ {
			pkt := make([]byte, L)
			if off < len(src) {
				n := len(src) - off
				if n > L {
					n = L
				}
				copy(pkt, src[off:off+n])
				off += n
				blockBytes += n
			}
			srcPkts[i] = pkt
		}

		// Encode parity
		tEnc := time.Now()
		parPkts := fec.PacketPolarEncode(p, srcPkts)
		encTotal += time.Since(tEnc)

		// Build received set with drops
		idx := rng.Perm(N)
		recv := make([]fec.Packet, 0, N-drops)
		// mark dropped indices
		dropped := make(map[int]struct{}, drops)
		for i := 0; i < drops && i < len(idx); i++ {
			dropped[idx[i]] = struct{}{}
		}
		for i := 0; i < N; i++ {
			if _, bad := dropped[i]; bad {
				continue
			}
			if i < K {
				recv = append(recv, fec.Packet{Index: i, Data: srcPkts[i]})
			} else {
				j := i - K
				recv = append(recv, fec.Packet{Index: i, Data: parPkts[j]})
			}
		}

		// Decode
		tDec := time.Now()
		decSrc, ok := fec.PacketPolarDecode(p, recv)
		d := time.Since(tDec)
		if !ok {
			t.Fatalf("decode failed at block %d (drops=%d)", blocks, drops)
		}
		decTotal += d
		blocks++

		// Append only the original bytes in this block (blockBytes)
		// concatenate K decoded packets and slice to blockBytes
		buf := make([]byte, 0, K*L)
		for i := 0; i < K; i++ {
			buf = append(buf, decSrc[i]...)
		}
		if blockBytes > len(buf) {
			blockBytes = len(buf)
		}
		out = append(out, buf[:blockBytes]...)
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
	t.Logf("Packet-Polar encode time (total): %v", encTotal)
	t.Logf("Packet-Polar decode time (total) [drops/block=%d]: %v", drops, decTotal)
	t.Logf("Packet-Polar blocks: %d; total time: %v", blocks, total)
	fmt.Printf("Packet-Polar encode(total)=%v, decode(total)=%v, blocks=%d, total=%v\n", encTotal, decTotal, blocks, total)
}
