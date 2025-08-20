package fecquic

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"time"

	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/fec"
)

// Params configure the FEC coding.
// K data chunks produce R parity chunks per block, each chunk has ChunkLen bytes.
type Params struct {
	Scheme   fec.Scheme
	K, R     int
	ChunkLen int
}

// Stats summarize a receive session.
type Stats struct {
	FrameCompletion float64 // fraction of data chunks recovered (K*blocks denominator)
	ReceivedBytes   int     // number of bytes returned in the payload (trimmed to original size)
}

// startMsg is sent on a unidirectional stream to announce a transmission.
type startMsg struct {
	Scheme    string
	K         int
	R         int
	ChunkLen  int
	NumBlocks int
	FileSize  int
}

// datagramHeader describes a data or parity chunk in a DATAGRAM.
type datagramHeader struct {
	BlockID  uint32
	Kind     byte   // 0=data, 1=parity
	Index    uint16 // within data (0..K-1) or parity (0..R-1)
	K        uint8
	R        uint8
	Scheme   uint8  // 0=xor,1=rlc,2=polar
	CoeffLen uint16 // optional tail size for RLC
	MetaLen  uint16 // optional tail size for Polar
}

func (h *datagramHeader) size() int { return 4 + 1 + 2 + 1 + 1 + 1 + 2 + 2 }

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

func decodeHeader(h *datagramHeader, b []byte) error {
	if len(b) < h.size() {
		return errors.New("fecquic: datagram header too small")
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

// Send transmits data using QUIC DATAGRAMs protected by FEC parity.
// A small control message is sent first on a unidirectional stream.
// Requires that the connection was created with EnableDatagrams=true.
func Send(ctx context.Context, conn *quic.Conn, data []byte, p Params) error {
	if p.K <= 0 || p.ChunkLen <= 0 {
		return errors.New("fecquic: invalid params")
	}
	if p.Scheme == fec.XOR {
		if p.R < 1 {
			p.R = 1
		}
	} else if p.Scheme == fec.None {
		// Fallback: reliable send on a stream.
		ws, err := conn.OpenUniStream()
		if err != nil {
			return err
		}
		if _, err := ws.Write(data); err != nil {
			return err
		}
		return ws.Close()
	} else if p.R <= 0 { // default parity if not set
		p.R = 1
	}

	fileSize := len(data)
	blocks := (fileSize + p.K*p.ChunkLen - 1) / (p.K * p.ChunkLen)
	// announce transfer
	ctrl, err := conn.OpenUniStream()
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(ctrl)
	if err := enc.Encode(&startMsg{Scheme: string(p.Scheme), K: p.K, R: p.R, ChunkLen: p.ChunkLen, NumBlocks: blocks, FileSize: fileSize}); err != nil {
		return err
	}
	_ = ctrl.Close()

	// send blocks
	for bi := 0; bi < blocks; bi++ {
		// slice out K data chunks, zero-padded
		chunks := make([][]byte, p.K)
		for i := 0; i < p.K; i++ {
			off := (bi*p.K + i) * p.ChunkLen
			end := off + p.ChunkLen
			b := make([]byte, p.ChunkLen)
			if off < fileSize {
				if end > fileSize {
					end = fileSize
				}
				copy(b, data[off:end])
			}
			chunks[i] = b
		}
		blk := fec.Block{ID: uint32(bi), K: p.K, R: p.R, ChunkLen: p.ChunkLen}
		res, _ := fec.Encode(p.Scheme, blk, chunks)
		// data datagrams
		for i := 0; i < p.K; i++ {
			payload := buildDatagram(blk.ID, 0, uint16(i), p, nil, nil, chunks[i])
			_ = conn.SendDatagram(payload)
			// small pacing to avoid internal drops
			time.Sleep(150 * time.Microsecond)
		}
		// parity datagrams
		for j := 0; j < p.R; j++ {
			var coeff, meta []byte
			if p.Scheme == fec.RLC {
				if res != nil && j < len(res.Coeffs) {
					coeff = res.Coeffs[j]
				}
			} else if p.Scheme == fec.Polar {
				if res != nil && j < len(res.Meta) {
					meta = res.Meta[j]
				}
			}
			payload := buildDatagram(blk.ID, 1, uint16(j), p, coeff, meta, res.Parity[j])
			_ = conn.SendDatagram(payload)
			time.Sleep(150 * time.Microsecond)
		}
	}
	return nil
}

// Receive waits for a single transfer announced by the peer and returns the recovered data.
// The receive completes when the context expires or when all blocks are complete.
// Requires that the connection was created with EnableDatagrams=true.
func Receive(ctx context.Context, conn *quic.Conn) ([]byte, Stats, error) {
	// read control stream
	rs, err := conn.AcceptUniStream(ctx)
	if err != nil {
		return nil, Stats{}, err
	}
	dec := gob.NewDecoder(rs)
	var start startMsg
	if err := dec.Decode(&start); err != nil {
		return nil, Stats{}, err
	}

	K, R, chunkLen := start.K, start.R, start.ChunkLen
	blocks := start.NumBlocks
	scheme := fec.Scheme(start.Scheme)

	// allocate buffers
	bufBlocks := make([][][]byte, blocks)
	present := make([][]bool, blocks)
	parities := make([][][]byte, blocks)
	coeffs := make([][][]byte, blocks)
	meta := make([][][]byte, blocks)
	for i := 0; i < blocks; i++ {
		bufBlocks[i] = make([][]byte, K)
		present[i] = make([]bool, K)
		for j := 0; j < K; j++ {
			bufBlocks[i][j] = make([]byte, chunkLen)
		}
	}

	// receive loop
	deadline := time.After(10 * time.Second)
	for {
		if allBlocksComplete(present) {
			break
		}
		select {
		case <-deadline:
			goto done
		default:
		}
		rctx, rcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		b, err := conn.ReceiveDatagram(rctx)
		rcancel()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		var h datagramHeader
		if decodeHeader(&h, b) != nil {
			continue
		}
		bi := int(h.BlockID)
		if bi < 0 || bi >= blocks {
			continue
		}
		off := (&datagramHeader{}).size()
		coeffTail := b[off : off+int(h.CoeffLen)]
		off += int(h.CoeffLen)
		metaTail := b[off : off+int(h.MetaLen)]
		off += int(h.MetaLen)
		payload := b[off:]
		if int(h.Kind) == 0 { // data
			idx := int(h.Index)
			if idx < K && !present[bi][idx] {
				copy(bufBlocks[bi][idx], payload)
				present[bi][idx] = true
			}
		} else { // parity
			parities[bi] = append(parities[bi], append([]byte(nil), payload...))
			if scheme == fec.RLC {
				coeffs[bi] = append(coeffs[bi], append([]byte(nil), coeffTail...))
			}
			if scheme == fec.Polar {
				meta[bi] = append(meta[bi], append([]byte(nil), metaTail...))
			}
		}
		// attempt recovery on block
		for bi := 0; bi < blocks; bi++ {
			if countTrue(present[bi]) == K {
				continue
			}
			blk := fec.Block{ID: uint32(bi), K: K, R: R, ChunkLen: chunkLen}
			_, _ = fec.Recover(scheme, blk, bufBlocks[bi], present[bi], parities[bi], coeffs[bi], meta[bi])
		}
	}
done:
	// assemble output, trimming to FileSize
	var out bytes.Buffer
	for bi := 0; bi < blocks; bi++ {
		for i := 0; i < K; i++ {
			out.Write(bufBlocks[bi][i])
		}
	}
	outBytes := out.Bytes()
	n := start.FileSize
	if len(outBytes) < n {
		n = len(outBytes)
	}
	outBytes = outBytes[:n]
	completed := 0
	for bi := 0; bi < blocks; bi++ {
		completed += countTrue(present[bi])
	}
	total := blocks * K
	fc := 0.0
	if total > 0 {
		fc = float64(completed) / float64(total)
	}
	return outBytes, Stats{FrameCompletion: fc, ReceivedBytes: len(outBytes)}, nil
}

func buildDatagram(blockID uint32, kind byte, index uint16, p Params, coeff, meta, payload []byte) []byte {
	var buf bytes.Buffer
	h := datagramHeader{BlockID: blockID, Kind: kind, Index: index, K: uint8(p.K), R: uint8(p.R), Scheme: schemeToByte(p.Scheme)}
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

// helpers (duplicated locally to avoid exporting internals from fec/codec.go)

func countTrue(b []bool) int {
	c := 0
	for _, v := range b {
		if v {
			c++
		}
	}
	return c
}

func allBlocksComplete(p [][]bool) bool {
	for _, r := range p {
		if countTrue(r) != len(r) {
			return false
		}
	}
	return true
}
