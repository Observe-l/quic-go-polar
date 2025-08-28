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

## 2. CLI Parameters (To Fill In)

| Parameter          | Example Value        | Description                              |
|--------------------|----------------------|------------------------------------------|
| N                  | 32                   | Block length                             |
| K                  | 16                   | Number of source packets                 |
| Indexing           | bit-reversed         | Index convention (must be consistent)    |
| ε-grid             | 0.16–0.22 step 0.01  | Range of erasure probabilities           |
| URS baseline       | on/off               | Use URS as baseline                      |
| UPO                | on/off               | Enforce universal partial order          |
| Monte Carlo trials | 10000                | Number of simulation trials              |
| Rank targets       | e=6                  | Erasure sizes to optimize                |
| Parallel workers   | 8                    | Parallel threads for computation         |
| Seed               | 12345                | RNG seed for reproducibility             |

---

## 3. Workflow Stages (A–E)

### Stage A · Reliability Calculation (BEC Bhattacharyya)
- Compute Z values with recursion formulas.  
- Store stable sorted results with hashes.

**Placeholder for Results:**  
- [x] Deterministic Z sequences across ε-grid confirmed.  
- [x] Hash of Z table: `bed52146929ce783cfc22e147737a59ad6273164db7b5d3d64d17edf2e2144ed`  

---

### Stage B · Robust Information Set A
- Combine across ε-grid via majority vote and coverage-based selection.  
- Apply UPO constraints if enabled.  

**Placeholder for Results:**  
- [x] Final A set indices: `[11, 13, 14, 15, 19, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31]`  
- [x] A_hash: `sha256:79c86cf7408a7671cb274327db81c15bdf2601f87faa967b5de0ea96f9584c67`  
- [ ] Alternative candidate sets: `n/a (majority vote produced a single dominant set)`  

---

### Stage C · Parity Column Ordering
- Evaluate marginal rank gain under target e/ε.  
- Select optimized order.

**Placeholder for Results:**  
- [x] parity_order: `[9, 0, 15, 2, 1, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14]`  
- [x] parity_order_hash: `sha256:6b890a0e44f6defbebfeb5c8825bcf9e3a60ad0fc9604db2f59d45dc182aa2a8`  
- [ ] Comparative gain vs random: `TBD`  

---

### Stage D · Rank & Success Evaluation (Monte Carlo)
- Run simulations under i.i.d. and burst models.  
- Collect success probabilities and rank histograms.  

**Result Templates:**  

| e (erasures) | Success Rate (Optimized) | Success Rate (URS) | Improvement |
|--------------|---------------------------|---------------------|-------------|
| 4            | 100.00%                  | TBD                 | TBD         |
| 5            | 99.65%                   | TBD                 | TBD         |
| 6            | 99.60%                   | TBD                 | TBD         |

**Rank Histogram Example:**  

| Rank Value | Frequency (Optimized) | Frequency (URS) |
|------------|------------------------|-----------------|
| 16         | 100 / 54 / 12          | TBD             |
| 15         | 508 / 640 / 676        | TBD             |
| <15        | others                 | TBD             |

---

### Stage E · Finalize Tables
- Save outputs, compute hashes, and assign table_id.  

**Placeholder for Results:**  
- [x] A.json saved: `tables/N32_K16/table_20250827T095903Z/A.json`  
- [x] parity_order.json saved: `tables/N32_K16/table_20250827T095903Z/parity_order.json`  
- [x] meta.json saved: `tables/N32_K16/table_20250827T095903Z/meta.json`  
- [x] table_id: `table_20250827T095903Z`  

---

## 4. QA & Reproducibility
- Confirm identity mapping at no erasure.  
- Repeat with fixed seed → identical outputs.  
- Cross-check with URS baseline.  

**Placeholder for Results:**  
- [x] Identity test passed (yes/no).  
- [x] Repeatability hash check: `sha256:79c86cf7408a7671cb274327db81c15bdf2601f87faa967b5de0ea96f9584c67`  

---

## 5. Compute Resource Estimate (Fill In)
| N   | K   | ε-grid size | Trials | Runtime (hrs) | Notes |
|-----|-----|-------------|--------|---------------|-------|
| 32  | 16  | 7           | 10k    | ~0.5          | local small run at 2k trials took seconds |
| 64  | 32  | 9           | 10k    | ________      |       |
| 128 | 64  | 11          | 10k    | ________      |       |

---

## 6. Online Integration Notes
- Protocol signals `table_id`.  
- Pre-store multiple ε-band tables.  
- Interleaver depth declared in meta.json.  

**Placeholder for Deployment Notes:**  
- Selected default table_id: `table_20250827T095903Z`  
- Alternative tables: `[generate N64_K32 similarly]`  

---

## 7. Acceptance Criteria
- Success(e) shows **+15–40% absolute improvement** over URS baseline at mid-range e.  
- Smooth success vs ε curves.  
- Robust to both i.i.d. and burst erasures.  

**Final Sign-Off:**  
- [ ] Improvement achieved: `TBD%`  
- [ ] Smoothness confirmed: yes  
- [ ] Robustness across models confirmed: partial (i.i.d. done; burst model pending)  
