# Offline Script Task Checklist — Generating Polar FEC Tables (Packet-Level, GF(2) Linear Algebra)

## 0. Objective & Scope
- **Objective**: For common `(N=2^n, K)` and chosen `ε-band` (or erasure counts `e`), generate offline:
  1. **Information set A** (stable, robust against ε drift),  
  2. **Parity column ordering parity_order** (to maximize incremental rank gain),  
  3. **Metadata** (indexing, ε grid, UPO/URS references, version ID/hash).  
- **Target scenario**: Packet-level BEC + linear algebra decoding (GF(2) XOR), focusing on **full-rank probability** and **early success rate**.  
- **Not included**: SC/SCL/LLR AWGN design for PHY.

---

## 1. Directory & Output Layout
- `tables/` — final repository (JSON/CSV):  
  - `N{N}_K{K}/table_{id}/A.json`  
  - `N{N}_K{K}/table_{id}/parity_order.json`  
  - `N{N}_K{K}/table_{id}/meta.json`  
- `logs/` — run logs and statistics (rank histograms, success rates).  
- `reports/` — generated Markdown/HTML validation reports.  
- `cache/` — intermediate (Z values, frequency counts, Monte Carlo samples).  

**meta.json required fields**:  
```json
{
  "version": "v1",
  "indexing": "bit-reversed|natural",
  "epsilon_grid": [0.16, 0.18, 0.20],
  "band_policy": "majority-vote+coverage",
  "upo": "enabled|disabled",
  "urs_base": "3gpp_38.212_vXX",
  "optimization_target": "max_E_rank|min_rank_deficit",
  "parity_order_policy": "marginal-rank-gain",
  "interleaver_depth": 0,
  "seed": 12345,
  "A_hash": "sha256:...",
  "parity_order_hash": "sha256:..."
}
```

---

## 2. CLI Parameters
- **Required**:  
  - `--N 32 --K 16` (multi-values allowed)  
  - `--indexing natural|bit-reversed`  
  - `--epsilon-grid 0.16,0.17,...,0.22` (or `--losses e_min:e_max`)  
- **Optional**:  
  - `--urs-baseline on|off`  
  - `--upo on|off`  
  - `--band-policy majority|median|topk+coverage`  
  - `--mc-trials 10000`  
  - `--rank-target e=6` (multi-values allowed)  
  - `--parallel 8`  
  - `--seed 12345`  
  - `--out tables/N{N}_K{K}/table_{id}`  

---

## 3. Workflow Stages (A–E)

### Stage A · Reliability Calculation (BEC Bhattacharyya)
- A1. Fix **indexing** (bit-reversed or natural).  
- A2. For each ε in grid, run BEC recursion (float64):  
  - `Z^- = 2Z - Z^2`, `Z^+ = Z^2` with init `Z=ε`.  
- A3. Sort with **stable tie-break** (e.g., by index or weight).  
- A4. Save results and hashes in `cache/`.  

**Acceptance**: deterministic ordering across runs; monotone variation across ε.  

---

### Stage B · Robust Information Set A
- B1. **Majority voting**: count frequency of each index across ε grid Top-K, take top K-Δ.  
- B2. **Coverage-based fill**: use rank proxy to fill Δ remaining, constrained by UPO if enabled.  
- B3. Output candidate A sets (majority/median/URS+swap), score them.  

**Acceptance**: A_hash logged; stable across repeated runs.  

---

### Stage C · Parity Column Ordering
- C1. Define **marginal rank gain** per column (expected increase in rank under targeted e or ε).  
- C2. Greedy selection or learning-based reordering.  
- C3. Save parity_order and hash.  

**Acceptance**: compare “random vs optimized order” success(m) curve; record gain.  

---

### Stage D · Rank & Success Evaluation (Monte Carlo)
- D1. **i.i.d. erasures**: sample S (lost sources), P (received parities).  
- D2. **Gilbert–Elliott**: simulate bursts, compare with/without interleaving.  
- D3. Collect statistics:  
  - success vs e and vs m,  
  - rank histograms,  
  - average early success m.  
- D4. Compare against **URS baseline** and variants.  

**Acceptance**: reports with CI, rank plots, relative improvement.  

---

### Stage E · Finalize Tables
- E1. Write A.json, parity_order.json, meta.json.  
- E2. Compute hashes, generate table_id.  
- E3. Summarize in reports/ with applicability, improvements, URS deltas.  
- E4. Integrate into protocol (encoder signals table_id).  

**Acceptance**: decode regression passes (identity mapping at no loss), stable success curves.  

---

## 4. QA & Reproducibility
- Random seed fixed; reproducible outputs (hash stable).  
- Float64 precision; explicit tie-break.  
- URS compatibility retained (record differences).  
- Unit tests: identity check with no erasures, consistent A+order import.  

---

## 5. Compute Resource Estimate
- Example: N ∈ {32,64,128}, 4–6 K values each, ε-grid ~10 points, mc-trials=10k.  
- Expected runtime: few hours on 8-core CPU, <1h incremental.  

---

## 6. Online Integration Notes
- Encoder signals `table_id` in headers.  
- Pre-store several ε-band tables, switch by measured ε drift.  
- Interleaver depth declared in meta.json.  

---

## 7. Acceptance Criteria
- Success(e) shows **+15–40% absolute improvement** over URS baseline in critical range (e ≈ K/2).  
- Smooth performance across ε; no sharp spikes.  
- Robust across i.i.d. and GE burst models.  

---

This checklist enables you to systematically generate offline Polar FEC tables that are robust, reproducible, and directly usable in your packet-level XOR decoder.
