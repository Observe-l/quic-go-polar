# Experimental Design — Demonstrating Packet-Level BEC and Channel Polarization

**Goal.** Provide a rigorous, reproducible experimental methodology to **(i)** validate that the packet-level transport channel can be modeled as a **Binary Erasure Channel (BEC)** after appropriate interleaving, and **(ii)** empirically demonstrate **channel polarization** for packet-level Polar codes (with packets as symbols over GF(2) payloads).

---

## 1. Hypotheses and Success Criteria

### H1 (Packet-Level ≈ BEC after interleaving)
- **Hypothesis.** After applying a moderate depth interleaver across time/paths, per-block packet receptions behave as **i.i.d. erasures** with rate `ε` (or sufficiently close for engineering use).  
- **Success criteria.**
  1. The **block loss count** `E` over `N` packets follows **Binomial(N, ε̂)** within tolerance (K–S test *p* > 0.05; RMSE of PMF < 2%).  
  2. **Lag-1 autocorrelation** of the received/erased Bernoulli sequence |ρ(1)| < 0.05 (95% CI includes 0).  
  3. **Runs test** for independence fails to reject i.i.d. at 5% level.

### H2 (Channel Polarization exists at packet level)
- **Hypothesis.** For Polar transform size `N=2^n`, the **subchannel erasure probabilities** `{p_i}` under **SC decoding on a BEC** **polarize**: as `N` grows, the empirical distribution of `{p_i}` becomes **bimodal**, clustering near `{0,1}`, and the fraction of “good” subchannels approaches the **capacity** `1−ε`.  
- **Success criteria.**
  1. The **empirical** `{p_i}` (estimated by Monte Carlo) **match** BEC recursion predictions `{Z_i}` with max deviation `max_i |p_i−Z_i| < 0.02`.  
  2. **Polarization metric** `μ(N)= (1/N) Σ_i min(p_i,1−p_i)` **decreases** with `N` and drops below `0.1` for `N≥128`.  
  3. The fraction of “good” subchannels (p_i < 0.1) ≈ `1−ε` within ±3% absolute.

---

## 2. Notation and Models

- **Blocklength** `N ∈ {8, 16, 32, 64, 128}` (power of two).  
- **Kernel** `F = [[1,0],[1,1]]`, generator `G_N = F^{⊗n}` over GF(2).  
- **Packet symbol.** Each coded position carries a 1500B payload; *known* iff the packet arrives; *erased* iff dropped.  
- **BEC(ε).** Each coded position independently erased with prob. `ε`.  
- **Gilbert–Elliott (GE)** (burst alternative): 2-state Markov with `P_GB, P_BG`, emission `e_G=0, e_B=1`.  
- **Interleaver.** Depth `d ∈ {0,2,4,8}`; stride-by-block mapping across time or paths.

---

## 3. Part I — Validating the BEC Assumption

### 3.1 Data sources
- **Synthetic:** Generate erasures from (i) BEC(ε) with ε ∈ {0.5%, 1%, 3%, 5%} and (ii) GE with multiple parameter sets causing mild/moderate bursts.  
- **Live/Trace (optional):** Capture packet delivery logs (ACK/NACK or Rx/Timestamp) over real links; derive per-block erasure bitmaps before/after interleaving.

### 3.2 Procedure (per (N, ε) and per interleaver depth d)
1. **Interleave**: Apply interleaver of depth `d` to the raw sequence.  
2. **Count distribution**: For 10k blocks, compute `E` (erased count). Fit **Binomial(N, ε̂)** where `ε̂` is the sample mean.  
3. **Goodness-of-fit tests**:  
   - **K–S** on cumulative distribution of `E` (discrete variant), report *p*-value.  
   - **RMSE** between empirical PMF of `E` and Binomial(N,ε̂).  
4. **Independence tests**:  
   - **Lag-k autocorrelation** on the interleaved Bernoulli sequence, `k=1..4`.  
   - **Runs test** (Wald–Wolfowitz) for randomness.  
5. **Acceptance**: Declare BEC-valid if all success criteria in §1 for H1 are met.

### 3.3 Outputs
- Table: for each `(N, ε, d)`, report ε̂, K–S *p*, PMF-RMSE, ρ(1), runs-test *p*.  
- Plots: PMF overlay (empirical vs Binomial), ACF bars, before/after interleaving comparison.

---

## 4. Part II — Demonstrating Polarization (BEC, SC-decoding)

### 4.1 Subchannel erasure probability via Monte Carlo
For each `(N, ε)`:
1. Construct `G_N`.  
2. For trial `t=1..T` (e.g., `T=100k`):
   - Draw erasure pattern `e ∈ {0,1}^N` i.i.d. Ber(ε) (`1=erased`).  
   - **BEC-SC knownness propagation** (no payload values needed): determine for each subchannel `i` whether `u_i` is **decodable** from the non-erased coded positions and previously decoded `u_1..u_{i-1}`.  
   - Record `known_i ∈ {0,1}`.  
3. Estimate `p_i = 1 − Pr[known_i]` for each `i`.

> **Why this works:** Over a BEC, SC decoding reduces to **logic on known/erased states** (no soft values). At the base kernel level `(x1, x2) = (u1 ⊕ u2, u2)`, we have:  
> - `u1` known **iff** `x1` and `x2` are both known → `Z^- = 2ε − ε^2`.  
> - `u2` known **iff** `x2` known **or** (`x1` known and `u1` known) → `Z^+ = ε^2`.  
> Recursive application over the FFT-like graph yields SC knownness without touching payload bytes.

### 4.2 BEC-SC knownness pseudo-code
```text
function SC_BEC_knownness(mask[0..N-1]):  # mask[j]=1 if coded pos j is KNOWN, 0 if ERASED
  # Work on the polar decoding tree, bottom-up + top-down passes.
  # Represent messages as {K, E} (Known or Erased).

  # Bottom step for N=2: given (x1,x2), 
  #   u1_known = (x1==K and x2==K)
  #   u2_known = (x2==K) or ((x1==K) and u1_known)

  # For N=2^n: recursively combine using f/g rules specialized to BEC:
  #   f(K,E) = E, f(E,K) = E, f(K,K) = K, f(E,E) = E    # for u^- (AND-like)
  #   g(u1_known, a, b) = K if (b==K) or (a==K and u1_known==K) else E  # for u^+ (OR-like)
  # Return array known_u[0..N-1].
```

### 4.3 Theoretical comparison
- Compute **theoretical** `Z_i(ε)` via BEC recursion (exact, float64).  
- Compare `p_i` vs `Z_i`: report `max_i |p_i−Z_i|`, `mean |p_i−Z_i|`, and correlation `ρ`.  
- Plot **histogram of `{p_i}`** for `N ∈ {8,16,32,64,128}` to show **bimodalization** as `N` grows.  
- Compute **polarization metric** `μ(N)=(1/N) Σ_i min(p_i,1−p_i)` and show monotone decrease with `N`.

### 4.4 Capacity check
- For each `(N, ε)`, count fraction `φ = (1/N)·|{i : p_i < τ}|`, with `τ∈{0.1,0.05}`.  
- Verify `φ ≈ 1−ε` within ±3% absolute (capacity of BEC).

### 4.5 Optional: Link to our engineering decoder
- Using the same `(N, ε)` and chosen `A` (top-K by smallest `p_i` or `Z_i`), run **binary linear decoder** (Gaussian elimination) over the same erasure patterns, and compare **block success rate** with the **SC capacity prediction** (they should be consistent up to small gaps at short `N`).

---

## 5. Experimental Parameters and Sample Size

- `N ∈ {8,16,32,64,128}`, `ε ∈ {0.5%, 1%, 3%, 5%}`.  
- Trials per `(N,ε)` for Monte Carlo: `T = 100,000`.  
  - 95% CI on each `p_i` is ≤ ±0.005 by Hoeffding’s bound.  
- Interleaver depths for Part I: `d ∈ {0,2,4,8}`.  
- GE models (for robustness): choose `{P_GB, P_BG}` to yield same mean ε with low/moderate burstiness.

---

## 6. Implementation Notes

- **Symbol abstraction.** For Part II, only **known/erased flags** of coded positions are needed; payload bytes are not used.  
- **Indexing.** Fix **bit-reversed or natural** indexing globally; use the same in `G_N` and in plotting `{p_i}` vs index.  
- **Determinism.** Fix RNG seed; log hashes of `Z_i(ε)` arrays and code versions.  
- **Performance.** Precompute the polar tree structure and operate on bitsets for knownness; complexity per trial `O(N log N)` boolean ops.  
- **Trace validation.** When using live data, normalize per-block by interleaver window; estimate ε̂ from observed delivery ratio.

---

## 7. Statistical Tests and Acceptance

- **BEC validity (Part I):**
  - K–S test *p* > 0.05; PMF-RMSE < 2%; |ρ(1)| < 0.05; runs-test *p* > 0.05.  
  - If GE (d=0) fails, increase interleaver depth; accept once criteria met for some `d≤8`.

- **Polarization (Part II):**
  - `max_i |p_i−Z_i| < 0.02` and `μ(N)` decreasing with `N`, with `μ(128) < 0.1`.  
  - Capacity fraction `φ` within ±3% of `1−ε` at `τ=0.1`.

---

## 8. Deliverables

1. **BEC Validation Report** (per `(N, ε, d)`): ε̂, K–S *p*, PMF-RMSE, ACF(1..4), runs-test *p*, plots (PMF overlay, ACF bars).  
2. **Polarization Report** (per `(N, ε)`): arrays `{p_i}`, `{Z_i}`, deviation stats, histograms, `μ(N)` curve, capacity fraction `φ`.  
3. **Reproducibility bundle:** source code, seeds, version hashes, and configuration files.  
4. **Executive summary:** A one-page takeaway: minimal `d` to achieve BEC-like behavior; evidence of polarization; practical implication for table design.

---

## 9. Appendix — Practical SC Knownness Rules for BEC

At the 2×2 kernel `(x1, x2) = (u1 ⊕ u2, u2)`:
- `u1` is **known** iff **both** `x1` and `x2` are known. → erasure prob `Z^- = 1 − (1−ε)^2 = 2ε − ε^2`.  
- `u2` is **known** iff `x2` known **or** (`x1` known **and** `u1` known). → erasure prob `Z^+ = ε^2`.  

Recursively apply these rules over the polar tree (FFT-like butterfly). This yields SC knownness decisions using only **boolean** messages `{Known, Erased}` — perfectly aligned with a packet-level BEC view.

---

**Version:** v1.0 — Experimental Design for Packet-Level BEC & Polarization


---

## 10. Required Outputs (Artifacts & Data to Submit)

### 10.1 Part I — BEC Validation Artifacts (per (N, ε, interleaver depth d))
- **Tables (CSV + in-report table):**
  - `bec_validation_summary.csv` with columns: `N, epsilon, d, eps_hat, KS_p, PMF_RMSE, ACF_lag1, ACF_lag2, ACF_lag3, ACF_lag4, Runs_p`.
- **Plots:**
  - **PMF overlay**: Empirical `P(E=e)` vs Binomial(N, ε̂) for `e=0..N` (PNG/SVG): `pmf_overlay_N{N}_eps{ε}_d{d}.png`.
  - **ACF bars**: Autocorrelation of the interleaved Bernoulli sequence (lags 1..4): `acf_N{N}_eps{ε}_d{d}.png`.
  - **Before/After Interleaving** comparison panel: `bec_interleaver_effect_N{N}_eps{ε}.png`.

**Placeholders to fill in (report table):**
| N | ε | d | ε̂ | KS-p | PMF-RMSE | ρ(1) | Runs-p |
|---|---|---|----|------|----------|------|--------|
| 8 | 1% | 0 | 0.997% | 1.000 | 0.01% | -0.000 | 0.858 |
| 8 | 1% | 4 | 1.011% | 1.000 | 0.01% | -0.000 | 0.842 |
| … | …  | … | ____ | ____ | ____ | ____ | ____ |

---

### 10.2 Part II — Polarization Artifacts (per (N, ε))

- **Arrays (CSV/NPZ):**
  - `p_i` (empirical subchannel erasure probs), `Z_i` (theoretical BEC recursion), and `delta_i = p_i - Z_i`:
    - File: `polar_subchannels_N{N}_eps{ε}.csv` with columns `i, p_i, Z_i, delta_i`.

- **Figures:**
  1. **Polarization histograms** of `{p_i}` for each `N`:
     - `hist_polar_N8_eps{ε}.png`, `hist_polar_N16_eps{ε}.png`, …, `hist_polar_N128_eps{ε}.png`.
  2. **μ(N) curve** across `N ∈ {8,16,32,64,128}` (per ε):
     - `mu_curve_eps{ε}.png` (y-axis μ(N) = (1/N) Σ min(p_i, 1−p_i)).
  3. **p_i vs Z_i scatter** with y=x reference:
     - `scatter_pi_vs_Zi_N{N}_eps{ε}.png`.
  4. **CDF of p_i** to visualize bimodalization:
     - `cdf_pi_N{N}_eps{ε}.png`.
  5. **Capacity consistency plot:** fraction `φ(τ)` vs τ ∈ [0,0.5] (mark τ=0.1, 0.05), overlay line at `1−ε`:
     - `capacity_fraction_N{N}_eps{ε}.png`.

- **Summary Tables (in-report + CSV):**
  - `polarization_metrics.csv` with columns: `N, epsilon, muN, muN_CI, max_abs_delta, mean_abs_delta, corr_pZ, phi_tau_0.1, phi_tau_0.05`.

**Placeholders to fill in (report tables):**
- **Deviation & correlation per (N, ε):**

| $N$ | $ε$ | $max\|p_i−Z_i\|$ | $mean\|p_i−Z_i\|$ | $corr(p,Z)$ | $μ(N)$ | $φ(0.1)$ | $φ(0.05)$ |
|-----|-----|------------------|-------------------|-------------|--------|----------|-----------|
| 8   | 1% | 0.0000 | 0.0000 | 1.000 | 0.0100 | 1.000 | 0.875 |
| 16  | 1% | 0.0000 | 0.0000 | 1.000 | 0.0100 | 0.938 | 0.938 |
| 32  | 1% | 0.0000 | 0.0000 | 1.000 | 0.0100 | 0.969 | 0.969 |
| 64  | 1% | 0.0000 | 0.0000 | 1.000 | 0.0100 | 0.984 | 0.969 |
| 128 | 1% | 0.0000 | 0.0000 | 1.000 | 0.0065 | 0.977 | 0.969 |

- **μ(N) monotonicity check (per ε):**

| $ε$ | $μ(8)$ | $μ(16)$ | $μ(32)$ | $μ(64)$ | $μ(128)$ | Monotone? |
|-----|--------|---------|---------|---------|----------|-----------|
| 0.5% | 0.0050 | 0.0050 | 0.0050 | 0.0050 | 0.0050 | yes |
| 1%   | 0.0100 | 0.0100 | 0.0100 | 0.0100 | 0.0065 | yes |
| 3%   | 0.0300 | 0.0300 | 0.0223 | 0.0188 | 0.0169 | yes |
| 5%   | 0.0500 | 0.0425 | 0.0309 | 0.0300 | 0.0235 | yes |

- **Capacity fraction check (target 1−ε):**

| N | $ε$ | $φ(0.1)$ | $\|φ(0.1) − (1−ε)\|$ | $φ(0.05)$ | $\|φ(0.05) − (1−ε)\|$ |
|---|-----|----------|----------------------|-----------|-----------------------|
| 8   | 1% | 1.000 | 0.000 | 0.875 | 0.115 |
| 16  | 1% | 0.938 | 0.072 | 0.938 | 0.072 |
| 32  | 1% | 0.969 | 0.041 | 0.969 | 0.041 |
| 64  | 1% | 0.984 | 0.026 | 0.969 | 0.041 |
| 128 | 1% | 0.977 | 0.033 | 0.969 | 0.041 |

---

### 10.3 Cross-Link (Optional) — Block Success vs SC Prediction
- **Figure:** Block success probability vs added parity `m` under the same `(N, ε)`, comparing:
  - **Binary linear decoder** (Gaussian elimination on coefficient matrix),
  - **SC capacity prediction** (good subchannels count K ≈ (1−ε)N).  
  - File: `block_success_vs_sc_N{N}_eps{ε}.png`.
- **Table:** `block_vs_sc.csv` with columns `N, epsilon, m, success_linear, success_sc_proxy`.

---

### 10.4 Reproducibility Checklist
- All scripts, fixed RNG seeds, and code hashes.  
- Raw CSV/NPZ data for `{p_i}`, `{Z_i}`, metrics tables.  
- All figures (PNG/SVG).  
- Markdown/HTML report that embeds the tables/figures above.

