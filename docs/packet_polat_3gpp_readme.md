# Design Guidance for Packet-Level Polar Code Using 3GPP Frozen Set Table

## 1. Objective
This document provides detailed instructions for building a **packet-level Polar Code encoder and decoder** by leveraging the **3GPP URS frozen set table** (`polar_table_5.3.1.2-1.txt`). The design ensures that encoding and decoding operations are consistent with binary Polar code standards, but adapted for **packet-level erasure recovery**.

---

## 2. Input Resources
- **Frozen Set Table**: e.g., `polar_table_5.3.1.2-1.txt`  
  - Format: two columns — `index`, `rank`.  
  - Each entry corresponds to a **bit-channel reliability order** defined in 3GPP TS 38.212 Table 5.3.1.2-1.  
  - `index`: subchannel position in `[0, N-1]`.  
  - `rank`: relative reliability (larger = more reliable).

- **Parameters**:
  - `N`: block length (must be power of 2, e.g., 8, 16, 32, 64, …).  
  - `K`: number of information packets (with `R = N-K` parity/frozen).  
  - `L`: payload size per packet (e.g., 1500 bytes for MTU).

---

## 3. Construction of Information and Frozen Sets

### 3.1 Extract Subset from URS
1. From the table, sort subchannels by descending `rank` (larger = more reliable).  
2. For a given `(N,K)`:
   - Select **K most reliable indices** → **Information Set A**.  
   - Remaining `R=N-K` indices → **Frozen Set Ac**.

### 3.2 Initialize Input Vector
- Create `u = [u_0, u_1, ..., u_{N-1}]`.  
- For each index:
  - If index ∈ A → assign corresponding **packet data** (length L).  
  - If index ∈ Ac → assign **frozen = all-zero packet**.

---

## 4. Encoding Procedure

### 4.1 Generator Matrix
- Define Polar generator matrix:
  \[
  G_N = F^{\otimes n}, \quad F=\begin{bmatrix}1&0\\1&1\end{bmatrix}, \quad N=2^n.
  \]
- For packet-level operation, treat each `u_i` as a **binary vector of length L** (bitwise XOR is applied component-wise).

### 4.2 Encoding Rule
- Compute:
  \[
  x = u \cdot G_N
  \]
- Implementation detail:
  - Replace binary add with **bitwise XOR** across packets.  
  - Use **butterfly structure** (FFT-like) for complexity `O(N log N)`.  
  - Output `x = [x_0,...,x_{N-1}]`, each `x_i` is a length-L packet.

---

## 5. Transmission
- Transmit packets `[x_0,...,x_{N-1}]` sequentially.  
- Assume **erasure channel**: each packet may be lost with probability `ε`.

---

## 6. Decoding Procedure

### 6.1 Input
- Received subset of packets `{x_i}` with some erased.  
- Frozen set indices known (`u_j=0` for `j ∈ Ac`).

### 6.2 Decoding Approaches

#### Option A: SC (Successive Cancellation)
- Traverse decoding tree with known/erased flags.  
- On BEC: decision rules simplify to **XOR logic**:  
  - `u^-` known if both inputs known.  
  - `u^+` known if right input known, or left+`u^-` known.

#### Option B: Gaussian Elimination (Packet-Level)
- Build **measurement matrix** from received indices.  
- Augment with received data.  
- Apply elimination (XOR-only) to solve for unknown `u_A`.  
- Complexity: `O(K^3)` for matrix elimination + `O(K^2·L)` for payload back-substitution.

---

## 7. Verification & Testing

### 7.1 Consistency Check
- After decoding, verify recovered packets `u_A` against original.  
- Ensure frozen packets remain zero.

### 7.2 Performance Evaluation
- Simulate with `(N,K)` = { (8,7), (16,11), (32,26), ... }.  
- For each, measure:
  - **Decoding success rate** vs erasure count.  
  - **Computation time** (encoding and decoding).  
  - **XOR operation counts**.

### 7.3 Expected Behavior
- Systematic performance aligns with binary Polar codes.  
- Larger `N` improves polarization effect.  
- Recovery ability depends on chosen URS frozen set consistency.

---

## 8. Notes and Extensions
- **Systematic Encoding**:  
  Can apply re-encoding using `G_{A,A}^{-1}` to directly embed information packets in codeword positions.  
- **Adaptive Design**:  
  Use different `(N,K)` depending on target redundancy.  
- **Practical Optimization**:  
  Precompute generator matrices for N ∈ {8,16,32,64,128} to accelerate encoding.

---

## 9. Deliverables
- Implementation code for:
  - Table parser (load `polar_table_5.3.1.2-1.txt`).  
  - Frozen set and information set construction.  
  - Packet-level encoder (XOR-based).  
  - Decoder (SC and Gaussian elimination modes).  
- Performance evaluation scripts.  
- Documentation of test results.

---

**Version**: v1.0 – Packet-Level Polar Code Design with 3GPP Frozen Set
