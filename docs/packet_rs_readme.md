# README — Packet-Level Reed–Solomon (RS) Code (Go)
> Packet-level FEC implementation using **Reed–Solomon (RS) codes over GF(2^8)**, widely adopted in storage and networking for their **MDS property** (any K out of N packets can recover the original data). Encoding and decoding require **GF(256) multiplications and additions**. Compared with XOR-only polar codes, RS provides deterministic recovery at the cost of higher computation and energy.

---

## 0. Objectives and Scope

**Objectives**
1. Implement packet-level RS encoding/decoding in Go, using GF(2^8) arithmetic.
2. Complexity consistent with MDS theory:  
   - Encoding O(KR·L) multiplications + additions.  
   - Decoding O(K^3) multiplications for matrix inversion/elimination + O(K^2L) multiplications for applying to packet data.
3. Verify perfect recovery when ≤R packets lost.

**Scope**
- Focus on systematic RS (original packets appear unchanged, parity added).
- Use lookup-table (log/antilog) for GF(256) multiplications.

---

## 1. Terms and Symbols

- K = number of source packets.  
- R = number of redundancy packets.  
- N = K+R.  
- L = packet length in bytes.  
- Field = GF(2^8), primitive polynomial selected.  
- Generator matrix: Vandermonde or Cauchy form.  

---

## 2. Requirements

### Functional
- **Encode**: Input K packets, output R parity packets, each parity is polynomial evaluation.  
- **Decode**: Input any m ≥ K packets; build Vandermonde submatrix; invert to recover missing sources.  

### Complexity
- Encoding per byte: R·K multiplications, R·(K-1) additions.  
- Decoding:  
  - Matrix inversion: O(K^3) multiplications.  
  - Apply inverse to L bytes: O(K^2L) multiplications.  
- Addition in GF(256) = XOR; multiplication requires lookup table or shift-and-reduce.

### Correctness
- RS is MDS: any K received packets guarantee successful recovery.

---

## 3. Mathematical Principles

- **Encoding**:  
  Treat K packets as coefficients of a polynomial over GF(256). Evaluate at N points to produce N packets.  
- **Systematic form**: transmit original K packets plus R parity packets (linear combinations).  
- **Decoding**: given any K packets, solve linear system defined by Vandermonde submatrix.

---

## 4. Go Implementation Outline

### API
```go
func EncodeRS(src [][]byte, K, R int) [][]byte
func DecodeRS(recv []Packet, K, R int) ([][]byte, bool)
```

### Key Points
- Use lookup tables for GF(256) multiply/divide.  
- Encode: for each parity, iterate over K sources, accumulate with GF(256) multiply+XOR.  
- Decode: construct Vandermonde matrix, invert with GF(256) elimination, apply to data arrays.

---

## 5. Verification

- **Complexity check**: measure number of multiplications vs XOR.  
- **Erasure tests**: randomly drop up to R packets, always recover.  
- **Latency**: measure on ARM IoT vs x86 to highlight cost of multiplications.  

---

## 6. Complexity Recap

| Stage | Operations | Count | Order |
|-------|------------|-------|-------|
| Encode | GF mult + XOR | R·K·L | O(KRL) |
| Decode (matrix inversion) | GF mult | ~⅓K^3 | O(K^3) |
| Decode (apply) | GF mult + XOR | K^2·L | O(K^2L) |

---

## 7. References
- Standard Reed–Solomon coding theory (Wicker, Lin & Costello).
- Practical GF(256) optimizations (Intel ISA-L, open-source RS libraries).
