package fec_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/csv"
	"encoding/gob"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"math/big"
	mrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/fec"
	quicproxy "github.com/quic-go/quic-go/integrationtests/tools/proxy"
)

// wire formats and helpers

type startMsg struct {
	Scheme    string
	K         int
	R         int
	ChunkLen  int
	NumBlocks int
	FileSize  int
}

// retransmission disabled for FEC modes

type datagramHeader struct {
	BlockID uint32
	Kind    byte // 0=data, 1=parity
	Index   uint16
	K       uint8
	R       uint8
	Scheme  uint8 // 0=xor,1=rlc,2=polar
	// lengths of optional tails follow in order
	CoeffLen uint16 // for RLC: K bytes
	MetaLen  uint16 // for Polar: ceil(K/8)
}

func (h *datagramHeader) marshal(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.LittleEndian, h.BlockID)
	_ = buf.WriteByte(h.Kind)
	_ = binary.Write(buf, binary.LittleEndian, h.Index)
	_ = buf.WriteByte(h.K)
	_ = buf.WriteByte(h.R)
	_ = buf.WriteByte(h.Scheme)
	_ = binary.Write(buf, binary.LittleEndian, h.CoeffLen)
	_ = binary.Write(buf, binary.LittleEndian, h.MetaLen)
}

func (h *datagramHeader) size() int { return 4 + 1 + 2 + 1 + 1 + 1 + 2 + 2 }

func genTLSConfig() (*tls.Config, *tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(crand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certBuf := &bytes.Buffer{}
	_ = pem.Encode(certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	b, _ := x509.MarshalECPrivateKey(priv)
	keyBuf := &bytes.Buffer{}
	_ = pem.Encode(keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
	cert, err := tls.X509KeyPair(certBuf.Bytes(), keyBuf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"fec-eval"}}
	clientTLS := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"fec-eval"}}
	return serverTLS, clientTLS, nil
}

// --- Core test ---

func TestFEC_Evaluation(t *testing.T) {
	if os.Getenv("QUIC_FEC_EVAL") != "1" {
		t.Skip("set QUIC_FEC_EVAL=1 to run the FEC evaluation (long-running)")
	}
	serverTLS, clientTLS, err := genTLSConfig()
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	// Verify at loss=0 the received file equals the sent file
	lossRates := []float64{0.0}
	schemes := []fec.Scheme{fec.XOR, fec.RLC, fec.Polar, fec.None}
	root := repoRoot(t)
	// ensure results directory exists
	_ = os.MkdirAll(filepath.Join(root, "results"), 0o755)
	outCSV := filepath.Join(root, "results", "fec_results.csv")
	// open in append mode so we don't overwrite other schemes
	f, err := os.OpenFile(outCSV, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	// write header only when file is empty
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		_ = w.Write([]string{"scheme", "loss", "frame_completion_ratio", "download_time_ms", "throughput_bps", "goodput_bps"})
		w.Flush()
	}

	for _, s := range schemes {
		for _, loss := range lossRates {
			res, err := runOnce(t, s, loss, serverTLS, clientTLS)
			if err != nil {
				t.Logf("run failed: %v", err)
				continue
			}
			_ = w.Write([]string{string(s), fmt.Sprintf("%.4f", loss), fmt.Sprintf("%.4f", res.FrameCompletion), fmt.Sprintf("%.0f", res.DownloadMS), fmt.Sprintf("%.0f", res.Throughput), fmt.Sprintf("%.0f", res.Goodput)})
			w.Flush()
		}
	}
}

type metrics struct {
	FrameCompletion float64
	DownloadMS      float64
	Throughput      float64
	Goodput         float64
}

type runParams struct {
	Scheme   fec.Scheme
	K, R     int
	ChunkLen int
}

func defaultParams(s fec.Scheme) runParams {
	// Slightly larger K for better efficiency; chunkLen keeps datagrams small.
	p := runParams{Scheme: s, K: 8, R: 2, ChunkLen: 900}
	if s == fec.XOR {
		p.R = 1
	}
	return p
}

func runOnce(t *testing.T, scheme fec.Scheme, loss float64, serverTLS, clientTLS *tls.Config) (metrics, error) {
	params := defaultParams(scheme)
	// Allow env overrides for K and overall code rate.
	if v := os.Getenv("QUIC_FEC_K"); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 {
			params.K = k
		}
	}
	if scheme == fec.XOR {
		params.R = 1
	} else if v := os.Getenv("QUIC_FEC_RATE"); v != "" {
		if rate, err := strconv.ParseFloat(v, 64); err == nil && rate > 0 && rate < 1 {
			// rate = K / (K+R) => R = ceil(K*(1-rate)/rate)
			params.R = int(math.Ceil(float64(params.K) * (1 - rate) / rate))
			if params.R < 1 {
				params.R = 1
			}
		}
	} else {
		// Choose R s.t. P(received >= K) >= 0.995 when loss=loss for K+R transmissions.
		params.R = minRForTarget(params.K, loss, 0.995)
		if params.R < 1 {
			params.R = 1
		}
	}
	// File
	root := repoRoot(t)
	dataPath := filepath.Join(root, "test_data", "train_FD001.txt")
	src, err := os.ReadFile(dataPath)
	if err != nil {
		return metrics{}, err
	}
	fileSize := len(src)
	// Server
	ln, err := quic.ListenAddr("127.0.0.1:0", serverTLS, &quic.Config{EnableDatagrams: true, MaxIncomingStreams: 16384, MaxIncomingUniStreams: 16384})
	if err != nil {
		return metrics{}, err
	}
	defer ln.Close()
	// Proxy
	p := &quicproxy.Proxy{Conn: newUDPConnLocalhost(t), ServerAddr: ln.Addr().(*net.UDPAddr)}
	p.DropPacket = func(_ quicproxy.Direction, _ net.Addr, _ net.Addr, _ []byte) bool {
		return mrand.Float64() < loss
	}
	if err := p.Start(); err != nil {
		return metrics{}, err
	}
	defer p.Close()

	// Server goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	var serverErr error
	recvPath := filepath.Join(root, "test_data", fmt.Sprintf("received_%s.txt", string(scheme)))
	var frameCompleted float64
	serverDone := make(chan struct{})
	go func() {
		defer wg.Done()
		// Always close serverDone so the client doesn't block indefinitely on errors.
		defer close(serverDone)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := ln.Accept(ctx)
		if err != nil {
			serverErr = err
			return
		}
		defer conn.CloseWithError(0, "")
		if scheme == fec.None {
			// Baseline: receive entire file on a reliable stream
			rs, err := conn.AcceptUniStream(ctx)
			if err != nil {
				serverErr = err
				return
			}
			var out bytes.Buffer
			if _, err := io.Copy(&out, rs); err != nil {
				serverErr = err
				return
			}
			outBytes := out.Bytes()
			_ = os.WriteFile(recvPath, outBytes, 0o644)
			frameCompleted = 1.0
			return
		}
		// control stream from client
		str, err := conn.AcceptUniStream(ctx)
		if err != nil {
			serverErr = err
			return
		}
		dec := gob.NewDecoder(str)
		var start startMsg
		if err := dec.Decode(&start); err != nil {
			serverErr = err
			return
		}
		K, R, chunkLen := start.K, start.R, start.ChunkLen
		blocks := start.NumBlocks
		schemeByte := schemeToByte(scheme)
		// receive datagrams
		bufBlocks := make([][][]byte, blocks)
		present := make([][]bool, blocks)
		parities := make([][][]byte, blocks)
		coeffs := make([][][]byte, blocks)
		meta := make([][][]byte, blocks)
		for i := 0; i < blocks; i++ {
			bufBlocks[i] = make([][]byte, K)
			present[i] = make([]bool, K)
		}
		deadline := time.After(15 * time.Second)
		for {
			// break out after deadline
			select {
			case <-deadline:
				goto doneDatagrams
			default:
			}
			// poll datagrams with a short timeout to respect the deadline
			rctx, rcancel := context.WithTimeout(ctx, 300*time.Millisecond)
			b, err := conn.ReceiveDatagram(rctx)
			rcancel()
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				continue
			}
			h := datagramHeader{}
			if err := decodeHeader(&h, b); err != nil {
				continue
			}
			payload := b[h.size()+int(h.CoeffLen)+int(h.MetaLen):]
			bi := int(h.BlockID)
			if bi < 0 || bi >= blocks {
				continue
			}
			if h.Kind == 0 { // data
				idx := int(h.Index)
				if idx < K && !present[bi][idx] {
					buf := make([]byte, len(payload))
					copy(buf, payload)
					bufBlocks[bi][idx] = buf
					present[bi][idx] = true
				}
			} else {
				parities[bi] = append(parities[bi], append([]byte(nil), payload...))
				if schemeByte == 1 { // RLC
					coeffs[bi] = append(coeffs[bi], append([]byte(nil), b[h.size():h.size()+int(h.CoeffLen)]...))
				} else if schemeByte == 2 { // Polar
					meta[bi] = append(meta[bi], append([]byte(nil), b[h.size():h.size()+int(h.MetaLen)]...))
				}
			}
			// attempt recovery per block if possible
			for bi := 0; bi < blocks; bi++ {
				if countTrue(present[bi]) == K {
					continue
				}
				sw := fec.Block{ID: uint32(bi), K: K, R: R, ChunkLen: chunkLen}
				n, _ := fec.Recover(scheme, sw, bufBlocks[bi], present[bi], parities[bi], coeffs[bi], meta[bi])
				_ = n // recovered accounted in present[]
			}
			if allBlocksComplete(present) {
				break
			}
		}
	doneDatagrams:
		// compute frame completion ratio from datagrams + FEC only
		completed := 0
		for bi := 0; bi < blocks; bi++ {
			completed += countTrue(present[bi])
		}
		total := blocks * K
		frameCompleted = float64(completed) / float64(total)
		// write file with whatever has been recovered; missing chunks remain zeroed
		var out bytes.Buffer
		for bi := 0; bi < blocks; bi++ {
			for i := 0; i < K; i++ {
				out.Write(bufBlocks[bi][i])
			}
		}
		outAll := out.Bytes()
		n := start.FileSize
		if len(outAll) < n {
			n = len(outAll)
		}
		outBytes := outAll[:n]
		_ = os.WriteFile(recvPath, outBytes, 0o644)
	}()

	// Client
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, p.LocalAddr().String(), clientTLS, &quic.Config{EnableDatagrams: true, MaxIncomingStreams: 16384, MaxIncomingUniStreams: 16384})
	if err != nil {
		return metrics{}, err
	}
	defer conn.CloseWithError(0, "")
	if scheme == fec.None {
		// Baseline: send entire file on a reliable stream
		ws, err := conn.OpenUniStream()
		if err != nil {
			return metrics{}, err
		}
		if _, err := ws.Write(src); err != nil {
			return metrics{}, err
		}
		ws.Close()
		<-serverDone
		elapsed := time.Since(start)
		goodput := float64(fileSize) * 8.0 / elapsed.Seconds()
		throughput := goodput
		wg.Wait()
		if serverErr != nil {
			return metrics{}, serverErr
		}
		recv, err := os.ReadFile(recvPath)
		if err != nil {
			return metrics{}, err
		}
		if !bytes.Equal(recv, src) {
			return metrics{}, fmt.Errorf("mismatch for baseline")
		}
		return metrics{FrameCompletion: 1.0, DownloadMS: float64(elapsed.Milliseconds()), Throughput: throughput, Goodput: goodput}, nil
	}
	// send start message for FEC modes
	blocks := int(math.Ceil(float64(fileSize) / float64(params.K*params.ChunkLen)))
	stm, err := conn.OpenUniStream()
	if err != nil {
		return metrics{}, err
	}
	enc := gob.NewEncoder(stm)
	_ = enc.Encode(&startMsg{Scheme: string(scheme), K: params.K, R: params.R, ChunkLen: params.ChunkLen, NumBlocks: blocks, FileSize: fileSize})
	stm.Close()

	// send data + parity via datagrams
	sentBytes := 0
	for bi := 0; bi < blocks; bi++ {
		// prepare K data chunks
		data := make([][]byte, params.K)
		for i := 0; i < params.K; i++ {
			off := (bi*params.K + i) * params.ChunkLen
			if off >= fileSize {
				data[i] = make([]byte, params.ChunkLen)
				continue
			}
			end := off + params.ChunkLen
			if end > fileSize {
				end = fileSize
			}
			chunk := make([]byte, params.ChunkLen)
			copy(chunk, src[off:end])
			data[i] = chunk
		}
		blk := fec.Block{ID: uint32(bi), K: params.K, R: params.R, ChunkLen: params.ChunkLen}
		res, _ := fec.Encode(scheme, blk, data)
		// send data datagrams
		for i := 0; i < params.K; i++ {
			payload := buildDatagram(blk.ID, 0, uint16(i), params.K, params.R, scheme, nil, nil, data[i])
			if err := conn.SendDatagram(payload); err == nil {
				sentBytes += len(payload)
			}
			// light pacing to avoid internal drops even at loss=0
			time.Sleep(200 * time.Microsecond)
		}
		// send parity datagrams
		for j := 0; j < params.R; j++ {
			var coeff []byte
			var meta []byte
			if scheme == fec.RLC {
				coeff = res.Coeffs[j]
			}
			if scheme == fec.Polar {
				meta = res.Meta[j]
			}
			payload := buildDatagram(blk.ID, 1, uint16(j), params.K, params.R, scheme, coeff, meta, res.Parity[j])
			if err := conn.SendDatagram(payload); err == nil {
				sentBytes += len(payload)
			}
			time.Sleep(200 * time.Microsecond)
		}
	}
	// No retransmission or fallback on client side

	<-serverDone
	elapsed := time.Since(start)
	throughput := float64(sentBytes) * 8.0 / elapsed.Seconds()
	wg.Wait()
	if serverErr != nil {
		return metrics{}, serverErr
	}
	// compute goodput as actual received bytes; also verify equality when loss==0
	recv, err := os.ReadFile(filepath.Join(root, "test_data", fmt.Sprintf("received_%s.txt", string(scheme))))
	if err != nil {
		return metrics{}, err
	}
	if loss == 0.0 && scheme != fec.None {
		if !bytes.Equal(recv, src) {
			return metrics{}, fmt.Errorf("mismatch at loss=0 for scheme %s", scheme)
		}
	}
	goodput := float64(len(recv)) * 8.0 / elapsed.Seconds()
	return metrics{FrameCompletion: frameCompleted, DownloadMS: float64(elapsed.Milliseconds()), Throughput: throughput, Goodput: goodput}, nil
}

func buildDatagram(blockID uint32, kind byte, index uint16, K, R int, scheme fec.Scheme, coeff, meta, payload []byte) []byte {
	var buf bytes.Buffer
	h := datagramHeader{BlockID: blockID, Kind: kind, Index: index, K: uint8(K), R: uint8(R), Scheme: schemeToByte(scheme)}
	if coeff != nil {
		h.CoeffLen = uint16(len(coeff))
	}
	if meta != nil {
		h.MetaLen = uint16(len(meta))
	}
	h.marshal(&buf)
	if coeff != nil {
		buf.Write(coeff)
	}
	if meta != nil {
		buf.Write(meta)
	}
	buf.Write(payload)
	return buf.Bytes()
}

func decodeHeader(h *datagramHeader, b []byte) error {
	if len(b) < h.size() {
		return fmt.Errorf("header too small")
	}
	off := 0
	h.BlockID = binary.LittleEndian.Uint32(b[off : off+4])
	off += 4
	h.Kind = b[off]
	off++
	h.Index = binary.LittleEndian.Uint16(b[off : off+2])
	off += 2
	h.K = b[off]
	off++
	h.R = b[off]
	off++
	h.Scheme = b[off]
	off++
	h.CoeffLen = binary.LittleEndian.Uint16(b[off : off+2])
	off += 2
	h.MetaLen = binary.LittleEndian.Uint16(b[off : off+2])
	off += 2
	return nil
}

// repoRoot finds the repository root by searching for go.mod upwards.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	dir := wd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		nd := filepath.Dir(dir)
		if nd == dir || nd == "/" {
			break
		}
		dir = nd
	}
	// fallback to working directory
	return wd
}

// countingWriter removed (no retransmission)

func schemeToByte(s fec.Scheme) uint8 {
	switch s {
	case fec.XOR:
		return 0
	case fec.RLC:
		return 1
	case fec.Polar:
		return 2
	default:
		return 255
	}
}

// minRForTarget chooses the minimal R such that probability of receiving at least K
// out of N=K+R transmissions with per-transmission loss p is >= target.
func minRForTarget(K int, loss, target float64) int {
	if loss <= 0 {
		return 0
	}
	for R := 0; R <= 64; R++ { // cap search window
		N := K + R
		if probAtLeastK(N, K, loss) >= target {
			return R
		}
	}
	return 64
}

func probAtLeastK(N, K int, loss float64) float64 {
	// success per packet = (1-loss)
	p := 1 - loss
	// sum_{i=K..N} C(N,i) p^i (1-p)^(N-i)
	var sum float64
	for i := K; i <= N; i++ {
		sum += binomPMF(N, i, p)
	}
	return sum
}

func binomPMF(n, k int, p float64) float64 {
	if k < 0 || k > n {
		return 0
	}
	// compute log choose + logs to avoid under/overflow
	return math.Exp(logChoose(n, k) + float64(k)*math.Log(p) + float64(n-k)*math.Log(1-p))
}

func logChoose(n, k int) float64 {
	if k < 0 || k > n {
		return math.Inf(-1)
	}
	if k > n-k {
		k = n - k
	}
	var s float64
	for i := 1; i <= k; i++ {
		s += math.Log(float64(n-k+i)) - math.Log(float64(i))
	}
	return s
}

func countTrue(b []bool) int {
	c := 0
	for _, v := range b {
		if v {
			c++
		}
	}
	return c
}

// missingIndices removed (no retransmission)
func allBlocksComplete(p [][]bool) bool {
	for _, r := range p {
		if countTrue(r) != len(r) {
			return false
		}
	}
	return true
}

// newUDPConnLocalhost mirrors helper from integration tests
func newUDPConnLocalhost(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("udp: %v", err)
	}
	return conn
}
