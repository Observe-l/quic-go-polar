# Implementation Update — Systematic Byte‑Level Polar Code & Reliability Table Integration

This document specifies **code changes**, **tests**, and **acceptance criteria** to (a) switch to **systematic Polar** at the **byte‑level**, (b) correctly **build the frozen/data sets from `polar_table_5_3_1_2_1_inverted.txt`**, (c) **reuse the existing _packet‑level_ Polar implementation** as the template (not the bit‑level one), (d) add a **no‑decode fast path** when `packet_loss = 0`, and (e) **keep interleaving logic unchanged**.

> TL;DR
> 1) Use **systematic polar**.  
> 2) Parse **`polar_table_5_3_1_2_1_inverted.txt`**: **col1 = channel index**, **col2 = reliability value** (larger = more reliable). **Sort by col2 DESC**, take the **top `K_bytes`** indices as data set `A`, others are frozen.  
> 3) Replace the current “table → sets” builder with the above logic.  
> 4) For byte‑level Polar, **follow the existing *packet‑level* Polar code structure** (it is closer to the byte‑level design).  
> 5) Add **fast path**: if `packet_loss == 0` and we use **systematic**, **skip decoding** entirely (after de‑interleaving).  
> 6) **Interleavers** (Random & Slope) remain the same; their caches & file formats are unchanged.

---

## 1) Affected Modules & New Flags

- **Affected**:
  - `polar/packet_polar_*` (use this as reference template for byte‑level; do **not** reuse the old bit‑level scaffolding).
  - `polar/byte_polar_*` (new or updated module).
  - `interleave/*` (unchanged code paths, only integration points).
  - `fec_pipeline/*` (integrates fast path when loss=0).
- **New/Updated Flags**:
  - `--systematic` (bool, default: `true`) – use systematic Polar for byte‑level.
  - `--reliability_table` (path, default: `tables/polar_table_5_3_1_2_1_inverted.txt`).
  - `--k_bytes` (int, e.g., `512`).
  - `--n_bytes` (int, fixed at `1024` for current experiments).
  - `--interleaver {random,slope}` and `--L {8,16,32,64,128}` (unchanged).
  - `--loss_rate` (float, e.g., `0.0` or `0.1`) – packet erasure probability.

---

## 2) Frozen/Data Set Construction (Mandatory Fix)

**Input file**: `polar_table_5_3_1_2_1_inverted.txt`  
**Format**: two columns per row  
- **col1**: **channel index** in `[0 … N-1]` (for N=1024).  
- **col2**: **reliability value** (**larger = more reliable**).

**Required builder** (replace current logic):
```text
Read all rows (index, value)
Sort rows by value in DESC order (stable by index if needed)
Let A = first K_bytes indices from the sorted list        // DATA set
Let F = remaining (N - K_bytes) indices                    // FROZEN set
Cache A to cache/polar_A_N1024_K{K}.bin (little-endian int32)
Reuse the same A for all 8 bit-planes
```
**Note**: This supersedes any prior assumption that the table is pre‑sorted by reliability or that “smaller index = more/less reliable”. The **inverted** table’s **second column is authoritative**.

---

## 3) Systematic Byte‑Level Polar — Encoder

**Why systematic?** In typical networks the **packet loss often equals 0**, so with systematic Polar the receiver can **bypass decoding** entirely, saving significant CPU time.

**Design (byte‑level)**
- Interpret each 1024‑byte packet as **8 parallel bit‑planes** of length **N=1024 bits** (one Polar of size 1024 per plane).  
- Use the **packet‑level Polar implementation** as the structural template (buffers, transforms, memory layout). Do **not** use the legacy bit‑level path.
- **Systematic encoding** (common practical recipe):
  1) Place information bits `u_A` at indices `A`, frozen bits `u_F=0` elsewhere.  
  2) Encode once to `x₀ = u·G_N` (standard butterfly).  
  3) Run a **systematicization step** to enforce **codeword positions at `A` equal the source bits**. Two equivalent options are acceptable:
     - **Option S1 (re‑encode via SC)**: Decode `x₀` with a Polar **SC encoder/decoder loop** constraining info positions to the source bits, producing `x_sys`.  
     - **Option S2 (algebraic)**: Compute the **systematic generator** effect using the `G_{A,*}` submatrix (as per your packet‑level reference) and apply an extra butterfly to obtain `x_sys`.
  4) Pack the 8 systematic planes back into bytes → **systematic encoded packet**.
- **Caching**: reuse `A` for all planes; cache scratch buffers for butterflies.

---

## 4) Decoder & Fast Path (BEC + Systematic)

**Fast path** (NEW):  
If `packet_loss == 0` (no erased packets) and we use **systematic**, then after **de‑interleaving**:
```text
Skip Polar decoding entirely.
Output the de-interleaved payload as the recovered packet.
```
**BEC decoding path** (unchanged when loss > 0):  
- Use your existing **Gaussian Elimination + XOR** per bit‑plane to solve on the set of **received positions S** (ML on BEC).  
- Early stop once rank reaches `K_bytes` per plane; repack planes → bytes.

---

## 5) Interleaving (UNCHANGED)

Keep both methods as‑is; cache formats unchanged.

- **Random interleaver (fixed π, cached)** – good for i.i.d. loss, reproducible.  
- **Slope interleaver** – provable uniform spread for any consecutive physical window `W`; recommended when burst losses are present.

The **byte‑level systematic encoder** plugs into the same interleaver APIs you already implemented for packet‑level Polar. No API changes.

---

## 6) Integration Steps (Concrete To‑Dos)

1) **Replace table parser** in the byte‑level module to the mandated logic (Section 2).  
2) **Add `--systematic`** flag and set default `true` for byte‑level path.  
3) **Port packet‑level Polar structures** to byte‑level:
   - Buffer layouts (8 planes), in‑place butterflies, SIMD packing.  
   - Systematicization step (reuse the packet‑level approach you already have).
4) **Hook fast path** in the pipeline:
   - After channel + de‑interleaver, **if `loss_rate == 0`**, return payload without decoding.  
   - Ensure metrics still measure encode/interleave times (decode time = 0).
5) **Keep interleaver code unchanged**, only ensure adapters accept the byte‑level packet buffers.  
6) **Cache**: write/read `cache/polar_A_N1024_K{K}.bin` once per `(N,K)`; validate cache hit.  
7) **Unit tests**:
   - **Table → sets**: for a toy file, verify top‑K by value selection and cache contents.  
   - **Systematic property**: after encoding, positions `A` in the **codeword** equal the source bits (per plane).  
   - **Fast path**: with `loss_rate=0`, output equals input (after de‑interleave).  
   - **Regression**: interleave → de‑interleave is identity (bit‑exact).

---

## 7) Test Plan (Updated)

**Dataset**: `test_data/train_FD001.txt`  
**Parameters**: `N=1024`, `K=512`, `L ∈ {8,16,32,64,128}`, `systematic=true`  
**Interleavers**: Random & Slope

### Runs
1) **Sanity (no loss)**: `loss_rate=0`  
   - Expect **decode_time_ms = 0** (fast path).  
   - Success rate = 100%; residual error = 0.
2) **FEC (10% loss)**: `loss_rate=0.1` (packet erasure, i.i.d.)  
   - Decoder used = **GE+XOR**.  
   - Expect near‑100% recovery at rate `R=0.5`, both interleavers.

### Metrics
- `t_encode_ms`, `t_interleave_ms`, `t_decode_ms`, `t_total_ms`  
- `ok_rate`, `throughput_MBps`  
- Log `(systematic, interleaver, L, N, K)`

---

## 8) Expected Results (Updated)

- **No loss (`loss_rate=0`)**: `t_decode_ms = 0`; `ok_rate = 100%`.  
- **10% loss**: `ok_rate ≈ 100%`; GE dominates CPU time; interleaver choice yields similar results on i.i.d. loss (Slope is superior for bursty traces).

---

## 9) Output Artifacts

- Console summary + CSV line per run in `results/run_summary.csv`:
```csv
date, systematic, interleaver, L, loss_rate, N, K, packets, ok, ok_rate,
t_encode_ms, t_interleave_ms, t_decode_ms, t_total_ms, throughput_MBps
```
- Cache: `cache/polar_A_N1024_K512.bin`  
- Logs: `logs/failed_packets.txt` (if any)

---

## 10) Acceptance Checklist

- [ ] **Systematic encoder** integrated for byte‑level Polar (using packet‑level reference).  
- [ ] **Correct A/F sets** built from `polar_table_5_3_1_2_1_inverted.txt` (col2 DESC).  
- [ ] **Fast path**: `loss_rate=0` → skip decoding.  
- [ ] **Interleavers unchanged**; round‑trip identity test passes.  
- [ ] **Tests pass** on `test_data/train_FD001.txt` with `K=512` for both `loss_rate=0` and `0.1`.  
- [ ] CSV produced; caches saved; timings reasonable.

---

### Notes & Pitfalls
- Ensure the inverted table parser is robust to whitespace and blank lines; validate `N=1024`.  
- When sorting by reliability value, **tie‑break by channel index** to keep determinism.  
- Verify **systematic property** per plane: positions in `A` of the **codeword** equal the original info bits.  
- Keep GE rows bit‑packed (64/128‑bit words) to maintain earlier performance.
