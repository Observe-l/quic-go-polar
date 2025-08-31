# Design Document for IEEE Journal Paper – Methods Section

This document structures the **Methods** section of the IEEE-style paper on **Packet-Level Polar Codes**. It follows the conventions of IEEE journals: formal, concise, technical, and mathematically rigorous. The goal is to help you draft the final LaTeX text.

---

## 1. Introduction to Polar Codes (Background & Principle)
- **Core idea**: Polar codes are the first family of channel codes provably achieving channel capacity for symmetric binary-input discrete memoryless channels (B-DMC) (Arıkan, 2009).
- **Channel polarization**: By recursively applying the **polar transform** based on Kronecker products, channels evolve into either “good” (reliable) or “bad” (unreliable).  
- **Mathematical formulation**:  
  - Define kernel matrix
    \[
    F = \begin{bmatrix} 1 & 0 \\ 1 & 1 \end{bmatrix},
    \]
    then construct
    \[
    G_N = F^{\otimes n}, \quad N=2^n,
    \]
    where \( \otimes \) denotes the Kronecker power.
  - Encoding rule:
    \[
    x = u G_N, \quad u = (u_0, u_1, \ldots, u_{N-1}).
    \]
- **Frozen set**: Indices of unreliable channels are set to frozen bits (usually zero). The remaining indices carry information.

**Citations**:  
- E. Arıkan, “Channel polarization: A method for constructing capacity-achieving codes for symmetric binary-input memoryless channels,” *IEEE Trans. Inf. Theory*, vol. 55, no. 7, pp. 3051–3073, Jul. 2009.  
- 3GPP TS 38.212 (Polar codes in 5G).

---

## 2. Systematic Polar Codes (Packet-Level Construction)
- **Motivation**: Systematic encoding ensures the information bits appear directly in the codeword, which simplifies application at packet level.
- **Systematic construction**:  
  - Partition index set \(\mathcal{A}\) for information and \(\mathcal{A}^c\) for frozen bits.  
  - Extract submatrix \(G_{\mathcal{A}, \mathcal{A}}\).  
  - Systematic mapping:
    \[
    \hat{G}_{\mathcal{A}} = G_{\mathcal{A}} G_{\mathcal{A}, \mathcal{A}}^{-1}.
    \]
  - Codeword:
    \[
    x = u_{\mathcal{A}} \hat{G}_{\mathcal{A}} \oplus u_{\mathcal{A}^c} G_{\mathcal{A}^c}.
    \]
- **Packet-level adaptation**: Treat each packet as a “symbol” mapped into the polar structure. Frozen set can be chosen from 3GPP URS or Bhattacharyya parameter construction.

---

## 3. XOR-Based Encoding (Equivalence Proof)
- **Encoding equivalence**: Over GF(2), matrix-vector multiplication reduces to XOR operations.  
- **Example**: For any row \(g_i\) of \(G_N\),
  \[
  x_i = \bigoplus_{j=0}^{N-1} u_j g_{i,j},
  \]
  where \(\oplus\) is XOR (addition modulo 2).  
- **Claim**: Performing XOR-only butterfly operations is **mathematically equivalent** to the standard polar encoding rule \(x = u G_N\).  
- **Proof sketch**:  
  - Kronecker construction ensures that each stage of encoding involves duplicating and XORing bit values.  
  - Hence, the butterfly network of XORs exactly implements \(G_N\).  
- **Benefit**: Implementation requires only bitwise XOR, avoiding GF(256) multiplications required in RLC/RS.

---

## 4. Decoder Design (Gaussian Elimination & XOR)
### 4.1 Gaussian Elimination Decoder
- Build measurement matrix \(A\) from received packets.  
- Perform Gaussian elimination over GF(2).  
- Solve for the information set:
  \[
  A u_{\mathcal{A}} = y, \quad u_{\mathcal{A}} = A^{-1} y.
  \]
- Complexity: \(O(K^3)\) bit operations.

### 4.2 XOR-Only Decoder
- Since all operations are over GF(2), elimination reduces to **row swaps, row XORs**.  
- No multiplications or divisions are needed.  
- This makes decoding efficient on hardware such as ARM-based IoT devices.

---

## 5. Complexity Analysis & Comparison
### 5.1 Polar Code (GF(2), XOR-only)
- **Encoding**: \(O(N \log N)\) XOR operations (butterfly network).  
- **Decoding**: \(O(K^3)\) XOR operations for Gaussian elimination.  
- **Implementation**: Efficient on CPUs lacking fast multiplication instructions.

### 5.2 Reed–Solomon (GF(256))
- **Encoding**: \(O(NK)\) multiplications + additions in GF(256).  
- **Decoding**: \(O(K^3)\) GF(256) multiplications for matrix inversion.  
- **Cost**: Multiplication in GF(256) is significantly more expensive than XOR.

### 5.3 Random Linear Coding (RLC)
- **Encoding**: Random coefficients → \(O(NK)\) multiplications in GF(256).  
- **Decoding**: Solve dense \(K \times K\) linear system, \(O(K^3)\) GF(256) multiplications.  
- **Drawback**: High computational load, poor fit for IoT/embedded devices.

### 5.4 Complexity Highlight
- XOR ≈ single-cycle on ARM/Intel CPUs.  
- GF(256) multiplication = ~10–30 cycles (via log/antilog tables).  
- Thus, **Polar code significantly reduces computation and energy consumption**.

---

## 6. Section Summary (for Paper)
- Polar codes achieve capacity with low-complexity XOR-only encoding.  
- Systematic polar code construction allows practical use at packet level.  
- Decoding with Gaussian elimination and XOR operations is mathematically correct and efficient.  
- Compared to RS and RLC, packet-level polar codes achieve **lower complexity and better suitability for constrained devices**.  

---

## Notes for LaTeX Writing
- Use `\section{}` for main sections, `\subsection{}` for Gaussian elimination vs XOR decoder.  
- Equations should be in `equation` or `align` environments.  
- Citations: Arıkan (2009), 3GPP TS 38.212, possibly Tal & Vardy (2011) for systematic encoding.  
- Highlight comparisons in a **table**: encoding/decoding complexity vs operations (XOR vs GF(256)).

