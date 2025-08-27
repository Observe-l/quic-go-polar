# Troubleshooting & Improvement Guide — Packet-Level **Polar FEC** (Go)

> **Symptom**: With `N=32, K=16 (R=16)`, your packet-level Polar FEC shows **high decode failure probability** once losses exceed **~5 packets**.  
> **Goal**: Provide a **design-level instruction** to (1) analyze likely causes, (2) apply targeted fixes, and (3) validate with a rigorous test plan.


---

## 1) Ground Truth for Packet-Level Polar (What “should” be true)

1. **Code model**: We treat Polar as a **linear block code over GF(2)** at packet level (no LLR/SC). Encoding/decoding are **pure XOR**.  
2. **Systematic construction**: Use a **systematic generator** so that the K original packets appear as-is, and R parity packets are XORs of sources.  
3. **Decoding criterion**: Under erasures, success depends on **rank**: the submatrix built from the **received columns** must have rank **≥ K** (or equivalently, the `S×P` submatrix over the **lost-source rows** must have full row-rank).  
4. **Short-block regime**: Polar is **not MDS**; at short `N` it can be fragile unless the information set and parity schedule are carefully designed for the **actual erasure process** (i.i.d. vs bursty).


---

## 2) Likely Root Causes of Poor Performance at `N=32, K=16`

> Prioritize the first six; they are the most common in practice.

1. **Non-systematic or broken systematic transform**  
   - If you used the raw `G_N = F^{⊗n}` without producing a **systematic** generator, received source packets won’t be “free wins,” and the column structure can be unfavorable.  
   - **Effect**: Early rank is worse; more parity needed to reach full rank.

2. **Wrong information set (A) for the actual PLR**  
   - You must pick A via **BEC Bhattacharyya recursion** using an **estimate of ε** (packet loss rate). If you fixed A at ε=0.1 but the real ε≈0.25 (or vice versa), you may be selecting **mediocre** subchannels as information bits.  
   - **Effect**: The induced parity submatrix over lost-source rows gets **low rank** more often.

3. **Incorrect G construction (bit-reversal / index mapping / submatrix slicing)**  
   - Polar `G_N` depends on **indexing order** (natural vs bit-reversed). If you compute `F^{⊗n}` but index columns/rows inconsistently when extracting `G_AA` and `G_{A A^c}`, your `G_par = [G_AA G_{A A^c}]^T` can be wrong.  
   - **Effect**: Parity rows do not match the intended positions → rank collapses for many loss patterns.

4. **Parity column scheduling not optimized (no “generality” ordering)**  
   - At short `N`, parity columns have **heterogeneous weights/shapes**. If you transmit parity in arbitrary order, early columns may bring **little incremental rank**.  
   - **Effect**: With 5–8 losses, your received parity set often lacks the “right” columns to span the lost-source rows.

5. **Low-weight / clustered column patterns**  
   - Some parity columns have very low Hamming weight (few source rows marked 1) or are **clustered** over similar rows.  
   - **Effect**: When those rows are the ones lost, received parity columns are **highly dependent**, reducing rank.

6. **Burst erasures not mitigated**  
   - If losses are bursty (Gilbert–Elliott) and you don’t **interleave** across time, it’s easy to drop many **correlated columns** (e.g., consecutive, similarly-shaped columns).  
   - **Effect**: Frequent rank deficiency even when `m ≥ K`.

7. **Matrix/domain bugs**  
   - Mixing up **row vs column** orientation when building `S×P` submatrix; not including received source columns as identity; wrong padding/length alignment; partial rows of zeros due to padding logic, etc.  
   - **Effect**: Spurious rank failures.

8. **Decode architecture not matching “matrix-only + replay”**  
   - If you entangle bit-matrix elimination with payload XOR in one pass, off-by-one swaps or aliasing can silently break rank while looking like “payload” issues.  
   - **Effect**: Apparent random failures.

9. **Too small N for your reliability target**  
   - Polar’s **polarization is weak at N=32**; if your target is “RS-like” success at losses 8–12, `N=32` can be inherently insufficient unless you add other structure (ordering, mixing, interleaving) or increase N.


---

## 3) Remediation Plan (Step-by-Step)

### A. Make the Construction Canonical & Systematic
1. **Recompute A with BEC recursion using your best ε estimate**  
   - Compute Bhattacharyya values with ε estimated from a sliding window (EWMA) of measured loss.  
   - Pick the **K smallest** Z_i as A; tie-break with column **weight** preference (slightly prefer columns that diversify rows you tend to lose).

2. **Build a correct **systematic** generator**
   - Construct `G_par = [ G_AA · G_{A A^c} ]^T` with a consistent indexing convention (decide **once**: bit-reversed or natural).  
   - Validate via **round-trip test**: encode → decode a fully received block (no erasures) must yield identity mapping for the K sources.

3. **Parity column ordering by “generality”**
   - Rank parity columns by a **generality score** that favors early rank growth across unknown rows, e.g.:  
     - higher Hamming weight (but not too high),  
     - coverage diversity across rows (minimize pairwise overlap),  
     - historical **marginal rank contribution** on your channel (online learning option).  
   - Transmit parity in this order; with incremental redundancy, early parity packets have maximal utility.

4. **Row/column mixing (optional but effective at N=32)**
   - Apply a light **pre-mixing** transform on sources (e.g., a small, invertible GF(2) scrambling matrix) **before** the systematic embedding to increase minimum rank over common `S` sets.  
   - Keep the transform fixed and serialize its seed/id; inverse it after decoding.

5. **Time interleaving against bursts**
   - Spread a block’s columns **across multiple RTTs / frames** so bursts do not wipe out adjacent, similar columns.  
   - Depth-2 or depth-4 interleaving is usually enough to mitigate GE bursts at short N.

6. **Increase N to 64 when latency budget allows**
   - `N=64, K=32` often shows a **clear success-rate lift** at losses 6–12, thanks to stronger polarization and richer column set.  
   - Keep the same systematic + ordering + interleaving logic.

7. **Decode architecture: split & verify**
   - Adopt “**Phase A: matrix-only elimination (record ops)** → **Phase B: replay ops on payload**” (see previous guidance).  
   - This makes rank failures diagnosable at matrix level (no payload side-effects).


---

## 4) Validation & Diagnostic Tests (Pre- and Post-Fix)

### 4.1 Structural correctness
- **Identity round-trip**: With no erasures, decode must return the K inputs exactly (byte-identical).  
- **Permutation sanity**: Randomly permute column order at encoder; ensure decoder rebuilds the **same order** from headers and still decodes.  
- **Rank oracle**: For a random block, enumerate all erasure sets of size `e=0..6` (feasible at K=16) and compute the submatrix rank; confirm that failures correlate with your pre-fix issues (e.g., certain `S` sets).

### 4.2 Rank vs losses (i.i.d.)
- For `N=32, K=16`, simulate i.i.d. erasures for `ε ∈ {0.02, 0.05, 0.1, 0.15}`.  
- For each ε, run ≥ 10,000 trials. Track:  
  - success probability vs **#lost packets e** and vs **#received m**,  
  - average **m needed** for success,  
  - distribution of **rank(H[S,P])**.  
- **Expected after fixes**: Success probability increases by **15–40% absolute** in the `e=6..10` band, with **fewer low-rank outliers**.

### 4.3 Burst losses (Gilbert–Elliott)
- Use a GE model (e.g., `P_GB=0.015, P_BG=0.30, e_G=0, e_B=1`, steady PLR ~4.8%).  
- Evaluate **with vs without interleaving**; measure success rate at `e=6..10`.  
- **Expected after interleaving**: Outage probability under bursts drops dramatically (often >×2 improvement).

### 4.4 Column ordering A/B
- Compare **random parity order** vs your **generality-ordered** parity.  
- Measure “success probability at fixed m” and “average m needed” (incremental redundancy regime).  
- **Expected**: Ordered parity yields **earlier** success (lower m) and **higher** success at the same m.

### 4.5 N-scaling
- Repeat the i.i.d. tests for `N=64, K=32`.  
- **Expected**: Clear success-rate lift for `e=6..12`, validating the “increase N when latency allows” guidance.


---

## 5) Instrumentation (What to log per block)

```
N,K,R, loss_model, epsilon_est,
rank, m, e, success,
num_source_recv, num_parity_recv,
elim_time_ns, apply_time_ns,
row_swaps, row_xors, apply_row_xors,
bytes_xored,
parity_ordering_id, mixing_id, interleaver_depth, seed
```

- Use this to build **success vs m**, **success vs e**, and **rank histograms** before/after each fix.  
- Keep RNG seeds for reproducibility; dump failing `S` and received parity indices `P` for triage.


---

## 6) Expected Outcomes After Fixes

- At `N=32, K=16`, **with** (i) correct systematic generator, (ii) A matched to ε, (iii) parity ordering by generality, and (iv) shallow interleaving:  
  - Success probability at `e=6..8` improves substantially (often **+15–40% absolute** over naive).  
  - Fewer pathological erasure patterns cause rank deficiency.  
- At `N=64, K=32`, further improvement in the `e=6..12` range, at the cost of 2× buffering/latency.  
- Timing counters remain XOR-only; elimination vs payload replay time should follow **O(K³)** vs **O(K²·L)** trends.


---

## 7) Quick Checklist When You See a Failure

1. Print the **lost-source set S** and **received parity set P**.  
2. Compute rank of `H = G_par[S,P]` — confirm rank deficiency (it should be < |S| when failure happens).  
3. Verify **indexing convention** (bit-reversal/natural) is consistent across all steps.  
4. Rebuild A with the **current ε estimate**; check if the failing `S` lies on weak rows.  
5. Test with **parity-ordering** enabled — does the same pattern now decode?  
6. Re-run with **interleaving** — does the failure disappear under the same burst trace?


---

## 8) Notes & Pitfalls Specific to Polar at Packet Level

- **Polar ≠ RS**: Don’t expect MDS behavior; Polar needs **careful A choice** and **parity scheduling** to be competitive at short N.  
- **Indexing matters**: `F^{⊗n}` often uses bit-reversal ordering; be consistent when slicing submatrices.  
- **Low-weight columns**: Too many early low-weight columns are harmful; mix or reorder.  
- **Don’t mix SC/LLR with packet-level**: Keep the decoding as GF(2) linear algebra; using SC-style logic in this setting is a mismatch.


---

## 9) Minimal Roadmap (Apply in this order)

1. Fix **systematic generator** + **indexing** + **identity round-trip**.  
2. Recompute **A(ε)** and lock it to your measured ε range.  
3. Enable **parity ordering (generality)**; ship order id in headers.  
4. Add **interleaving** (depth 2–4) for burst channels.  
5. (If latency allows) test **N=64**; keep the other fixes.  
6. Instrument & run the **validation suite** (i.i.d. + GE + ordering A/B + N scaling).

