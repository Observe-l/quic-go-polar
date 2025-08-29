# QUIC-FEC Design (quic-go + Packet-Level RLC / RS / Polar)
**Goal.** Use `quic-go` to transport a local file `test_data/train_FD001.txt` from client → server with **application-layer FEC**. We fix **(N,K) = (8,6)** per FEC block, target **loss rate = 0.5%**, and **no retransmission** after loss (use QUIC DATAGRAMS). The receiver reconstructs the file and saves it **in the same folder** as `train_FD001.recv.txt`.

---

## 1) Requirements & Constraints
- **Transport:** QUIC (using `quic-go`).
- **Reliability policy:** **No retransmissions** at the app level → use **QUIC DATAGRAM** frames (unreliable, unordered) to carry FEC-coded packets.
- **FEC block:** `N=8`, `K=6`, `R=2` parity packets per block.
- **File under test:** `test_data/train_FD001.txt`.
- **Loss model:** **0.5%** packet loss (Bernoulli i.i.d.) injected by **in-process dropper** (for reproducibility), **no repair / resend** beyond FEC.
- **Schemes:** **RLC** (GF(256)), **RS** (GF(256) MDS), **Packet-Level Polar** (systematic; XOR-only).
- **Receiver output:** Save to `test_data/train_FD001.recv.txt` (same directory).

---

## 2) High-Level Architecture

```
+----------------+     QUIC DATAGRAM     +----------------+
|  FEC Sender    |  ==================>  |  FEC Receiver  |
| (Client)       |                       |   (Server)     |
|                | <==================   |                |
|  (optional     |   ACKs/Control msgs   |  (optional)    |
|   control)     |   (QUIC STREAM)       |   control      |
+----------------+                        +----------------+
         |                                         |
  Read file, chunk → K symbols               Decode blocks, reassemble file
  Per block: encode to N symbols             Save out `recv_train_{fec}.txt`
  Drop ~0.5% DATAGRAMs (simulated)
```

- **Data path (FEC payload):** QUIC **DATAGRAM** frames (unreliable, unordered).
- **Control path (optional):** A single **QUIC STREAM** for metadata (block counts, file size, SHA-256, end-of-transfer marker).
- **Why DATAGRAM?** No retransmissions, immediate delivery, perfect for “FEC handles losses”.

---

## 3) FEC Block Model & Wire Format

### 3.1 Blockization
- **File → byte stream → chunk into K data symbols per block.**
- Each **symbol** size \(L\) in bytes (choose to fit MTU; for local test you can set `L = 1200`–`1400` bytes to stay under QUIC path MTU).
- For (N=8, K=6):
  - **Data symbols:** `s[0..5]`
  - **Parity symbols:** `p[0..1]` (depends on scheme)
- The last block is padded (zeros) to `K*L` bytes if needed.

### 3.2 Datagram Wire Header (per encoded symbol)
```
struct FECHeader {
  uint8  version;       // e.g., 1
  uint8  fec_scheme;    // 0=RLC, 1=RS, 2=Polar
  uint16 block_id;      // monotonically increasing per block
  uint8  N;             // = 8
  uint8  K;             // = 6
  uint8  sym_id;        // 0..N-1 (position inside the codeword)
  uint8  flags;         // e.g., systematic flag, last-block flag
  uint32 payload_len;   // L in bytes
  uint32 seed_or_idx;   // for RLC: RNG seed; for RS: column index; for Polar: info/frozen mask id (optional)
}
[header bytes][payload L bytes]
```
- **RLC:** include the RNG seed/coeff column index so the receiver can reconstruct coefficients.
- **RS:** include column index (evaluation point) so receiver knows which generator column it is.
- **Polar:** `sym_id` is the codeword position; systematic mapping implies some positions are raw data.

---

## 4) FEC Schemes (Block Encoders/Decoders)

### 4.1 RLC (Random Linear Coding, GF(256))
- **Encoding:** pick dense random coefficients \(C \in \mathbb{F}_{256}^{R	imes K}\), parity \(P = C \cdot S\), where \(S\) is `K×L` byte-matrix of data symbols. Send K systematics (optional) + R parities. Store `seed_or_idx` for coefficient reconstruction (e.g., xorshift to fill `C`).
- **Decoding:** upon receiving \(K'\ge K\) symbols, build coefficient matrix \(A \in \mathbb{F}_{256}^{K' 	imes K}\) from systematics/parities, solve \(A \cdot \hat{S} = Y\) (Gaussian elimination over GF(256)), recover \(S\).
- **Pros:** near-MDS with tiny overhead; robust to burst or random losses.  
- **Complexity:** \(O(K^3 + K^2 L)\) GF ops for decode.

### 4.2 RS (Reed–Solomon, GF(256), MDS)
- **Encoding:** evaluation over distinct field points; use **Cauchy** or **Vandermonde** generator. Parity = linear combos over GF(256).
- **Decoding:** erasure decoding: pick the \(K\) received columns, invert the \(K	imes K\) submatrix, back-solve symbols.
- **Pros:** MDS guarantee: any ≤R losses recoverable.  
- **Complexity:** similar to RLC (matrix inversion over GF(256)).

### 4.3 Packet-Level Polar (Systematic, XOR-only)
- **Construction:** load your frozen set (e.g., URS); build systematic mapping \(\hat{G}_A = G_A G_{A,A}^{-1}\).
- **Encoding:** map `K` data symbols into `N` positions (systematic at `A`), compute parity symbols by XOR (fast butterfly or precomputed parity matrix).  
- **Decoding options:**  
  - **XOR Gaussian** on the binary coefficient matrix for the received subset; solve for `K` info symbols.  
  - (Optional) SC-on-BEC knownness for quick feasibility checks.
- **Pros:** very low CPU/energy (XOR-only).  
- **Caution:** not MDS, sensitive to which `sym_id` are erased; interleaving helps if you later test with burst losses.

---

## 5) QUIC Integration (quic-go)

### 5.1 Enable DATAGRAMS (unreliable)
- **Server & Client:** configure `EnableDatagrams: true` in `quic.Config`.
- **Send:** use `conn.SendMessage([]byte)` to send a datagram (contains `[FECHeader||payload]`).
- **Receive:** loop `conn.ReceiveMessage()` to get datagrams; parse header.

### 5.2 Optional Control Stream
- Open one bidirectional stream for:
  - File metadata: filename, total bytes, block count, `L`, SHA-256.
  - End-of-transfer marker.
  - (No retransmission constraints here are irrelevant; streams are reliable by spec, but this channel is tiny.)

---

## 6) Loss Injection (0.5%, no retransmission)
- **In-process dropper** at **sender**:
  - Before `SendMessage`, draw `u~Uniform(0,1)`; if `u < 0.005`, **skip** the send (simulate loss).
  - Do **not** re-send (rateless OFF); the test is “FEC-only”.
- (Alternative for system tests: OS `tc netem` or `pfctl`—not needed for this unit test.)

---

## 7) Sender Pipeline

1. **Read file** `train_FD001.txt` → bytes.  
2. **Chunk into K-symbol blocks**:
   - Choose `L` (e.g., 1300 bytes).  
   - For block `b`, slice `K*L` bytes and pad zeros if last block is short.
3. **Encode block** with selected scheme (RLC/RS/Polar) → `N` symbols.  
4. **For i=0..N-1**:
   - Build `FECHeader{fec_scheme, block_id=b, N=8, K=6, sym_id=i, payload_len=L, seed_or_idx=...}`.  
   - Serialize header + payload → datagram `D`.  
   - **Dropper(0.5%)**: with p=0.005, skip `SendMessage(D)`; else send.
5. **(Optional)** periodically send control-stream progress (blocks sent).
6. **After last block**: send end-of-transfer marker on control stream.

---

## 8) Receiver Pipeline

1. **Init per-block state**: for each `block_id`, store:
   - `N=8, K=6, L`, scheme, received symbol list, coefficient metadata.
2. **On datagram**:
   - Parse `FECHeader`, append payload to block state, record which `sym_id` arrived.
   - If **received ≥K unique symbols** for this block → **attempt decode**:
     - **RLC/RS**: build `A` and `Y`, solve for `S` in GF(256).
     - **Polar**: binary XOR elimination on the submatrix for the received indices.
   - On success: emit `K*L` bytes to file reassembly buffer (truncate padding if final block).
3. **File reassembly**: append decoded data-symbol bytes in **original order** per block.  
4. **When all blocks complete**: write out `test_data/train_FD001.recv.txt`.  
5. **Verify** (optional): SHA-256 equals the sender’s metadata.

---

## 9) Data Structures (Go-ish, conceptual)

```go
type FECScheme int
const (
  SchemeRLC FECScheme = iota
  SchemeRS
  SchemePolar
)

type FECHeader struct {
  Version     uint8
  Scheme      FECScheme
  BlockID     uint16
  N, K        uint8
  SymID       uint8
  Flags       uint8
  PayloadLen  uint32
  SeedOrIdx   uint32 // RLC seed, RS column, Polar table idx if any
}

type BlockState struct {
  Scheme   FECScheme
  N, K, L  int
  RecvMask [8]bool
  Symbols  [][]byte        // len up to N
  Meta     []CoeffMeta     // per symbol (coeffs desc)
}
```

---

## 10) Parameter Choices & Defaults
- **Symbol size L**: 1300 bytes (safe under most QUIC PMTUs).
- **Field**: GF(256) with log/antilog tables for RLC/RS.
- **RLC coefficients**: dense random, seed per block; systematics-first (send `K` data, then `R` random parities).
- **RS generator**: Cauchy preferred (faster inversion) or Vandermonde (simple).
- **Polar**: Systematic form, reuse your existing URS/frozen set and parity matrix; precompute `Gpar` (parity rows) once for N=8,K=6.

---

## 11) Metrics & Logging (for later analysis)
- **Sender:** number of datagrams sent/dropped, throughput, time per block encode.  
- **Receiver:** per-block decode attempts, success/failure, time per decode, total file time.  
- **Totals:** overall goodput, end-to-end time, final file hash check.

---

## 12) Happy-Path Test Procedure
1. Start **server** (quic-go) listening (EnableDatagrams=true); it writes received data to `test_data/train_FD001.recv.txt`.
2. Start **client**:
   - Opens connection, streams metadata (filename, filesize, L, total blocks).
   - Chooses scheme (RLC / RS / Polar) from CLI flag.
   - Sends all blocks via DATAGRAM with **0.5% in-process dropper**.
3. Confirm receiver:
   - Reports decode success for all blocks.
   - Writes final file; verify SHA-256 matches source.
4. Repeat for each scheme; record times & logs.

**Expected (with p=0.5%)**:  
- **RS**: guaranteed decode per block (≤R=2 losses expected, most blocks see 0–1 losses).  
- **RLC**: near-100% decode (probability of rank-deficiency negligible).  
- **Polar**: should succeed with high probability at N=8,K=6 under i.i.d. 0.5% (but verify).

---

## 13) Edge Cases & Fallbacks
- **Datagram reordering:** handle out-of-order arrivals by buffering per `block_id`.
- **Duplicate datagrams:** ignore if `RecvMask[SymID]` already true.
- **Final short block:** track original file size; trim padding on write.
- **Decode failure:** (should be rare at 0.5%) log block_id & missing indices, continue transfer.

---

## 14) Directory Layout (suggested)
```
.
├── cmd/
│   ├── quicfec-server/   # quic-go receiver
│   └── quicfec-client/   # quic-go sender
├── fec/
│   ├── rlc/              # GF(256) tables, encode/decode
│   ├── rs/               # Cauchy/Vandermonde, erasure decode
│   └── polar/            # systematic matrices, XOR Gaussian
├── test_data/
│   ├── train_FD001.txt
│   └── recv_train_{fec}.txt (output)
└── internal/
    ├── header/           # FECHeader marshal/unmarshal
    └── dropper/          # Bernoulli p=0.005 dropper
```

---

## 15) Security & Robustness Notes
- Validate header fields (`N=8, K=6`, payload_len == L).  
- Bound memory per block to prevent DoS via huge block_id ranges.  
- Optional: authenticate control-stream metadata or use a session key (not required for local testing).

---

## 16) Future Extensions
- **Burst-loss tests**: add interleaver and bursty dropper (Gilbert–Elliott).  
- **Adaptive FEC**: switch schemes or R on-the-fly based on observed loss via ACK feedback.  
- **Partial reliability**: mix DATAGRAM (FEC) for bulk + STREAM for control/critical metadata.  
- **Offload**: SIMD for GF(256) mul; prefetch-friendly layouts.

---

**TL;DR:** Send your file over **QUIC DATAGRAM** with **(N,K)=(8,6)** FEC blocks, simulate **0.5%** loss by **dropping 0.5% of outgoing datagrams**, never retransmit. At the receiver, reconstruct blocks with RLC/RS/Polar decoders and reassemble the file in the same directory. This isolates **FEC effectiveness** under **unreliable transport** while keeping QUIC’s session, path, and crypto benefits.
