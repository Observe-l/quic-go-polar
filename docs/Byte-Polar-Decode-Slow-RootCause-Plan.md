# Byte‑Level Polar — Why Decode Is Still Slow (After Reuse) & How To Fix It
**Input:** Your latest changes (mask identity + factorization reuse, caching, num_GE counters, scratch buffers, 1‑byte solver, p=0 trimming) and the full grid of results for `N=1024`, `K=512`, `L∈{8,16,32,64,128}`, `p∈{0,0.1%,1%,10%}`, with both **slope** and **random** interleavers.

---

## 0) Key Observations From Your Results

1) **Reuse now works in many paths** (num_GE > 0 but ≪ #packets), yet **decode time still balloons with p**.  
2) **Slope** is still problematic:  
   - **much slower GE per factorization** (e.g., L=16, p=10%: GE≈5.21 s with only num_GE=78 ⇒ ≈66.8 ms/GE),  
   - and **lower ok-rate** (down to 0.47 at L=8, p=10%).  
3) **Random** is reliable (ok≈1.00 across the board), but:
   - `num_GE` grows with p and L (as expected), and  
   - **solve time dominates** at higher p (e.g., L=128, p=10%: solve≈636.7 ms vs GE≈101.1 ms).  
4) **p=0 still spends decode time** in some slope cases (e.g., L=8: dec=3.77 ms, GE=2.91 ms, num_GE=1). The fast path is not consistently triggered at the **group** level.

---

## 1) What’s Now Reasonable vs. What’s Not

- **Reasonable now:**  
  - `num_GE` reflects **#distinct erasure masks S** encountered across groups (not “1 per run”). With i.i.d. losses, masks repeat but not always; your random interleaver data matches this expectation (e.g., L=16, p=10%: num_GE=162 vs ~429 groups).  
  - Decode time increases with `p` because `m=|S|≈(1−p)N` shrinks slowly while pivot search and triangular solves grow.

- **Still not reasonable:**  
  - **Slope GE per factorization** is tens of ms, vs ~1–3 ms for random. That is not explained by linear algebra alone; it points to **mapping/column‑generation inefficiency or correctness issues**.  
  - **Ok-rate collapse under slope** (0.47–0.96 while random is ~1.00) signals a **slope mapping bug** (not mere performance).

---

## 2) Evidence‑Driven Root Causes

### R1) Slope path: expensive column generation and/or broken mapping
- **GE cost per factorization** on slope is ~10–30× that of random at the same p. Since `G_{A,S}` (chosen columns of the same polar generator) should not “magically densify” for slope, the large gap most likely comes from **how you build `S` and its columns**:
  - Recomputing **modular inverse / `s_inv`** per element or per column.  
  - Doing **per‑byte inverse mapping** `t→q` repeatedly rather than using **LUTs / blocked runs**.  
  - Interleaver **offset `o` not constant per group** (varies per source), yielding **different `S` per original** and forcing more work.  
  - Signed modulo or `t−o` underflow issues (negative `%`), causing **wrong columns** and poor rank ⇒ ok‑rate drop.

### R2) Random path: solve dominates because you are solving 8 planes **separately**
- You successfully reuse a factorization per `S`, but then you run **8 independent triangular solves**, one per bit‑plane, per packet. That is **8× redundant** work on the same `L,U`.  
- At higher p (e.g., L=128, p=10%), **solve time ≫ GE time**; this is exactly where **multi‑RHS solves** (8 planes together) and **SIMD bitset rows** pay off.

### R3) p=0 fast path not enforced at group level
- In slope L=8, p=0 you still do GE once. The detection likely checks a **per‑stream** condition, not **per‑group** `B==0` (lost outputs) *after de‑interleave*.  

### R4) Interleaver timings are still in **ms** (not µs)
- You are most likely **not using LUT + blocked copies** yet, or still dispatching **per‑byte** moves. This compounds the cost of building `S` and de‑interleaving at higher p.

---

## 3) Action Plan (Prioritized)

### A) Fix Slope Interleaver Correctness & Cost
1. **Freeze offset `o` per group** (e.g., `o=group_id & (M-1)`), not per source.  
2. Assert **`gcd(s_step, M)==1`** and **`(s_step * s_inv) % M == 1`** on startup; log `M, s_step, s_inv, o` once per group.  
3. Use **unsigned modular arithmetic** for `t - o` before `% M` to avoid negative remainder.  
4. **LUTs & blocked runs:** Precompute the forward mapping for `o=0`:
   - `q = s + L*j` → `t0 = (s_step * q) % M`; then for arbitrary `o`, `t = (o + t0) % M` is just an **add + mask**.
   - Pack the destinations by `(l = t/N)` into **contiguous runs** of `r=t%N` and copy with `memcpy`.  
   - For inverse, the same: precompute `q0 = (s_inv * t) % M`, then adjust by `-o` once.  
5. **Unit tests (must pass before perf work):**  
   - **Round‑trip**: `DeInt(Int(group)) == group` for 1e5 random groups, all L.  
   - **Mask consistency** within a group: **all originals** produce **the same `S`** for a given set of lost outputs. Log **unique hashes of `S`** per group (expect `1`).  
   - **Rank & ok‑rate** at p=10% should match random (≈1.00).

> **Expected**: GE per factorization for slope drops to **~1–3 ms**, ok‑rate → ~1.00.

### B) Make Solves 8× Cheaper (Multi‑RHS)
1. **Batch the 8 planes** as an 8‑wide RHS matrix `Y` (bit‑sliced), and run **one** forward & back substitution pass:  
   - Store `L,U` rows as **bitsets** (u64/u128/u256).  
   - Maintain **8 RHS bitsets** packed into machine words so that each XOR touches **8 planes at once**.  
2. **SIMD** the XORs (AVX2/NEON/AVX‑512) and unroll the inner loops.  
3. **Early‑stop** in solves too: skip rows that have no pivots relevant to remaining unknowns (track pivot span).

> **Expected**: solve time **÷ ~8** (or better), especially at higher p where solve dominates.

### C) Strengthen GE Heuristics
1. **Column ordering**: feed columns in **descending popcount(index)** (sparser first), or reuse the packet‑level heuristic that minimized fill‑in for you previously.  
2. **Early‑stop at rank `K`**, record `cols_scanned_until_rank_K` to ensure we didn’t scan all `m`.  
3. **Pre‑allocate** row blocks, maintain pivot bitset, avoid heap allocs in hot loops.

> **Expected**: another **1.5–3×** drop in GE cost (both interleavers).

### D) Enforce Group‑Level Fast Path for `p=0`
- After de‑interleave, compute **lost‑output count `B` per group**. If `B==0`, **skip** building `S` and **skip** decoding entirely.  
- Log a fast‑path counter to validate the win.

### E) Interleave / De‑interleave in **µs**
- Replace per‑byte moves with **LUT + blocked memcpy**. For random interleaver, the table is `π`/`π⁻¹`; for slope, use the **`t0` precomputation** described above.  
- Use `memcpy`/`std::copy`/slice `copy` with cache‑aligned runs; prefetch next block.

> **Expected**: `t_interleave_us`, `t_deinterleave_us` ≤ **50 µs** each per L‑group.

---

## 4) What To Log Next (To Prove Fixes)
- **Per‑group:** `B`, `m=|S|`, `num_unique_S`, `rank_at_stop`, `cols_scanned_until_rank_K`.  
- **Per interleaver:** `avg ms/GE`, `avg ms/solve` per packet, plus totals.  
- **Slope checks:** `M, s_step, s_inv, o` and assert invariants.  
- **Interleaver microbench:** `t_interleave_us`, `t_deinterleave_us` for all `L`.

---

## 5) Expected After Applying This Plan
- **Slope**: ok‑rate back to ≈1.00; GE cost per factorization ~ random; total decode time comparable to random.  
- **Random**: total decode time drops mainly via **multi‑RHS solves**; modest extra win from GE ordering.  
- **Overall** (`N=1024, K=512, p=10%, L in {8..128}` on your dataset size):  
  - Decode totals likely fall from **0.5–1.5 s** to **~0.15–0.35 s**, depending on L and CPU SIMD width.  
  - p=0: decode≈0 with fast‑path; interleave/deinterleave in µs.

---

## 6) Checklist
- [ ] Slope mapping: fixed `o` per group; unsigned mod; gcd/s_inv asserts; LUTs; round‑trip fuzz = pass.  
- [ ] Mask consistency: **one `S` per group** (hash).  
- [ ] GE reuse: **one factorization per mask**; log `avg ms/GE` and `num_GE`.  
- [ ] Multi‑RHS solves (8 planes together); SIMD XOR; early‑stop.  
- [ ] Group‑level fast path for `p=0`.  
- [ ] Interleaver LUT + blocked memcpy; µs timings recorded.  
- [ ] Reliability at p=10%: ok≈1.00 for both interleavers.

---

### Appendix — Slope Mapping Reference (Forward/Inverse)
Let `M = N * L`, choose `s_step` with `gcd(s_step, M) = 1`, compute `s_inv = s_step^{-1} (mod M)`, choose **group‑constant** offset `o`.
- **Forward** (source `(s,j)` to output `(l,r)`):  
  `q = s + L*j;  t0 = (s_step * q) % M;  t = (o + t0) % M;  l = t / N;  r = t % N`.
- **Inverse** (output `(l,r)` to source `(s,j)`):  
  `t = l*N + r;  t' = (t - o) % M (unsigned mod);  q = (s_inv * t') % M;  s = q % L;  j = q / L`.
