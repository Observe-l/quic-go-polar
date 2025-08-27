# README — Packet-Level Random Linear Coding (RLC) (Go)
> Packet-level FEC implementation using **Random Linear Codes (RLC)**, a probabilistic approach. Each redundancy packet is a random linear combination of the K source packets. Decoding succeeds with high probability if enough linearly independent packets are received. Encoding/decoding requires GF multiplications; reliability depends on field size and randomness.

---

## 0. Objectives and Scope

**Objectives**
1. Implement packet-level RLC in Go over GF(2^8) (standard choice).  
   Optionally compare GF(2) binary RLC for XOR-only operation.  
2. Complexity:  
   - Encoding O(KR·L) operations (multiplications+XOR).  
   - Decoding O(K^3) for Gaussian elimination + O(K^2L) to apply back.  
3. Verify probabilistic recovery: success rate grows with number of received packets.

---

## 1. Terms and Symbols

- K = number of source packets.  
- R = number of redundancy packets.  
- N = K+R.  
- L = packet length in bytes.  
- Field = GF(2^8) (standard, good reliability); GF(2) (cheap XOR-only, lower reliability).  
- Coefficient vectors: random, stored as packet headers.  

---

## 2. Requirements

### Functional
- **Encode**: For each redundancy packet, draw random coefficients a_i ∈ GF(q); compute y = Σ a_i·x_i.  
- **Decode**: Collect m ≥ K packets (some original, some parity); build m×K coefficient matrix; perform GF(q) Gaussian elimination.  

### Complexity
- Encoding: R·K multiplications + XOR per byte (GF(2^8)), or only XOR in GF(2).  
- Decoding:  
  - Elimination: O(K^3).  
  - Apply solution to L-byte data: O(K^2L).  
- Reliability depends on field size:  
  - GF(2): higher probability of rank deficiency.  
  - GF(2^8): near-MDS with very high probability.

---

## 3. Mathematical Principles

- Redundancy packets are linear combinations:  
  y_j = Σ a_{j,i} · x_i, with a_{j,i} drawn uniformly at random.  
- Decoding is solving linear system A·X = Y, where A is random m×K matrix.  
- Success probability = Pr(rank(A)=K).  

---

## 4. Go Implementation Outline

### API
```go
func EncodeRLC(src [][]byte, K, R int, field string) [][]byte
func DecodeRLC(recv []Packet, K int, field string) ([][]byte, bool)
```

### Key Points
- Encode: generate random coefficient vector, apply GF multiply+XOR to source data.  
- Header: include coefficient vector for decoder.  
- Decode: build coefficient matrix, run elimination over chosen GF.  

---

## 5. Verification

- **Complexity check**: measure multiplication/XOR counts.  
- **Success probability**: simulate random erasures, estimate decoding success probability vs m.  
- **Field size comparison**: GF(2) vs GF(2^8) trade-off: efficiency vs reliability.  

---

## 6. Complexity Recap

| Stage | Operations | Count | Order |
|-------|------------|-------|-------|
| Encode (GF(2^8)) | GF mult + XOR | R·K·L | O(KRL) |
| Encode (GF(2)) | XOR | ~½R·K·L | O(KRL) |
| Decode (elimination) | GF mult or XOR | ~⅓K^3 | O(K^3) |
| Decode (apply) | GF mult + XOR | K^2·L | O(K^2L) |

---

## 7. References
- Ho et al., "Random Linear Network Coding".
- Luby, "LT Codes and Fountain Codes".
