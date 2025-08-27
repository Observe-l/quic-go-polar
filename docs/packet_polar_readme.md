# README — Packet-Level Polar Code (Go)
> Packet-level FEC implementation using **Polar Codes over GF(2)** (pure XOR), targeting short blocklengths (e.g., N=32) and low-power IoT/embedded devices. This design avoids physical-layer SC/LLR decoding and instead treats Polar codes as structured linear block codes. Encoding and decoding use **only XOR operations**, consistent with theoretical complexity and empirical findings [36][37].

---

## 0. Objectives and Scope

**Objectives**
1. Implement packet-level polar code encoding/decoding in **Go**, using GF(2) operations only.
2. Complexity must match theory: encoding O(KR·L) XOR; decoding O(K^3) XOR (matrix elimination) + O(K^2L) XOR (apply back to packet data).
3. Performance verification under random and burst erasures, showing recoverability close to analysis, and end-to-end latency advantage vs RS/RLC.

**Out of scope**: physical-layer polar decoding (SC/SCL/LLR); GF(256) RS/RLC implementations.

---

## 1. Terms and Symbols

- K = number of source packets; R = redundancy packets; N = K+R (power of two).
- L = bytes per packet (e.g., MTU ≈ 1200B).
- ε = packet loss rate (modeled as BEC).
- F = [[1,0],[1,1]]; G_N = F^{⊗n} (Kronecker power).
- A = set of K best subchannels chosen by Bhattacharyya recursion.
- Systematic polar generator: 
  G_sys = [ I_K | (G_AA G_AA^c)^T ].
- Encoding uses parity submatrix G_par.

---

## 2. Requirements

### Functional
- **Constructor**: Given (N,K,ε), compute Bhattacharyya parameters, choose A, build G_par.
- **Encode**: Pure XOR; input K packets ([]byte padded to L); output R parity packets.
- **Decode**: Input m≥K received packets with indices; build submatrix Ĝ; if rank==K, solve by GF(2) Gaussian elimination (XOR only).

### Complexity
- **Encoding XOR count**: ≤ K·R·L.
- **Decoding XOR count**:
  - Elimination: O(K^3)
  - Back substitution to data: O(K^2·L)
- No GF(256) multiplications.

### Correctness
- Verify that under PLR ε, empirical success rate matches BEC-polar analysis.
- Demonstrate non-MDS behavior: not all ≤R erasure patterns recoverable.

---

## 3. Mathematical Principles

### Bhattacharyya Recursion (BEC)
Z(W_1^1)=ε; for N=2^n:
- Z(W_{2N}^{2i-1}) = 2Z(W_N^i) - (Z(W_N^i))^2
- Z(W_{2N}^{2i})   = (Z(W_N^i))^2

Choose the K lowest Z_i indices as information set A.

### Generator and Systematic Encoding
- Build G_N=F^{⊗n}, extract submatrices G_AA, G_AA^c.
- Form G_par = [G_AA G_AA^c]^T.
- Encode parity: Y_par = G_par · X (GF(2) XOR).

### Decoding
- Construct Ĝ from received packets.
- If rank==K, perform GF(2) elimination, apply same row ops to data arrays.

---

## 4. Go Implementation Outline

### API

```go
type Params struct {
    N, K   int
    Epsilon float64
    AIdx   []int
    Gpar   [][]uint64 // bitset rows
    MaxLen int
}

type Packet struct {
    Index int
    Data  []byte
}

func NewParams(N,K int, eps float64, maxLen int) (*Params, error)
func Encode(p *Params, src [][]byte) [][]byte
func Decode(p *Params, recv []Packet) ([][]byte, bool)
```

### Key Points
- Store G_par rows as bitsets (uint64 slices), for fast XOR masking.
- Encode: parallel goroutines per parity row, XOR data of selected source packets.
- Decode: GF(2) elimination with synchronized XOR on packet data.

---

## 5. Verification

### Complexity Counters
- Count XORs during encoding/decoding; verify bounds:
  - enc_xor ≤ K·R·L
  - dec_xor ≈ O(K^3) + O(K^2L)
- Ensure gf256_mul_count == 0.

### Packet Loss Simulation
- Random loss: ε in {0.01,…0.05}, N=32, various K,R; simulate 1000 trials.
- Burst loss: Gilbert–Elliott model; compare success rates.
- Expected: Polar success rate < MDS, > random binary code at same complexity.

### Latency/Energy (optional)
- Benchmark encoding/decoding latency on ARM IoT board vs RS/RLC baseline.
- Expected: Polar shows 3.5–5× latency advantage, consistent with [37].

---

## 6. Example Pseudocode

### Encode
```go
func Encode(p *Params, X [][]byte) [][]byte {
    Y := make([][]byte, p.N-p.K)
    for r := 0; r < p.N-p.K; r++ {
        out := make([]byte, p.MaxLen)
        row := p.Gpar[r]
        for i := 0; i < p.K; i++ {
            if bitIsSet(row, i) {
                xorBytes(out, X[i])
            }
        }
        Y[r] = out
    }
    return Y
}
```

### Decode
```go
func Decode(p *Params, recv []Packet) ([][]byte, bool) {
    Gm, rows := buildSubmatrix(p, recv)
    piv, ok := findInvertible(Gm, p.K)
    if !ok { return nil, false }
    gaussianEliminateInPlace(Gm, piv, func(dst, src int){
        xorBytes(recv[dst].Data, recv[src].Data)
    })
    return extractSources(recv, piv), true
}
```

---

## 7. Complexity Recap

| Stage | Operations | Count | Order |
|-------|------------|-------|-------|
| Encode | XOR | ≤K·R·L | O(KRL) |
| Decode-elimination | XOR | ~⅓K^3 | O(K^3) |
| Decode-backsub | XOR | K^2·L | O(K^2L) |

No GF(256) multiplication.

---

## 8. References
- [36] *Polar-QUIC: Adaptive Packet-Level Polar Coding-Assisted QUIC in Satellite Networks* — Introduces systematic packet-level polar codes, N=32 short codes, encoding O(K(N-K)), GF(2) elimination decoding.  
- [37] *Polar Coding for Efficient Transport Layer Multicast* — Analyzes short blocklength polar codes for multicast, highlights XOR-only implementation and latency/energy benefits vs RS/RLC.  

