# Byte‑Level Polar — Decode Performance Action Plan
**Goal:** Deliver a byte‑level **systematic** Polar FEC whose **decode time** and **reliability** match packet‑level baselines while providing **microsecond‑level interleaver** overhead. This plan focuses on (1) fixing decode scaling with loss, (2) correctness of A/F and index orders, (3) reuse of Gaussian elimination (GE), and (4) **reporting interleave & de‑interleave timings** while testing **both** interleavers (random & slope).

---

## 1) Targets (Clear & Measurable)
- **Correctness**
  - With `R = K/N = 0.5` and **i.i.d. packet loss** `p = 10%`, **ok_rate =100.0%** over ≥1000 runs of `train_FD001.txt` (should be 100%).  
  - With `p = 0`, **skip decoding** (systematic fast path) and output equals input (after de‑interleave).
- **Latency (on `train_FD001.txt`)**
  - **Encode** `< 50 ms`.
  - **Decode** `< 50 ms` (includes GE factorization + solves; excludes I/O).
  - **Interleave** and **De‑interleave**: **each** ≤ **50 µs** per L‑group using LUT + blocked copies.
- **Scaling sanity**
  - For fixed `(N=1024, K=512, L)`, decode time grows modestly with `p` (no ms→s explosion).  
  - For fixed `(N, K, p)`, decode time weakly dependent on `L` (no large swings).

---

## 2) Implementation Changes (What to Change)
1) **Build A/F from inverted table (mandatory)**
   - Parse `polar_table_5_3_1_2_1_inverted.txt` as `(index, value)`; **sort by value DESC**; `A = top K_bytes` indices; cache to `cache/polar_A_N1024_K{K}.bin`.  
   - Unit test: print top/bottom 16 indices; CRC the cache file.

2) **Index order consistency (critical)**
   - Ensure **encoding** and **M = G_{A,S}** column generation use the **same order** (natural vs bit‑reversed).  
   - Unit vector test: for each bit index `j`, encode `u=e_j` and compare to generated column `col(j)` (must match bit‑exact).

3) **Decode reuse: one GE per (A,S)**
   - For each L‑group (same erasure set `S` per original codeword), **factorize once** on a single plane:  
     `Fact = factorize_GAS(A, S)` → store `(P, L, U, pivots)` over GF(2).  
   - For all **8 planes** and all packets in the group: **solve with `Fact`** (triangular XOR only).  
   - **Early‑stop** GE when rank reaches `K`; ignore remaining columns.

4) **Interleavers: LUT + inverse LUT (unchanged logic)**
   - Random interleaver: fixed permutation `π` and `π⁻¹` (cached).  
   - Slope interleaver: choose `s_step` with `gcd(s_step, M=N·L)=1`, precompute `s_inv`; cache `{M, s_step, s_inv, o_policy}`.  
   - Perform **blocked memcpy runs** per destination; no per‑byte scatter.  
   - **Measure & report** interleave and de‑interleave times **separately**.

5) **Systematic encoder (byte‑level)**
   - Follow **packet‑level** Polar layout (not the old bit‑level path).  
   - Verify **codeword bits at A equal the source bits** (per plane).  
   - With `p=0`, **skip decode** after de‑interleave (fast path).

---

## 3) Instrumentation & Metrics (What to Record)
For **every run**, log the following **per interleaver** (random & slope) and **per L ∈ {8,16,32,64,128}**:

- **Timing (ms unless noted)**
  - `t_encode_ms`
  - `t_interleave_us` (microseconds)
  - `t_deinterleave_us` (microseconds)
  - `t_GE_ms` (factorization time; should be ~once per group)
  - `t_solve_ms` (sum of all triangular solves across packets & planes)
  - `t_decode_ms = t_GE_ms + t_solve_ms`
  - `t_total_ms`
- **Counts / State**
  - `B` (lost output packets) and `p = B/L`
  - `m = |S|` (usable columns per original codeword; expect ≈ `(1−p)·N`)
  - `num_GE` (expect 1 per group) and `rank_at_stop` (expect `K`)
  - `n_packets`, `N`, `K`, `L`, `systematic`
  - `ok`, `ok_rate`, `residual_errors` (expect 0 if decoded)
- **CSV schema** (`results/run_summary.csv`):
  ```csv
  date, interleaver, L, loss_rate, N, K, systematic,
  B, m, num_GE, rank_at_stop,
  t_encode_ms, t_interleave_us, t_deinterleave_us, t_GE_ms, t_solve_ms, t_decode_ms, t_total_ms,
  packets, ok, ok_rate, throughput_MBps
  ```

---

## 4) Experiments (How to Test)
### 4.1 Baselines
- Dataset: `test_data/train_FD001.txt`, chunk to 1024‑byte packets; pad last packet with zeros.
- Config: `N=1024`, `K=512`, `systematic=true`.
- Interleavers: **Random** & **Slope** (both).  
- Loss model: **packet erasures**, i.i.d., with seeds.

### 4.2 Loss sweep (decode scaling)
For each interleaver & each `L ∈ {8,16,32,64,128}`:
- Run `p ∈ {0, 0.1%, 1%, 10%}` (≥3 seeds each).  
- Expect: decode time grows **modestly** with `p`; **no** ms→s blow‑up.

### 4.3 Reliability at p=10%
- Fix `p=10%`, run ≥1000 repeats; expect **ok_rate ≥ 99.99%**.  
- If failures occur, dump: indices of `A`, `B`, `m`, `rank_at_stop`, and interleaver parameters.

### 4.4 Interleaver microbench
- Measure `t_interleave_us` & `t_deinterleave_us` for both interleavers and all `L`.  
- Acceptance: **each ≤ 50 µs** per L‑group (modern CPU) with LUT + blocked copies.

---

## 5) Acceptance Criteria (Pass/Fail)
- **Correctness**
  - `p=0`: decode skipped; output matches input after de‑interleave.  
  - `p=10%`: ok_rate ≥ 99.99% (approaching 100%).  
- **Latency**
  - `t_encode_ms < 50 ms`, `t_decode_ms < 50 ms` on `train_FD001.txt`.  
  - `t_interleave_us ≤ 50 µs`, `t_deinterleave_us ≤ 50 µs` per L‑group.
- **Reuse**
  - `num_GE == 1` per group; `rank_at_stop == K`; `m ≈ (1−p)·N` and **independent of L**.

---

## 6) Debug Aids (Turn on During Bring‑Up)
- Print first/last 16 indices of `A` and their bit‑reversed partners; CRC of `cache/polar_A_N1024_K512.bin`.  
- Unit vector column test (encode vs generated column).  
- Interleaver round‑trip: `DeInt(Int(group)) == group` (1e5 random groups).  
- Log `(B, m, rank_at_stop, num_GE)` for each run.

---

## 7) Example CLI (Pseudo)
```bash
# Random interleaver
app --file test_data/train_FD001.txt --n 1024 --k 512 \
    --systematic true --interleaver random --L 32 \
    --loss_rate 0.10 --seed 42 --report results/run_summary.csv

# Slope interleaver
app --file test_data/train_FD001.txt --n 1024 --k 512 \
    --systematic true --interleaver slope --L 32 \
    --loss_rate 0.10 --seed 43 --report results/run_summary.csv
```

---

## 8) Checklist
- [ ] A/F from inverted table (value DESC), cached; CRC logged.  
- [ ] Encoding/column order consistent (unit vector test passes).  
- [ ] One GE per (A,S); early‑stop at rank K; solves reuse factorization across **planes & packets**.  
- [ ] Random & Slope interleavers both tested; **LUT + blocked copies**; microsecond timings logged.  
- [ ] p=0 fast path (systematic) skips decode.  
- [ ] CSV logging includes `t_interleave_us` and `t_deinterleave_us`.  
- [ ] p‑sweep and L‑sweep complete; acceptance criteria met.
