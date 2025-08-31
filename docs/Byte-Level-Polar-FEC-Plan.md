# Byte-Level Polar FEC with Cross-Packet Interleaving
**Requirements · Test Plan · Expected Results · Outputs · Checklist**

> Goal: Implement and evaluate a **byte-level polar code** per packet (non-systematic), with two cross-packet interleavers (random & slope), and **BEC-optimal decoding via Gaussian Elimination (GE) + XOR**. Validate with `test_data/train_FD001.txt` at `K=512` bytes, packet length `N=1024` bytes, first with `p=0%` loss (sanity) then `p=10%` (FEC performance). Cache all heavy, repeatedly used indices (frozen set, interleaver maps) to avoid recomputation. Supported interleaver fan-out `L ∈ {8,16,32,64,128}`.

---

## 1) Design Overview

- **Storage unit**: bytes. **Polar operation unit**: **bit-planes**.  
  Treat each packet’s 1024 **bytes** as 8 parallel **binary** polar codewords of length **1024 bits** (one per bit-plane b∈{0..7}). This keeps strict binary polar math while using byte-packed memory and SIMD.
- **Per-packet code parameters**:  
  - Packet length: **N = 1024 bytes** (= 1024 bit positions per bit-plane).  
  - External control: **K_bytes** information **bytes** per packet (default **K=512**).  
  - Effective per-plane info bits = **K_bytes**; total per packet info bits = **8·K_bytes**.
- **Frozen/data set (per bit-plane)**: use **3GPP `polar_table_5_3_1_2_1.txt`** for **N=1024** (descending reliability). For each plane, pick the top **K_bytes** indices as **data** set `A` and the rest as frozen (0). Reuse the same index list across all 8 bit-planes.
- **Encoding (per packet)**: for each bit-plane, build `u_A` (data) and `u_F` (frozen=0), then compute `x = u·G_N` with the standard polar generator `G_N = B_N · F^{⊗ n}` (n=10). Use in-place butterfly (no materialized `G_N`). Pack 8 planes back into bytes to get an **encoded packet** of 1024 bytes (non-systematic).
- **Cross-packet interleaving**: process packets in groups of size **L**.
  - **Method 1 – Random interleaver (fixed permutation)**: byte-wise, reproducible, cached.
  - **Method 2 – Slope interleaver** (deterministic “uniform W-window” guarantee).
- **Channel**: **BEC**. Erasures apply at **packet level** (whole packets missing) in required tests (optional: byte-level erase).
- **Decoding**: **GE + XOR** per bit-plane on the surviving byte indices (BEC ML decoding). Early-stop when rank reaches `K_bytes` per plane; re-pack 8 planes to bytes.

---

## 2) Exact Algorithms & Cached Indices

### 2.1 Frozen/Data set (per plane)

- **Input**: `polar_table_5_3_1_2_1.txt` with two columns:  
  col1 = reliability rank (larger = more reliable), col2 = bit-channel index in `[0..1023]`.
- **Build**:
  1) Sort by col1 **descending**.  
  2) Take the first `K_bytes` indices → **data set `A`**; the rest → **frozen set `F`**.
- **Cache**: store `A` as `cache/polar_A_N1024_K{K}.bin` (little-endian int32 array, length = `K_bytes`).  
  Reuse the same `A` across **all 8 bit-planes**.

---

### 2.2 Interleaver – Method 1 (Random, fixed & cached)

- **Idea**: Permute each source packet’s **byte indices** with a fixed random permutation `π` over `[0..N-1]`, then distribute bytes from **each source packet** across **all L output packets** in equal slices, using a deterministic row/segment layout.
- **Precompute & Cache**:
  - Choose a fixed seed (e.g., `seed=0xC0FFEE`); generate `π` (a permutation of `[0..1023]`).  
  - Save `cache/perm_random_N1024_seed{seed}.bin` (int32, length `N`).
- **Mapping (group of L encoded packets `C[s][u]`, `s∈[0..L-1]`, `u∈[0..N-1]`)**:
  ```
  let S = N / L                           // bytes per segment (integer since L | N)
  for s in 0..L-1:        // source packet id
    for j in 0..N-1:      // source byte index
      u  = π[j]                          // permuted source index
      l  = j mod L                       // target (output) packet id
      r  = s*S + floor(j / L)            // target byte position within output packet
      Out[l][r] = C[s][u]
  ```
- **De-interleaver (inverse)**:
  ```
  // Precompute π^{-1}
  for l in 0..L-1:
    for r in 0..N-1:
      s  = floor(r / S)
      t  = r % S
      j  = t*L + l
      u  = π[j]            // same π
      C[s][u] = In[l][r]
  ```

---

### 2.3 Interleaver – Method 2 (Slope, uniform-W guarantee)

- **Idea**: Deterministic “diagonal” interleaver over the **entire group surface** to **uniformly** spread any **consecutive physical window of length W** over the L original packets (difference ≤1).
- **Parameters**:
  - `M = N * L` (total bytes across the group).  
  - Choose **step** `s_step` such that `gcd(s_step, M)=1` (recommend `s_step = M-1`), and **offset** `o` (e.g., `o = frame_id`).
- **Mapping** (from sources to outputs):
  ```
  // linearize (s, j) into q, then permute to t, then slice into (l, r)
  for s in 0..L-1:
    for j in 0..N-1:
      q = s + L*j                      // q in [0..M-1]
      t = (o + s_step * q) mod M       // permutation over [0..M-1]
      l = floor(t / N)                 // target packet id
      r = t % N                        // target byte position
      Out[l][r] = C[s][j]
  ```
- **De-interleaver**:
  ```
  // precompute multiplicative inverse s_inv of s_step mod M
  for l in 0..L-1:
    for r in 0..N-1:
      t = l*N + r
      q = (s_inv * (t - o)) mod M
      s = q % L
      j = floor(q / L)
      C[s][j] = In[l][r]
  ```
- **Uniformity property**: For **any** consecutive physical window `W` over the serialized transmit order of the group, each original packet contributes either `⌊W/L⌋` or `⌈W/L⌋` bytes (difference ≤ 1).
- **Cache**: store chosen `(s_step, o)` per `L` in `cache/perm_slope_L{L}.json`.

---

## 3) Encoding & Decoding Details

### 3.1 Packet Encoding (non-systematic, per packet)

1) **Bit-slice** the 1024 input bytes into 8 arrays of 1024 bits (planes `b=0..7`).  
2) For each plane `b`:
   - Build `u` of length 1024: fill indices `A` with the `K_bytes` info bits of that plane, set others to 0 (frozen).
   - Compute `x = u · G_N` over GF(2). Use an **in-place butterfly**.  
3) **Pack** 8 planes back to 1024 **bytes** → encoded packet.

> Complexity: ~`8·N·log2(N)` XORs per packet; bit-slicing is SIMD-friendly.

### 3.2 Decoding on BEC (per packet)

- After **de-interleaving**, each packet yields a set `S ⊆ {0..N-1}` of **known byte positions** (received). The unknown positions are erasures.
- For each bit-plane `b`:
  - Let `m = |S|`. Build the `K_bytes × m` sub-matrix `M = G_{A,S}` **implicitly** (online column generation) and form `y_S`.
  - Solve `y_S = u_A · M` over GF(2) with **incremental Gaussian Elimination + XOR**; **early-stop** at rank `K_bytes`, then back-substitute to recover `u_A`.
  - Reassemble `u` (fill frozen with 0), pack 8 planes → bytes. (Optional: re-encode to verify known positions.)

**Fail condition**: if final rank `< K_bytes` → decoding failure for that packet.

---

## 4) Caching & File Layout

- `cache/polar_A_N1024_K{K}.bin` — int32 array of length `K_bytes`, **data set indices** (desc. reliability).  
- `cache/perm_random_N1024_seed{seed}.bin` — int32 array of length `N`, **random permutation π**.  
- `cache/perm_slope_L{L}.json` — `{ "M": N*L, "s_step": ..., "s_inv": ..., "o_policy": "frame_id|fixed|random" }`.  
- Optional: inverse maps for faster de-interleave.

All caches are **versioned by (N, K, L, seed)** to avoid cross-test pollution.

---

## 5) Test Plan

### 5.1 Dataset & Pre-processing
- Input file: **`test_data/train_FD001.txt`**.  
- Chunk the file into **packets of 1024 bytes** (pad last packet with zeros if needed).  
- Group packets in batches of **L** for interleaving.

### 5.2 Configurations to run

- Fixed: `N=1024`, `K=512` bytes, **non-systematic polar**, **BEC**, **GE+XOR** decoder.  
- Interleaver: **Random** and **Slope** (both).  
- `L ∈ {8, 32, 128}` (at least three points; others optional from {8,16,32,64,128}).  
- Loss models:
  1) **Sanity**: `p = 0%` packet-erasure (no loss).  
  2) **FEC**: `p = 10%` packet-erasure, i.i.d. across output packets (default).  
     - (Optional) Byte-erasure mode at `p=10%` for additional stress.

For each (interleaver, L, p), run **≥ 3 trials** with different random loss seeds (interleaver seed fixed), report mean ± std.

### 5.3 Metrics to collect (per configuration)

- **Encode time** (ms): pure polar transforms (8 planes).  
- **Interleave + De-interleave time** (ms).  
- **Decode time** (ms): GE+XOR.  
- **Total time** (ms) = sum of the above.  
- **Success rate** (%): fraction of packets decoded (rank = K_bytes).  
- **Residual byte error** (should be 0 on BEC if decoded).  
- **Throughput** (MB/s): file_size / total_time.

Log CPU model, core count, SIMD avail, and build flags.

---

## 6) Expected Results

- **p=0%**:  
  - **Success rate = 100%**, residual error = 0.  
  - Encode/Decode times establish baseline (GE fast due to immediate full rank).
- **p=10% (packet-erasure)** with `R = K/N = 0.5`:  
  - With either interleaver, **success rate ≈ 100%** on typical test sizes (rank deficiency rare).  
  - **Random vs. Slope**: similar on i.i.d. loss; under **burst loss** (not required here), **Slope** should dominate (uniform-W guarantee).

Performance intuition (non-binding):  
- Encoder ≈ `8·N·log2(N)` XORs per packet.  
- Decoder ≈ `~(K_bytes^2 · m / w)` XORs per plane with early-stop (w=word width).  
- Interleaver overhead negligible relative to decoding.

---

## 7) Outputs & Reporting

Produce the following artifacts per run:

- **Console/Log summary** (human-readable):
  ```
  Config: N=1024, K=512, L=32, Interleaver=Slope, Loss=10% packets
  Packets: sent=XXXX, received=~0.9*sent, decoded_ok=YYYY (ZZZ%)
  Encode_time_ms=..., Interleave_time_ms=..., Decode_time_ms=..., Total_time_ms=...
  Throughput_MBps=...
  ```
- **CSV report** `results/run_summary.csv` (append rows):
  ```
  date, seed_loss, interleaver, L, p_loss, N, K, packets, ok, ok_rate,
  t_encode_ms, t_interleave_ms, t_decode_ms, t_total_ms, throughput_MBps
  ```
- **Cache dump**: ensure all generated caches are saved under `cache/` as specified.
- **Failure log** (if any): `logs/failed_packets.txt` with packet ids where `rank<K_bytes`.

---

## 8) Developer Checklist

**Frozen/Data set**
- [ ] Parse `polar_table_5_3_1_2_1.txt` correctly (desc reliability).  
- [ ] Build `A` for `N=1024`, `K_bytes=512`; cache to `cache/polar_A_N1024_K512.bin`.  
- [ ] Reuse the same `A` across all 8 bit-planes.

**Encoder**
- [ ] Bit-slice 1024 bytes → 8×1024 bits.  
- [ ] Polar transform per plane via in-place butterfly.  
- [ ] Pack planes → 1024 encoded bytes (non-systematic).

**Interleavers**
- [ ] **Random**: generate & cache permutation `π` (fixed seed); implement mapping & inverse.  
- [ ] **Slope**: choose `s_step` (coprime to `M=N·L`), compute `s_inv`, decide `o` policy; cache JSON; implement mapping & inverse.  
- [ ] Unit-test that de-interleaver( interleaver(packet_group) ) == packet_group (bit-exact).

**Channel model**
- [ ] Implement **packet-erasure** at rate `p` with RNG seed; (optional) byte-erasure mode.

**Decoder (BEC, GE+XOR)**
- [ ] For each plane, build `M = G_{A,S}` **implicitly** via column generation.  
- [ ] Incremental GE with early-stop (rank→`K_bytes`), SIMD XOR rows.  
- [ ] Repack planes to bytes; (optional) verify by re-encoding and comparing known positions.

**Timing & Reporting**
- [ ] Measure: encode, (de)interleave, decode, total.  
- [ ] Aggregate: success rate, residual errors, throughput.  
- [ ] Emit console summary + append to `results/run_summary.csv`.  
- [ ] Save caches under `cache/`; write failures to `logs/failed_packets.txt`.

**Experiments**
- [ ] Run `p=0%` for both interleavers and `L∈{8,32,128}`; expect 100% success.  
- [ ] Run `p=10%` for both interleavers and `L∈{8,32,128}`; expect ≈100% success.  
- [ ] Record mean±std over ≥3 seeds for the loss process.

**Determinism & Reproducibility**
- [ ] Fix interleaver seed (random method).  
- [ ] Log `s_step, s_inv, o` (slope method).  
- [ ] Log CPU, compiler flags, SIMD features.

---

## 9) Notes & Practical Tips

- **Why 8 parallel planes?** Exact binary polar math with byte-packed SIMD. The 3GPP reliability table (N=1024) applies per plane directly.  
- **Non-systematic** is simpler and fine on BEC with GE (we don’t need data bits to appear directly in the codeword).  
- **When to prefer Slope**: For burst losses (long consecutive packet drops), slope provides a provable near-uniform spread across originals; random is excellent for i.i.d. but lacks worst-case bounds.  
- **Early-stop**: As soon as rank hits `K_bytes`, back-solve—don’t wait for all columns.  
- **Memory**: Pack GE rows into 64-/128-bit words; maintain pivot indices and a bitset for active rows.
