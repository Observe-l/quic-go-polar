# Decode-Time Blowup vs. Loss — Targeted Analysis & Action Plan
**Context.** You measured decode time vs. packet loss `p` for byte-level Polar with two interleavers (slope, random), `N=1024`, `K=512`, `L=16`, dataset size ≈6866 packets.

```
Byte-Polar[slope]: L=16 p=0.0000 | enc=72.96ms inter=26.1ms deint=27.7ms dec=1.60ms (GE=0  solve=0    ) | ok=1.00
Byte-Polar[slope]: L=16 p=0.0010 | enc=72.96ms inter=27.2ms deint=26.8ms dec=40.37ms (GE=20.96ms solve=11.61ms) | ok=1.00
Byte-Polar[slope]: L=16 p=0.0100 | enc=72.96ms inter=26.1ms deint=26.7ms dec=430.29ms (GE=261.33ms solve=112.58ms) | ok=0.98
Byte-Polar[slope]: L=16 p=0.1000 | enc=72.96ms inter=26.1ms deint=24.5ms dec=6.51s  (GE=5.20s   solve=251.70ms) | ok=0.51

Byte-Polar[random]: L=16 p=0.0000 | enc=72.96ms inter=13.5ms deint=14.2ms dec=1.32ms (GE=0       solve=0       ) | ok=1.00
Byte-Polar[random]: L=16 p=0.0010 | enc=72.96ms inter=13.5ms deint=14.5ms dec=31.24ms (GE=11.61ms solve=14.70ms)
Byte-Polar[random]: L=16 p=0.0100 | enc=72.96ms inter=13.8ms deint=14.4ms dec=168.09ms (GE=30.27ms solve=126.55ms)
Byte-Polar[random]: L=16 p=0.1000 | enc=72.96ms inter=13.6ms deint=13.2ms dec=960.66ms (GE=313.25ms solve=528.35ms) | ok=1.00
```

The decode time grows sharply with `p`, and slope interleaver is much worse than random (both in time and `ok_rate`).

---

## A. What’s Reasonable vs. What’s Not

- **Reasonable:** With higher `p`, **some** decode slowdown (fewer usable columns, more pivot search) is expected. With reuse of the factorization per `(A,S)` and early-stop at rank `K`, the growth should be **modest** (e.g., ms → tens/hundreds of ms).  
- **Not reasonable (what we see):** ms → seconds as `p` increases; slope `p=10%` `GE≈5.2s` and `ok≈0.51` while random stays ≤1s and `ok=1.00`. This indicates **algorithmic reuse is failing** and/or there’s a **slope-specific mapping bug** creating “hard” matrices and rank issues.

---

## B. Likely Root Causes (Evidence-Driven)

### B1) **GE reuse failure (slope path) — doing GE ~L times per group**
- At `p=10%`, **slope GE time ≈ 5.20 s** vs **random GE ≈ 0.313 s**. The ratio **≈ 16.6×**, which **matches `L = 16`**.  
- This strongly suggests in slope mode you are **refactorizing once per original codeword** (i.e., L times per group), **instead of once per group**.  
- Why would that happen? Because the de-interleaver / erasure-set builder is likely producing **different `S` per original codeword** (it should be the **same** for all originals within the same output loss pattern). If `S` differs, reuse of the factorization is impossible → **GE repeated L times**.

### B2) **Slope interleaver mapping bug → inconsistent `S`, poor `ok_rate`**
- Slope `ok_rate` collapses to **0.51** at `p=10%`, but random stays at **1.00**. That’s a correctness smell, not just speed.  
- Common pitfalls:
  - Wrong linearization: correct is `q = s + L*j`, not `q = j + N*s` or mixed.  
  - Wrong slicing back: `l = floor(t / N)`, `r = t % N`.  
  - Inverse mapping: must use **`s_inv = s_step^{-1} mod M`**, and `q = s_inv * (t - o) mod M`. Signed modulo mistakes can break this.  
  - **Offset policy `o`** changing per *source* rather than per *group*. If `o` varies across sources, each original codeword sees a **different `S`**, causing both **GE×L** and uneven erasures → **rank loss**.  
  - Not verifying `gcd(s_step, M) = 1` (here `M=N·L=16384`).

### B3) **GE column ordering & early-stop not effective enough**
- GE time dominates, and grows superlinearly. Ensure:
  - **Column ordering:** insert columns in **descending popcount** of the bit index (sparser first) or another cache-friendly ordering to reduce fill-in.  
  - **Early-stop:** stop as soon as rank hits `K` (don’t sweep all `m` columns).  
  - **Bitset rows + SIMD XORs** to keep constant factors low.

### B4) **Interleaver timings are unexpectedly large**
- Interleave/de-interleave in the logs are **13–27 ms** each, not **µs**. This hints LUT not being used (or extremely fine-grained scatter stores). It does not explain the GE blow-up, but it indicates **copy path inefficiency**.

---

## C. What to Log Next (to confirm/deny)
Add these per-group diagnostics for both interleavers and all `p`:

1) **Uniqueness of `S` across originals:**  
   - For each group, compute `hash(S)` for **each original codeword**. Expect **one unique hash**.  
   - If slope shows **L distinct hashes**, you are refactorizing L times → fix mapping/offset.
2) **Loss math check:**  
   - Record `B` (lost output packets) and `m = |S|`. Expect `m = N - B·(N/L)` (exact with our constructions).  
   - `m` should **not depend on which original** within the group.
3) **GE stats:**  
   - `num_GE` per group (expect `1`).  
   - `rank_at_stop` (expect `K=512`); `cols_scanned_until_rank_K` (should be well below `m`).  
4) **Slope parameters:**  
   - Log `M, s_step, s_inv, o` per group. Assert `gcd(s_step,M)=1` and `(s_step * s_inv) mod M = 1`.
5) **Round-trip tests online (optional):**  
   - For random batches during the run: test `DeInt(Int(group)) == group` (bit-exact).

---

## D. Concrete Fixes (Prioritized)

1) **Force one `(A,S)` per group; reuse factorization**
   - Fix slope mapping so **every original codeword sees the same `S`** given a particular set of lost output packets.  
   - Set **offset `o` per group**, not per source; verify linearization `q = s + L*j` and slicing `l = floor(t/N)`, `r = t%N`.  
   - After de-interleave, **build `S` once**, compute **one GE**, then reuse for all 8 planes and all originals via triangular solves.

2) **Strengthen GE efficiency**
   - Implement **popcount-desc** column ordering (or equivalent heuristic).  
   - **Early-stop** at rank `K`; maintain a pivot bitset to avoid re-work.  
   - SIMD XOR on 64/128/256-bit lanes; pre-allocate all buffers; no heap in hot loops.

3) **Make interleaver µs-level**
   - Build **LUT + inverse LUT**; execute **blocked memcpy runs** per destination (no per-byte scatter).  
   - With `N=1024`, `L in {8..128}`, interleave or deinterleave should take **≤ 50 µs** each per group.

---

## E. Expected After Fix
- **Slope vs. Random parity:** With correct mapping and reuse, slope’s GE time should match random’s order of magnitude. For `p=10%`, expect **GE ~ 0.3–0.6 s** across the full dataset (then more reductions from GE heuristics).  
- **No ms→s explosion:** Decode time grows gently with `p`.  
- **Reliability:** With `R=0.5`, `p=10%` i.i.d., expect **ok≈1.00** (≥99.99%) for both interleavers.  
- **Interleaver timing:** each in the **µs** range.

---

## F. Verification Plan
1) **Instrument & re-run** the same grid (`L=16`, `p = 0, 0.1%, 1%, 10%`) for both interleavers.  
   - Confirm: `unique_hashes(S) == 1`, `num_GE == 1`, `rank_at_stop == K`, and `m = N - B·N/L` holds.
2) **Reliability sweep (`p=10%`)** ≥1000 repeats.  
   - Expect ok_rate ≥ 99.99%; if not, dump `(A, B, m, rank_at_stop, interleaver params)` for the failures.
3) **Interleaver microbench** for all `L ∈ {8,16,32,64,128}`.  
   - Expect `t_interleave_us, t_deinterleave_us ≤ 50 µs`.

---

## G. Minimal Code To-Dos
- [ ] Add `hash(S)` per original; assert single unique per group.  
- [ ] Log `(B, m, num_GE, rank_at_stop, cols_scanned_until_rank_K)` per group.  
- [ ] Validate slope params: `gcd(s_step,M)==1`, `(s_step*s_inv) mod M == 1`, fixed `o` per group.  
- [ ] Enforce GE reuse (`Fact` cache) across 8 planes & all originals in the group.  
- [ ] Implement column ordering & early-stop; SIMD XOR; pre-alloc.  
- [ ] Build LUT-based blocked interleaver / deinterleaver.

---

### Appendix — Correct Slope Mapping (reference)
- `M = N * L`, choose `s_step` with `gcd(s_step,M)=1`, choose group offset `o` (fixed per group).  
- Forward:
  ```text
  q = s + L*j                // s in [0..L-1], j in [0..N-1]
  t = (o + s_step * q) mod M
  l = floor(t / N)
  r = t % N
  Out[l][r] = C[s][j]
  ```
- Inverse:
  ```text
  s_inv = s_step^{-1} mod M
  t = l*N + r
  q = (s_inv * (t - o)) mod M
  s = q % L
  j = floor(q / L)
  C[s][j] = In[l][r]
  ```

If any of the above differs in code (including signed/unsigned modulo), fix it; otherwise `S` will vary per original and both time and reliability will suffer.
