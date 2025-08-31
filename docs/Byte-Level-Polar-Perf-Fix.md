# Byte‑Level Polar — Performance & Reliability Fix Plan
**Scope:** Diagnose and fix (1) slow **encode**, (2) slow **decode**, (3) poor **FEC performance**, (4) timing targets (**<50 ms** for both encode & decode on `train_FD001.txt`), and (5) microsecond‑level **interleaver** cost via LUT.

This plan assumes: **systematic Polar**, **BEC**, **GE+XOR** decoding, **N=1024 bytes** per packet (8 bit‑planes × 1024 bits), **K=512 bytes**, two interleavers (**random** + **slope**), and that **`polar_table_5_3_1_2_1_inverted.txt`** (col1=index, col2=reliability value; larger=more reliable) is authoritative for building `A` (data) and `F` (frozen). Reference the **packet‑level Polar** implementation (not the old bit‑level one).

---

## 0) Quick Summary of Problems → Likely Root Causes
1) **Encode time 3–10× slower than packet‑level**  
   **Likely causes:**  
   - Per‑plane **full matrix ops** instead of **in‑place butterfly** (or butterfly not fused over 8 planes).  
   - **No bit‑slicing/SIMD**: operating byte‑wise/bit‑wise with scalar loops.  
   - **Rebuilding artifacts** per packet (A/F sets, permutations) and **heap allocs** inside hot loops.  
   - **Branchy code & cache misses** (non‑contiguous access, poor SoA/ AoS choice).

2) **Decode time ≈ 10× packet‑level**  
   **Likely causes:**  
   - Doing **GE from scratch for every packet and every plane**, despite **identical erasure pattern S**.  
   - Not **reusing factorization** (echelon / PLU) across packets **and** across 8 planes.  
   - Column order causing **fill‑in explosion**; no **early‑stop** on reaching rank `K`.

3) **Poor FEC success at rate 0.5 with 10% loss**  
   **Likely causes:**  
   - **Wrong A/F construction** from the inverted table (e.g., sorting the wrong column / wrong direction).  
   - **Byte vs bit index mismatch** (treating byte indices as Polar bit indices).  
   - **Systematic encoder** not actually systematic at positions `A` per plane.  
   - **Interleave/de‑interleave mismatch**, **slope step not coprime** to `M=N·L`, or LUT not applied symmetrically.  
   - Building `M = G_{A,S}` incorrectly (row/col swap, bit‑reversal mismatch, not zeroing frozen bits).

4) **Targets** (encode & decode **<50 ms** on `train_FD001.txt`) not met  
   **Likely causes:** mix of the above + missing O3/`-march=native`/SIMD, unnecessary copies, and I/O in timing.

5) **Interleaver should be μs‑level**  
   **Likely causes:** recomputing permutations, scattered writes, and no **LUT**/**blocked copy** strategy.

---

## 1) Fixes — Encoding (Aim: 3–10× speed‑up)
### 1.1 Use fused **in‑place butterfly** across 8 bit‑planes
- Keep data as **bit‑sliced SoA**: `plane[b][i]` where `b∈[0..7]`, `i∈[0..1023]`. Pack into 64/128/256‑bit words.
- Perform Polar transform in **10 stages** (since 1024=2¹⁰), **in place**. For each stage:
  ```c
  // pseudo: width = 1<<stage; stride = width<<1;
  for (i = 0; i < N; i += stride) {
    for (k = 0; k < width; ++k) {
      // fuse 8 planes; W is vector type (u64/u128/u256)
      W a = load(plane0[i+k]),  b = load(plane0[i+k+width]);
      store(plane0[i+k],      a ^ b);
      // repeat for plane1..plane7 with unrolled loop or vector-of-8 pointers
    }
  }
  ```
- **Do not** materialize `G_N`. **Do** precompute the **bit‑reversal schedule** (or operate in natural order if your packet‑level reference already does).

### 1.2 SIMD & memory layout
- Use **AVX2/NEON/AVX‑512** XORs on 16–64 bytes per instruction; align buffers to 32/64 B.  
- **Avoid heap allocs** and branches inside stage loops; pre‑allocate scratch once per run.  
- Compile with `-O3 -march=native -fno-exceptions -fno-rtti` (C++) or equivalent Go `-gcflags='-d=checkptr=0'` + cgo SIMD kernels.

### 1.3 Reuse cached artifacts
- Load `A` from `cache/polar_A_N1024_K{K}.bin`. **No sorting** at runtime.  
- Reuse the **same `A`** for all 8 planes and all packets of the run.

**Expected:** Encoding drops to ≈ packet‑level timing (within ±20%). If previous was 3–10× slower, we expect **3–10× speed‑up**.

---

## 2) Fixes — Decoding (Aim: ≥8× speed‑up)
### 2.1 Factorize **once per (A,S)** and reuse
- For each group (same erasure pattern `S` after de‑interleave): build `M = G_{A,S}` **once** (for one plane).  
- Compute **row‑echelon / PLU** (over GF(2)) **once** → cache `(P,L,U,pivots)`.  
- For **every packet** and **every plane**, solve by **two triangular solves** (XOR only) with different RHS `y_S`:
  ```text
  Solve: y_S = u_A · M   =>   u_A = y_S · M^{-1}
  Reuse M^{-1} or (P,L,U) across all 8 planes and all packets.
  ```
- Since **M is identical across 8 planes**, the factorization cost is amortized by 8×.

### 2.2 Column ordering & early‑stop
- Insert columns of `S` in **descending popcount** of bit indices to reduce fill‑in.  
- **Stop** GE as soon as rank==K; columns after rank K are ignored for that run.  
- Keep rows packed into 64/128‑bit words; use XOR gather on cache‑resident rows.

**Expected:** If you currently do 8×(GE)×#packets, this drops to **1×GE + many cheap solves**, yielding ≳**8–16×** wall‑time reduction.

---

## 3) Fixes — FEC Performance (Correctness)
### 3.1 Build A/F correctly from **inverted table**
- Parse **`polar_table_5_3_1_2_1_inverted.txt`** as `(index, value)`; **sort by value DESC**; take **top K (bytes)** indices → `A`.  
- **Unit test:** Print top 5 & bottom 5 indices; checksum `A`. Persist to `cache/polar_A_N1024_K512.bin`.

### 3.2 Ensure byte↔bit index mapping is correct
- The **Polar indices are bit positions** per plane in `[0..1023]`, **not byte indices**. Confirm that **byte erasures** map to **8 bit erasures per plane** at the same index.  
- Verify frozen bits are **0**; systematic encoder ensures codeword bits at `A` equal the source bits.

### 3.3 Interleaver integrity
- **Slope**: verify `gcd(s_step, M)==1` (M=N·L). Precompute **`s_inv`**. LUT both directions; fuzz 100 random windows `W`, check uniformity (±1).  
- **Random**: fixed π; ensure de‑interleaver uses **π⁻¹**; unit test `DeInt(Int(x))==x` on 1e5 random bytes.

### 3.4 Rank sanity
- For `p=10%`, after de‑interleave, check **m = |S| ≈ 0.9·N = 922**. With `K=512`, expect `rank(M)=K` with overwhelming probability.  
- Add a **rank probe** (without solving) and log `rank`, `m`, and failure-only edge cases.

**Expected:** With `R=0.5` and i.i.d. 10% loss, **success rate ≈100%**. If failures persist, they will correlate with wrong `A`, wrong `S`, or interleaver mismatch.

---

## 4) Interleaver to μs — LUT + blocked copies
- Precompute **LUTs** for both interleavers: arrays of `(dst_packet, dst_offset)` per source `(s,j)`; and inverse LUTs.  
- Perform **blocked memcopies** per `(dst_packet, contiguous run)` rather than scatter per byte.  
- Keep LUTs **contiguous & cache‑aligned**; use `memcpy`/`std::copy`/`copy` on slices; prefetch next run.  
- Batch multiple packets to amortize call overhead.

**Expected:** Interleave + de‑interleave each **≤ 10–50 μs** per L‑group on modern CPUs.

---

## 5) Measurement, Expectations, & Acceptance
### 5.1 What to measure (per run)
- `t_encode_ms`, `t_interleave_ms`, `t_decode_ms`, `t_total_ms`, `ok_rate`, `throughput_MBps`.  
- Also log: `rank(M)`, `m=|S|`, `n_packets`, `(interleaver, L)`, `(systematic, N, K)`.

### 5.2 Targets (on `train_FD001.txt`)
- **Encode** `< 50 ms` and **Decode** `< 50 ms`.  
- **Interleave** in **μs** range.  
- **p=0**: `ok_rate=100%`, **decode skipped** (0 ms fast path).  
- **p=10%**: `ok_rate ≈ 100%` at `R=0.5` with both interleavers.

### 5.3 Expected speed‑ups after fixes
- **Encode**: **3–10× faster** (match packet‑level).  
- **Decode**: **≥8–16× faster** via one‑time GE + many cheap solves.  
- **Overall**: **≥4–8×** total pipeline improvement typical.

---

## 6) Verification Protocol & Outputs
1) **Unit tests**  
   - `A/F` builder: compare sorted‑by‑value top‑K against a saved golden list; CRC the `A` file.  
   - Interleaver round‑trip: `DeInt(Int(group)) == group` bit‑exact (both methods, all `L`).  
   - Systematic property: for a random packet, check *codeword bits at A == input bits* per plane.

2) **Functional tests**  
   - `loss=0`: decode path skipped; output equals input after de‑interleave.  
   - `loss=10%`: run ≥3 seeds; report mean±std for timing & ok_rate.

3) **Profiling**  
   - Use high‑res timers around encode/GE/solve/memcpy; exclude I/O.  
   - Count XORs & rows touched in GE to confirm early‑stop behavior.

**Outputs**  
- Console summary and `results/run_summary.csv` rows.  
- `logs/rank_stats.txt`: per run (rank, m, failures).  
- `cache/` artifacts for `(A, interleaver LUTs)`.

---

## 7) Implementation Checklist
- [ ] Load `A` from **inverted table** (col2 DESC), cache per `(N,K)`.  
- [ ] Encode: fused in‑place butterfly across 8 planes; SIMD XORs; no heap in hot path.  
- [ ] Decode: **one GE per (A,S)**; reuse factorization across **packets & planes**; early‑stop at rank `K`.  
- [ ] Interleaver: LUT + blocked copies; μs‑level timing.  
- [ ] Fast path: `loss=0` + **systematic** ⇒ skip decode.  
- [ ] Instrumentation: timings, ranks, success rate; CSV logging.  
- [ ] Regressions: interleave round‑trip; systematic property; `A` CRC.

---

## 8) Common Pitfalls to Re‑check
- Mixing **byte index** with **bit index**. Polar indices are **bits per plane**.  
- Not zeroing **frozen bits**.  
- Using **bit‑reversal** in one place and **natural order** in another.  
- Slope interleaver with **gcd(s_step,M)≠1** or forgetting to compute `s_inv`.  
- Measuring with I/O included or with debug builds (no `-O3 -march=native`).

---

### Appendix: Minimal GE Reuse API (suggested)
```text
build_factorization(A, S) -> Fact { P, L, U, pivots }
solve_with_fact(Fact, y_S) -> u_A             // cheap triangular solves
```
Cache `Fact` per `(A, S)`; reuse for all packets and all 8 planes within the same loss pattern.
