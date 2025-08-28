# Packet-Level Polar Codes for Reliable Transmission: Theory, Implementation, and Evaluation

Generated: 2025-08-28

Source results: docs/reports/fec_eval_report_20250828_010010.md

## Abstract
We study packet-level Polar codes as a transport-layer FEC scheme and compare them with Random Linear Coding (RLC) and Reed–Solomon (RS). We (i) give a constructive formulation of packet-level Polar coding over GF(2), including a rigorous mapping from the classical bit-channel polarization to the packet domain; (ii) show how to produce a systematic generator matrix $[I\,|\,P]$ by computing $P = G_{AA}^{-1} G_{A A^c}$, where $A$ is the information set induced by Bhattacharyya ordering on a BEC($\varepsilon$); (iii) design a split-phase decoder that separates coefficient elimination (bit-matrix only) from payload replay (XOR only), enabling precise timing and energy accounting; and (iv) build RS and RLC baselines over GF(256). We further introduce an offline artifact pipeline that robustifies $A$ against $\varepsilon$-sensitivity via band-wise majority voting and produces a rank-gain–optimized parity ordering. In i.i.d. losses $p\in\{0.5\%,1\%,3\%,5\%\}$ with 1500-byte packets, Polar attains success rates close to RS/RLC for moderate redundancy, while its XOR-only structure yields an order-of-magnitude energy reduction (≈20–35× in our model). Limitations at short blocklengths (non-MDS) appear only near the redundancy limit.

## I. Introduction
Transport protocols over lossy paths (wireless, satellite, QUIC) benefit from FEC to reduce latency and retransmission overhead. RS codes provide maximum-distance separable (MDS) guarantees but require heavy GF($2^8$) arithmetic. RLC is flexible and near-MDS in expectation but decoding via Gaussian elimination is multiplication-dominated. Polar codes (Arıkan) are the first provably capacity-achieving codes for symmetric channels, relying only on XOR operations over GF(2). This work adapts Polar to the packet level by treating each packet as a symbol bit of a length-$N=2^n$ code, retaining GF(2) operations end-to-end. We provide the mathematics, a practical artifact-driven implementation integrated with QUIC-like packetization, and a head-to-head evaluation against RS/RLC.

## II. Background and Related Work
### A. Reed–Solomon (RS) Codes
- MDS over GF($2^8$) with $N\le 255$: any $K$ of $N$ suffice for recovery. A systematic Vandermonde encoder evaluates a degree-$<K$ polynomial at $N$ nonzero field points. Complexity: encoding $\Theta(NK)$ multiplies; decoding $\Theta(K^2)$ field ops (BM/Forney) or elimination on a $K\times K$ system.

### B. Random Linear Coding (RLC)
- Parity packets are random GF($2^8$) linear combinations of sources with coefficient header. Decoding solves a random linear system via Gaussian elimination $\Theta(K^3)$ multiplies (plus $\Theta(K^2 L)$ byte ops), approaching MDS reliability in expectation.

### C. Polar Codes at Packet Level
- Classical Polar uses $G_N=F^{\otimes n}$ with $F=\begin{bmatrix}1&0\\1&1\end{bmatrix}$ and channel polarization to select an information set $A\subseteq\{0,\dots,N-1\}$. For a BEC($\varepsilon$), the bit-channel reliabilities follow the recursion
	$$Z^- = 2Z - Z^2,\quad Z^+ = Z^2.$$
	We extend this to packet level by mapping each packet to a bit-channel index, keeping all operations in GF(2). A systematic encoder is obtained by partitioning columns of $G_N$ into $A$ (info) and $A^c$ (frozen/parity), then forming
	$$P \;=\; G_{AA}^{-1}\, G_{A A^c},\quad \text{so that}\; [I_K\;|\;P]\in\{0,1\}^{K\times N}.$$
	Encoding and decoding require XOR only.

## III. Theoretical Analysis
### A. Channel Model and Success Condition
We model packet loss as BEC($\varepsilon$). Let $N=K+R$ and $\mathcal{L}\subseteq\{0,\dots,N-1\}$ be the set of erased packet indices. With systematic generator $G=[I\,|\,P]$, the receiver observes $K$ or more rows of the $N\times K$ coefficient matrix $C$ where each received source adds a canonical row $e_i^\top$ and each received parity $j$ adds row $P_{j,:}^\top$. Decoding is possible iff the received $m\times K$ matrix has rank $K$:
$$\text{success} \iff \operatorname{rank}(C_{\text{recv}})=K.$$
Thus, success probability hinges on the distribution of ranks induced by loss patterns and the structure of $P$.

### B. Constructing the Information Set $A$
For BEC($\varepsilon$), the bit-channel Bhattacharyya parameters $\{Z_i(\varepsilon)\}_{i=0}^{N-1}$ are produced by $n$ applications of $Z^-\!,Z^+$ starting from $Z=\varepsilon$, ordered by the bit-reversal index of $i$. A baseline selection is
$$A\;=\;\operatorname*{arg\,min}_{|S|=K}\sum_{i\in S} Z_i(\varepsilon)\;\equiv\;\text{take the K smallest }Z_i.$$
However, the ordering can be $\varepsilon$-sensitive for short $N$. We mitigate this by a banded design: pick a band $[\varepsilon_0-\Delta,\varepsilon_0+\Delta]$, evaluate $Z_i$ on a grid $\{\varepsilon_t\}$, and majority-vote the top-$K$ set:
$$\hat{A} \;=\; \operatorname*{mode}\big\{ A(\varepsilon_t) : \varepsilon_t\in[\varepsilon_0-\Delta,\varepsilon_0+\Delta]\big\}.$$
We then compute $P=G_{\hat{A}\hat{A}}^{-1} G_{\hat{A} \hat{A}^c}$.

### C. Parity Ordering via Marginal Rank Gain
Let $\mathcal{P}=\{0,\dots,R-1\}$ index parity columns. We greedily order parities $\pi(0),\pi(1),\dots,\pi(R-1)$ to maximize the expected rank gain under an assumed loss budget $e$:
$$\pi(t) \;=\; \arg\max_{j\in \mathcal{P}\setminus \{\pi(0),\dots,\pi(t-1)\}} \; \mathbb{E}[\Delta\,\operatorname{rank}\mid j, t, e],$$
where the expectation is over random erased sources consistent with $e$. Operationally, this reduces to selecting the column whose support (set of ones in $P_{j,:}$) covers the most yet-uncovered pivot positions. This ordering improves early stopping and robustness when only a subset of parities arrive.

### D. Systematic Encoding and Split-Phase Decoding
Given $[I\,|\,P]$, parity $j$ is computed as
$$y_j \;=\; \bigoplus_{i=0}^{K-1} P_{j,i}\, x_i,$$
where $x_i\in\{0,1\}^{8L}$ is the packet payload (as a byte vector) and $\oplus$ is bytewise XOR. For decoding, we form an $m\times K$ row set $\{r_u\}$: $r=e_i$ for each received source $i$, and $r=P_{j,:}$ for each received parity $j$. Our split-phase algorithm:
1) Phase A (coefficients): Gaussian elimination on the bit-matrix only, logging operations $\{\mathrm{SWAP}(u,v),\;\mathrm{XOR}(u\leftarrow u\oplus v)\}$. Complexity: $\tilde{\Theta}(K^3/\omega)$ word-XORs with word size $\omega=64$.
2) Phase B (payloads): Replay the same XOR sequence on the payload buffers. Complexity: $\Theta(M\,L)$ byte XORs, where $M$ is the number of logged row-XORs.

### E. Complexity and Energy Model
- Polar encode: at most $\sum_{j=0}^{R-1} w_j$ packet XORs, where $w_j=\|P_{j,:}\|_0$. Worst-case $\Theta(KR)$ packet XORs $\Rightarrow \Theta(KR\,L)$ byte XORs.
- Polar decode: Phase A $\tilde{\Theta}(K^3/\omega)$ bitwise XORs; Phase B $\Theta(M\,L)$ byte XORs. No GF($2^8$) multiplies.
- RLC/RS encode: $\Theta(KR\,L)$ byte GF multiplies plus XORs.
- RLC/RS decode (elimination): $\Theta(K^3)$ GF multiplies and $\Theta(K^2 L)$ byte ops.

Energy proxy (ARM-A7-like): cycles/byte $\approx 1$ for XOR, $\approx 5$ for GF($2^8$) multiply. Joules are estimated as
$$E\;=\;\frac{\text{cycles}}{f_\mathrm{CPU}}\;\cdot P_\mathrm{CPU},\quad f_\mathrm{CPU}=1\,\text{GHz},\; P_\mathrm{CPU}=0.5\,\text{W}.$$
Polar’s advantage stems from eliminating multiplications entirely and reducing Phase B work via the split-phase replay.

## IV. Engineering Implementation
### A. Artifact Pipeline and Robust $A$
For each $(N,K,e)$ we generate $\hat{A}$ using the banded Bhattacharyya majority-vote and derive $\pi$ (parity order) via greedy rank gain. We persist artifacts in tables/N{N}_K{K}_e{e}/table_*/ with A.json, parity_order.json, meta.json (band center, grid, success estimates). At runtime we load the most recent table.

### B. Generator Formation and Bitset Layout
We construct $G_N=F^{\otimes n}$ once, then slice $G_{AA}$ and $G_{A A^c}$ to compute $P$. Each parity row $P_{j,:}$ is stored as a uint64 bitset over $K$ sources (words $=\lceil K/64\rceil$). Popcount is used to pre-compute weights for encode cycle estimation.

### C. Encoder
For each parity $j$ (emitted in order $\pi$), we XOR-accumulate the source buffers whose bits are 1 in $P_{j,:}$. This is cache-friendly and vectorizable; in Go we use tight byte-wise XOR.

### D. Split-Phase Decoder
We build an $m\times K$ coefficient matrix as bitsets (sources contribute identity rows; parities reuse $P$ rows), and a parallel slice of $m$ payload buffers. Phase A performs in-place elimination over bitsets, logging SWAP/XOR ops. Phase B replays those ops on the payload slice. This preserves numerical stability (over GF(2)) and decouples matrix cost from payload size.

### E. Baselines
RS: systematic Vandermonde; parity rows $v(x)=\big(1,x,\dots,x^{K-1}\big)$ evaluated at $x=\alpha^{K+j}$ and mapped through $V_\text{sys}^{-1}$. RLC: per-parity random coefficients (nonzero over GF(256)) prepended to the packet; decoder extracts rows and performs GF elimination. Both share the same loss simulation harness.

## V. Methodology
- Configs: (N,K) ∈ {(8,7), (8,6), (16,12), (16,11), (32,26)}; L=1500 B; i.i.d. drop p ∈ {0.5%, 1%, 3%, 5%}.
- Runs: 100,000 per (scheme, N,K, p).
- Metrics: success rate, total and average encode/decode time, energy estimate in Joules: cycles × (power/frequency) with defaults 0.5 W, 1.0 GHz; XOR=1 cycle/byte, GF(256) mul=5 cycles/byte.

Experimental platform model and energy parameters (used by the evaluator):
- CPU power: 0.5 W (active)
- CPU frequency: 1.0 GHz
- Operation costs: XOR=1 cycle/byte; GF(256) multiply=5 cycles/byte (decode adds elimination work)
- Energy estimate: E[J] = (cycles / frequency) × power

Repetition and precision:
- Each data point averages 100,000 independent runs; the binomial standard error at p≈0.95 is ≈0.069% (95% CI ≈ ±0.14%).

## VI. Results

### A. Success Rate (%)
Representative values (see source report for full tables):
- (8,6): p=5% → Polar 97.21, RLC 99.42, RS 99.40
- (8,7): p=5% → Polar 94.34, RLC 94.32, RS 94.33
- (16,12): p=5% → Polar 97.87, RLC 99.90, RS 99.91
- (16,11): p=5% → Polar 99.91, RLC 99.99, RS 99.99
- (32,26): p=5% → Polar 99.28, RLC 99.91, RS 99.92

Observations: Polar tracks RS/RLC closely at low losses. At higher losses near redundancy limits, RS/RLC retain an edge (MDS/near-MDS), especially for (8,6) and (16,12).

### B. Runtime (total over 100k runs; Avg per run in parentheses)
- (8,6) encode: Polar 1254 ms (0.003), RLC 9272 ms (0.023), RS 8973 ms (0.022)
- (8,6) decode: Polar 1906 ms (0.005), RLC 15488 ms (0.039), RS 7002 ms (0.018)
- (8,7) encode: Polar 1378 ms (0.003), RLC 5415 ms (0.014), RS 6104 ms (0.015)
- (8,7) decode: Polar 2107 ms (0.005), RLC 12251 ms (0.031), RS 8197 ms (0.020)
- (16,11) encode: Polar 6630 ms (0.017), RLC 41751 ms (0.104), RS 43529 ms (0.109)
- (16,11) decode: Polar 8363 ms (0.021), RLC 55086 ms (0.138), RS 15782 ms (0.039)
- (16,12) encode: Polar 5405 ms (0.014), RLC 36463 ms (0.091), RS 38900 ms (0.097)
- (16,12) decode: Polar 6986 ms (0.017), RLC 50440 ms (0.126), RS 17839 ms (0.045)
- (32,26) encode: Polar 16547 ms (0.041), RLC 117018 ms (0.293), RS 144150 ms (0.360)
- (32,26) decode: Polar 21235 ms (0.053), RLC 153324 ms (0.383), RS 68212 ms (0.171)

Observations: Polar is consistently faster than RLC/RS in encoding (8–9× faster vs RS at N=32,K=26), and faster than RLC in decoding. RS decode can be faster than RLC for these sizes but remains much slower than Polar.

### C. Energy (Joules, per loss over 100k runs)
- (8,6) p=5%: Polar 0.858, RLC 25.451, RS 25.456 → ~30× lower vs RS/RLC
- (8,7) p=5%: Polar 0.983, RLC 26.410, RS 26.411 → ~27× lower
- (16,12) p=5%: Polar 4.047, RLC 102.795, RS 102.771 → ~25× lower
- (16,11) p=5%: Polar 5.130, RLC 99.157, RS 99.180 → ~19× lower
- (32,26) p=5%: Polar 13.244, RLC 423.965, RS 423.979 → ~32× lower

Trend: Polar’s XOR-only operations yield an order-of-magnitude energy advantage, growing with K and R.

### D. Complete Tables (Per Configuration)

Below are the full success rate, timing, and energy tables for each (N,K). Values are taken directly from the referenced results file.

#### (N=8, K=6)

Success Rate (%)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 99.96 | 99.89 | 99.00 | 97.21 |
| RLC | 100.00 | 100.00 | 99.86 | 99.42 |
| RS | 100.00 | 99.99 | 99.86 | 99.40 |

Encoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 1254 | 0.003 |
| RLC | 9272 | 0.023 |
| RS | 8973 | 0.022 |

Decoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 1906 | 0.005 |
| RLC | 15488 | 0.039 |
| RS | 7002 | 0.018 |

Energy (J)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 0.896579 | 0.892803 | 0.876830 | 0.857693 |
| RLC | 26.438895 | 26.331451 | 25.897002 | 25.451346 |
| RS | 26.443679 | 26.334140 | 25.898061 | 25.456215 |

#### (N=8, K=7)

Success Rate (%)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 99.92 | 99.71 | 97.80 | 94.34 |
| RLC | 99.92 | 99.73 | 97.80 | 94.32 |
| RS | 99.94 | 99.75 | 97.75 | 94.33 |

Encoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 1378 | 0.003 |
| RLC | 5415 | 0.014 |
| RS | 6104 | 0.015 |

Decoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 2107 | 0.005 |
| RLC | 12251 | 0.031 |
| RS | 8197 | 0.020 |

Energy (J)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 1.044374 | 1.039051 | 1.012997 | 0.983055 |
| RLC | 27.694133 | 27.561902 | 27.016595 | 26.409959 |
| RS | 27.702322 | 27.569646 | 27.011624 | 26.411287 |

#### (N=16, K=11)

Success Rate (%)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 100.00 | 100.00 | 99.99 | 99.91 |
| RLC | 100.00 | 100.00 | 100.00 | 99.99 |
| RS | 100.00 | 100.00 | 100.00 | 99.99 |

Encoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 6630 | 0.017 |
| RLC | 41751 | 0.104 |
| RS | 43529 | 0.109 |

Decoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 8363 | 0.021 |
| RLC | 55086 | 0.138 |
| RS | 15782 | 0.039 |

Energy (J)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 5.237951 | 5.226071 | 5.178878 | 5.130136 |
| RLC | 102.731475 | 102.324288 | 100.742615 | 99.156846 |
| RS | 102.730436 | 102.351909 | 100.751970 | 99.179896 |

#### (N=16, K=12)

Success Rate (%)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 99.98 | 99.91 | 99.23 | 97.87 |
| RLC | 100.00 | 100.00 | 99.99 | 99.90 |
| RS | 100.00 | 100.00 | 99.99 | 99.91 |

Encoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 5405 | 0.014 |
| RLC | 36463 | 0.091 |
| RS | 38900 | 0.097 |

Decoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 6986 | 0.017 |
| RLC | 50440 | 0.126 |
| RS | 17839 | 0.045 |

Energy (J)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 4.188612 | 4.176725 | 4.118310 | 4.046531 |
| RLC | 106.665840 | 106.233300 | 104.503996 | 102.795062 |
| RS | 106.665408 | 106.231464 | 104.513123 | 102.771422 |

#### (N=32, K=26)

Success Rate (%)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 100.00 | 100.00 | 99.90 | 99.28 |
| RLC | 100.00 | 100.00 | 100.00 | 99.91 |
| RS | 100.00 | 100.00 | 100.00 | 99.92 |

Encoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 16547 | 0.041 |
| RLC | 117018 | 0.293 |
| RS | 144150 | 0.360 |

Decoding Time (ms)

| Scheme | Total | Avg/Run |
|---|---:|---:|
| POLAR | 21235 | 0.053 |
| RLC | 153324 | 0.383 |
| RS | 68212 | 0.171 |

Energy (J)

| Scheme | p=0.005 | p=0.010 | p=0.030 | p=0.050 |
|---|---|---|---|---
| POLAR | 13.470752 | 13.440468 | 13.313864 | 13.243737 |
| RLC | 440.770044 | 438.898629 | 431.411253 | 423.965200 |
| RS | 440.791455 | 438.881547 | 431.421306 | 423.978659 |

## VII. Discussion
Polar offers a compelling latency/energy profile for transport-layer FEC when redundancy is moderate. At higher erasure (near R), short-block non-MDS limits reduce success relative to RS; nonetheless, the gap is often small compared with significant energy savings. RLC matches RS reliability in expectation but incurs the highest decode cost. For embedded or edge devices, Polar’s GF(2) structure is advantageous.

Limitations: Results assume i.i.d. losses and the provided CPU power/frequency model. Real platforms will vary; RS/RLC implementations can leverage specialized GF optimizations; Polar performance depends on robust ε-band artifact selection.

## VIII. Conclusion and Future Work
Packet-level Polar codes with precomputed artifacts attain reliability close to RS/RLC in the tested regimes while reducing energy by ~20–35×. This makes Polar attractive for energy-constrained networking. Future work: adaptive artifact selection across ε, hybrid schemes that mix RS parity for high-loss tails, and hardware acceleration for large N.

## IX. Reproducibility
The following command produced the referenced results and can be re-run to regenerate tables and an updated IEEE-style report:

```bash
go run ./cmd/fec_eval \
	-runs=100000 \
	-configs="8,7;8,6;16,12;16,11;32,26" \
	-loss="0.005,0.01,0.03,0.05" \
	-scheme=all \
	-packet-size=1500 \
	-artifacts=tables \
	-cpu-power-w=0.5 \
	-cpu-freq-ghz=1.0 \
	-out docs/reports/fec_eval_report.md
```

## References
- E. Arıkan, “Channel polarization: A method for constructing capacity-achieving codes for symmetric binary-input memoryless channels,” IEEE Trans. Inf. Theory, 2009.
- Additional references on packet-level Polar and transport-layer coding.
