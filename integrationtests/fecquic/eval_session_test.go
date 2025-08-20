package fecquic_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/fec"
	"github.com/quic-go/quic-go/fecquic"
	quicproxy "github.com/quic-go/quic-go/integrationtests/tools/proxy"
)

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
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"fecquic-eval"}}
	clientTLS := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"fecquic-eval"}}
	return serverTLS, clientTLS, nil
}

func TestFECQUIC_Evaluation(t *testing.T) {
	serverTLS, clientTLS, err := genTLSConfig()
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	root := repoRoot(t)
	_ = os.MkdirAll(filepath.Join(root, "test_data"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "results"), 0o755)
	dataPath := filepath.Join(root, "test_data", "train_FD001.txt")
	src, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("read src: %v", err)
	}

	outCSV := filepath.Join(root, "results", "fec_results.csv")
	f, err := os.OpenFile(outCSV, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		_ = w.Write([]string{"scheme", "loss", "frame_completion_ratio", "download_time_ms", "throughput_bps", "goodput_bps"})
		w.Flush()
	}

	schemes := []fec.Scheme{fec.XOR, fec.RLC, fec.Polar}
	losses := []float64{0.0, 0.005}

	for _, scheme := range schemes {
		for _, loss := range losses {
			res, err := runOnce(t, scheme, loss, serverTLS, clientTLS, src)
			if err != nil {
				t.Logf("run failed (%s, loss=%.4f): %v", scheme, loss, err)
				continue
			}
			_ = w.Write([]string{string(scheme), fmt.Sprintf("%.4f", loss), fmt.Sprintf("%.4f", res.FrameCompletion), fmt.Sprintf("%.0f", res.DownloadMS), fmt.Sprintf("%.0f", res.Throughput), fmt.Sprintf("%.0f", res.Goodput)})
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

func runOnce(t *testing.T, scheme fec.Scheme, loss float64, serverTLS, clientTLS *tls.Config, src []byte) (metrics, error) {
	k := 8
	r := 2
	if scheme == fec.XOR {
		r = 1
	}
	chunkLen := 900
	fileSize := len(src)

	ln, err := quic.ListenAddr("127.0.0.1:0", serverTLS, &quic.Config{EnableDatagrams: true, MaxIncomingStreams: 16384, MaxIncomingUniStreams: 16384})
	if err != nil {
		return metrics{}, err
	}
	defer ln.Close()

	p := &quicproxy.Proxy{Conn: newUDPConnLocalhost(t), ServerAddr: ln.Addr().(*net.UDPAddr)}
	p.DropPacket = func(_ quicproxy.Direction, _ net.Addr, _ net.Addr, _ []byte) bool { return mrand.Float64() < loss }
	if err := p.Start(); err != nil {
		return metrics{}, err
	}
	defer p.Close()

	recvPath := filepath.Join(repoRoot(t), "test_data", fmt.Sprintf("received_%s.txt", string(scheme)))
	var frameCompleted float64
	done := make(chan struct{})
	var serverErr error
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := ln.Accept(ctx)
		if err != nil {
			serverErr = err
			return
		}
		defer conn.CloseWithError(0, "")
		data, stats, err := fecquic.Receive(ctx, conn)
		if err != nil {
			serverErr = err
			return
		}
		frameCompleted = stats.FrameCompletion
		_ = os.WriteFile(recvPath, data, 0o644)
	}()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, p.LocalAddr().String(), clientTLS, &quic.Config{EnableDatagrams: true, MaxIncomingStreams: 16384, MaxIncomingUniStreams: 16384})
	if err != nil {
		return metrics{}, err
	}
	defer conn.CloseWithError(0, "")
	if err := fecquic.Send(ctx, conn, src, fecquic.Params{Scheme: scheme, K: k, R: r, ChunkLen: chunkLen}); err != nil {
		return metrics{}, err
	}
	<-done
	if serverErr != nil {
		return metrics{}, serverErr
	}
	elapsed := time.Since(start)

	blocks := int(math.Ceil(float64(fileSize) / float64(k*chunkLen)))
	header := 14
	tail := 0
	switch scheme {
	case fec.RLC:
		tail = k
	case fec.Polar:
		tail = (k + 7) / 8
	default:
		tail = 0
	}
	sentBytes := blocks * (k*(header+chunkLen) + r*(header+chunkLen+tail))
	throughput := float64(sentBytes) * 8.0 / elapsed.Seconds()
	goodput := float64(fileSize) * 8.0 / elapsed.Seconds()
	return metrics{FrameCompletion: frameCompleted, DownloadMS: float64(elapsed.Milliseconds()), Throughput: throughput, Goodput: goodput}, nil
}

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
	return wd
}

func newUDPConnLocalhost(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("udp: %v", err)
	}
	return conn
}
