# Troubleshooting & Improvement Guide — Packet-Level **RS** and **RLC** Decoders (Go)

> **Symptom**: With `K=16, R=16 (N=32)`, decoding sometimes fails once losses ≥ 9 packets, even though theory suggests **RS** (MDS) should recover from **any ≤ R** erasures (i.e., any `K` of `N` packets). You observe similar failure rates for **RLC**.  
> **Goal**: Provide a **systematic instruction** to diagnose root causes, implement fixes, and validate with a rigorous test plan.


---

## 1) Ground Truth (What “must” be true if the code is correct)

### RS (Reed–Solomon over GF(2^8))
- **MDS property**: Any **K** out of **N** packets are sufficient to reconstruct **all K** sources, regardless of which packets are erased.  
- **Field**: A single, consistent GF(256) defined by the **same primitive polynomial** (and the same generator α) for **both** encoder and decoder.  
- **Evaluation points**: All **distinct**, consistent between encoder and decoder (e.g., `α^0, α^1, … α^(N-1)` or a Cauchy configuration).  
- **Symbol model**: Each **byte** of a packet is **one GF(256) symbol**. The same **K×K inverse** is applied **independently per byte offset** across the packets.  
- **No XOR-only shortcuts**: RS needs GF(256) **multiplications** and XOR additions for both **encoding** and **decoding** (including applying the inverse).

### RLC (over GF(2^8) or GF(2))
- **Probabilistic full-rank**: Success iff the `m×K` coefficient matrix has **rank K** over the chosen field.  
- **GF(2^8)** with dense random coefficients → success probability is very high once `m ≥ K`.  
- **GF(2)** or sparse coefficients → rank deficiency appears more often (especially at short K).


---

## 2) Likely Root Causes When RS Fails Despite m ≥ K

> Prioritize these checks; the first 4 are the most common.

1. **Field mismatch** between encoder and decoder  
   - Primitive polynomial (e.g., `0x11d` vs `0x11b`) or generator α not aligned.  
   - **Effect**: Enc/dec “agree” on being RS but live in different fields → wrong inverse, sporadic failures.

2. **Evaluation-point mismatch / wrong indices**  
   - Encoder evaluates at `{x_j}` but decoder builds Vandermonde at `{x'_j}` (e.g., off-by-one, 0-index vs 1-index).  
   - **Effect**: The submatrix you invert is not the one used to encode → decode fails for some erasure patterns.

3. **Wrong application of the inverse to payloads**  
   - RS operates **byte-wise**: for each byte offset `b` (0..L-1), compute `out[:,b] = InvK × in[:,b]` (GF(256) mult).  
   - **Anti-patterns** that break correctness:  
     - Doing a single matrix multiply across **entire byte arrays** instead of per **byte offset**.  
     - Using **XOR-only** to apply the inverse (skipping GF mult).  
     - Mixing endianness/stride errors across offsets.

4. **Inconsistent “systematic” mapping**  
   - If you assume the first K packets are systematic but your encoder actually permuted columns (or used a different systematic transform), the decoder’s column mapping is wrong.  
   - **Effect**: Submatrix selection is corrupted; invertible-in-theory matrices become singular-in-practice.

5. **Table implementation errors (GF(256))**  
   - `log/antilog` wrap at **255** must be **mod 255** (not 256).  
   - Handling of **0** (log(0) undefined) must be special-cased.  
   - Overflow of exponent accumulators (use int, not uint8).  
   - Wrong tables for the chosen polynomial.

6. **Shortening/Puncturing without consistent treatment**  
   - If you “shorten” the code (e.g., pad virtual zeros) but don’t mirror that at the decoder, Vandermonde structure is broken.

7. **Matrix build mistakes**  
   - Not including the received **systematic** packets as identity rows; column/row indices mixed.  
   - Only forward elimination without proper **back substitution** (or without recording/playing operations consistently).

8. **Memory aliasing / buffer reuse bugs**  
   - Reusing the same slice for multiple rows; swapping pointers incorrectly.  
   - **Effect**: Random-looking failures under some loss patterns.


---

## 3) Likely Root Causes When RLC Fails with m ≥ K

1. **Too small field or sparse coefficients**  
   - GF(2) or low-density random masks increase chance of rank deficiency.  
   - **Fix**: switch to GF(2^8) and dense coefficients (each a_i uniform in GF(256)).

2. **Poor randomness / repeated coefficient vectors**  
   - Seed reuse; correlated PRNG across rows; low-entropy RNG.  
   - **Fix**: cryptographic or high-quality PRNG; include nonce/ctr per parity row.

3. **No pivoting / weak elimination**  
   - Not using full pivoting in GF ⇒ rank detection is wrong / premature.

4. **Header/coeff corruption**  
   - Coeff vectors not serialized/parsed consistently; endianness/length mismatches.

5. **Same matrix-build & table issues as RS**.


---

## 4) Remediation Plan (Step-by-Step)

### A. RS — Make It Canonically Correct
1. **Freeze the field**  
   - Choose one primitive polynomial (e.g., `x^8 + x^4 + x^3 + x^2 + 1` = `0x11d`) and stick to it for **enc/dec**.  
   - Regenerate **log/antilog** tables from this polynomial. Add self-tests:
     - `a*b == antilog(log(a)+log(b) mod 255)` for all `a,b ≠ 0`.
     - `a+ a == 0` (XOR), `a+0==a`, `0*a==0`.

2. **Canonical evaluation points**  
   - Use `x_j = α^j` for `j=0..N-1` (or a known-good Cauchy construction).  
   - Export the **exact index list** used during encoding in your packet headers; decoder reads and rebuilds the correct submatrix.

3. **Byte-wise matrix application**  
   - Decode must apply `K×K` inverse **per byte offset**.  
   - Create a test: for a random `K×K` invertible over GF(256), verify `Inv × (A × v) == v` for **every byte position** independently.

4. **Systematic mapping sanity**  
   - If you want systematic RS, use a standard systematic transform or an established library formula.  
   - Serialize the **column permutation** (if any) and replay it at the decoder.

5. **Matrix construction tests**  
   - Unit test: For any random erasure set E (|E|≤R), build submatrix from the **remaining** columns; verify invertible (should always be true for RS).  
   - If any submatrix is singular, print the exact column list and check your evaluation points and field.  

### B. RLC — Boost Rank and Robustness
1. **Switch to GF(2^8)** (if you used GF(2)) and draw **dense** coefficients uniformly.  
2. **Use full pivoting** during elimination.  
3. **Serialize coefficients** in packet headers; ensure length and endianness are validated.  
4. **PRNG hygiene**: independent seed/nonce per parity row; test for duplicate rows.  
5. **Rank monitoring**: Measure rank vs number of received packets `m`, ensure the rank curve approaches `K` quickly.

### C. Shared Infrastructure Fixes
- **Op-log split**: Keep your matrix elimination (bit-level) separate from data application (byte XOR/mult). This helps you time and also audit correctness (the same op log must reconstruct data).  
- **Deterministic tests**: Fix RNG seed per test case for reproducibility.  
- **Fuzz tests**: Random erasures, random payloads, random seeds, thousands of trials.  
- **Instrumentation**: Count GF mult/XOR, rank, pivot patterns; dump failing patterns for triage.


---

## 5) Post-Fix Test Plan (What & How to Measure)

### 5.1 RS — Deterministic MDS Validation
- **All-K Subsets Test** (small K): For `K=8, R=8 (N=16)`, **exhaustively** test **all** `C(16,8)=12870` subsets. Every subset must decode.  
- **Random Subsets at K=16, R=16**: Sample ≥ 10,000 random erasure sets of size `e`, for `e=0..16`. For each, ensure **decode always succeeds when m=K** (i.e., `e ≤ R`).  
- **Byte-wise correctness**: For each byte position independently, verify `decoded == original`. Compare full packet digests (e.g., SHA-256).  
- **Field conformance**: Run the field self-tests at startup; abort on mismatch.

**Expected**: **Zero** decode failures for all `e ≤ R`. Any failure indicates implementation or configuration error.

### 5.2 RLC — Probabilistic Rank Curve
- **Field impact**: Compare GF(2) vs GF(2^8), same `K=16, R=16`.  
- **Rank vs m**: For m from K to N, estimate `Pr(rank(A)=K)` over ≥10,000 trials.  
- **Success probability**: With GF(2^8) dense coefficients and full pivoting, success should be **~100%** at `m=K` (rank-K) except for vanishingly rare collisions. With GF(2) or sparse masks, expect notable shortfalls near `m=K`.

### 5.3 Performance sanity
- Log GF mult/XOR counts and running times for your platform (ARM/x86).  
- RS should perform more GF(256) multiplications than RLC (with sparse masks) but should **never** fail when `m ≥ K`.


---

## 6) Quick Triage Checklist (When You See a Failure)

1. Print the **erasure set** and the **indices of received packets**.  
2. Dump the **exact evaluation points** used by encoder and by decoder for those packets — they must be identical.  
3. Verify the **field polynomial** and **tables** match on both sides.  
4. Log whether you applied the inverse **per byte offset** (if not, fix it).  
5. Dump the selected `K×K` submatrix; over GF(256), compute its det — must be non-zero for RS.  
6. Re-run the same case with an **independent RS implementation** (e.g., a reference library) to cross-check.


---

## 7) Expected Outcomes After Fixes

- **RS**: 100% decode success for all erasure patterns with `e ≤ R` (any `K` of `N`).  
- **RLC (GF(2^8), dense)**: Empirical success ~~ 100% at `m=K` (very high), strictly increasing for `m>K`.  
- **RLC (GF(2), sparse)**: Noticeable failure probability near `m=K`; improves as `m` grows.  
- Your timing/operation counters will reflect: RS > RLC > Polar in GF(256) **multiplication** cost; Polar/XOR-only lowest energy but not MDS.


---

## 8) Appendix — Common RS Implementation Pitfalls (Byte-Level)

- **Not per-byte**: Applying the matrix once across the entire buffer instead of per offset.  
- **Stride errors**: Mixing offsets (e.g., using step > 1 or wrong alignment).  
- **Zero-handling in tables**: `log(0)` and multiply-by-zero bugs.  
- **Modulo mistakes**: exponents must be mod **255**, not 256.  
- **Evaluation point 0**: If you include 0, ensure you define encoding correctly (usually avoid 0 to keep Vandermonde invertible).  
- **Shortening without coordination**: Pad rules must match at dec.  
- **Row/col permutations**: Always serialize and replay the exact layout.
