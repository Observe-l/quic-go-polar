# Polar Encoder and Decoder for Packet-Level FEC

Date: 2025-08-25

This note summarizes the encoder and decoder we implemented for a binary polar-style code with packet-level bit interleaving. It focuses on the core theory, the practical packet-loss mechanism, performance optimizations, and complexity, with comparisons to common packet-level FEC schemes.

## Symbols

| Symbol | Meaning |
|---|---|
| $n$ | Polar exponent (here $n=10$) |
| $N$ | Code length, $N = 2^n = 1024$ bits per codeword |
| $\mathsf{F}$ | Kernel matrix $\begin{bmatrix}1 & 0\\ 1 & 1\end{bmatrix}$ |
| $\mathsf{G}_N$ | Polar transform $\mathsf{F}^{\otimes n}$ (Kronecker power) |
| $\mathcal{I}$ | Information set (indices of reliable positions), from reliability sequence |
| $Q$ | Code rate, $Q = |\mathcal{I}|/N$ (fraction of information bits per codeword) |
| $\mathbf{u}\in\{0,1\}^N$ | Information vector (bits placed at indices in $\mathcal{I}$) |
| $\mathbf{x}\in\{0,1\}^N$ | Encoded codeword, $\mathbf{x} = \mathbf{u}\,\mathsf{G}_N$ |
| $\pi$ | Bit-level interleaver (permutation of $[0,\dots,N-1]$), called `randomMap` |
| $K$ | Number of packets per batch (power of two; e.g., $K=32$) |
| $S = N/K$ | Bits per packet subset of a codeword (per subset) |
| $\mathbf{P}_s$ | Packet-$s$ byte stream carrying subsets of multiple codewords |
| $\mathbf{A}$ | Square system matrix for erasure decoding (selected rows of $\mathsf{G}_N$ at $\mathcal{I}$) |
| $\mathbf{B}$ | RHS matrix built directly from received packets |
| $M$ | Loss mask over packets (bitmask of missing packets) |
| $W$ | Machine word size in bits for packed operations (e.g., $W=64$ on x86_64/ARM64) |
| $T$ | Number of codewords processed in one batch |
| $R$ | Number of parity packets (when applicable in other FECs) |
| $B$ | Total source data size (bits) compared across schemes |
| $S_{\text{pkt}}$ | Payload bits per packet/symbol for packet-level FEC (RS/XOR/RLC) |

---

## 1. Encoder

### 1.1 Theory

We use the binary polar transform with kernel $\mathsf{F} = \begin{bmatrix}1 & 0\\ 1 & 1\end{bmatrix}$ and generator
$\mathsf{G}_N = \mathsf{F}^{\otimes n}$. Given an information set $\mathcal{I}$ (with $|\mathcal{I}|=Q\,N$) obtained from a reliability sequence, we form $\mathbf{u}\in\{0,1\}^N$ by placing the message bits at indices $\mathcal{I}$ and zeros elsewhere. The encoded codeword is
$$
\mathbf{x} = \mathbf{u}\,\mathsf{G}_N \in \{0,1\}^N.
$$
In practice, our reliability sequence is specified in the $\mathsf{B}_N\mathsf{F}^{\otimes n}$ domain; we align it to our $\mathsf{F}^{\otimes n}$ encoder by bit-reversing the indices (denoted $\mathrm{bitrev}_n(\cdot)$).

For multiple codewords in a batch, we repeat this mapping. When no losses occur, a second application of $\mathsf{F}^{\otimes n}$ (the self-inverse butterfly) recovers $\mathbf{u}$ exactly.

**Column-XOR equivalence.** Over GF(2), matrix-vector multiply can be written as an XOR of selected columns. Let $\mathsf{G}_N = [\mathbf{g}_0\;\mathbf{g}_1\;\dots\;\mathbf{g}_{N-1}]$ with columns $\mathbf{g}_j$. Then
$$
\mathbf{x} = \mathbf{u}\,\mathsf{G}_N = \bigoplus_{j=0}^{N-1} u_j\,\mathbf{g}_j = \bigoplus_{j\in\mathcal{I}} u_j\,\mathbf{g}_j,
$$
where $\oplus$ denotes bitwise XOR and $u_j\in\{0,1\}$. Thus, precomputing packed columns $\{\mathbf{g}_j\}_{j\in\mathcal{I}}$ and XORing those with $u_j=1$ produces exactly the polar codeword. Our fused encoder further maps these contributions through $\pi$ and straight into packet bytes.

### 1.2 Packet-level interleaving and loss model

We apply a fixed bit-level permutation $\pi$ (the `randomMap`) to each codeword and split the permuted codeword into $K$ equal-sized subsets of $S=N/K$ bits. Subset $s\in\{0,\dots,K-1\}$ of codeword $t$ occupies a contiguous byte-range in packet $\mathbf{P}_s$ at offset proportional to $t$:
$$
\text{subset}_s(t) = \big(\pi(\mathbf{x}^{(t)})\big)[\,sS : (s+1)S\,),\quad \mathbf{P}_s[\,t\cdot S : (t+1)S\,) \mathrel{\widehat{=}} \text{subset}_s(t),
$$
where $\widehat{=}$ denotes bitwise packing to bytes. This spreads every codeword across all packets; dropping a subset of packets corresponds to erasures of known rows in the transform domain.

The interleaver is persisted to disk so the mapping remains stable across runs, enabling cache hits for precomputed artifacts.

### 1.3 Fused encode-to-packets (performance)

Naïvely, one could materialize each codeword $\mathbf{x}$ and then copy bits into packets. We instead fuse encoding and packet assembly:
 - Precompute a packet-level look-up table (LUT) $\mathcal{L}[b_y, v]$ for each message byte position $b_y$ and byte value $v\in[0,255]$. Each LUT entry is a 128-byte vector (for $N=1024$) representing the XOR contribution across all $K$ subsets in permutation order.
 - Encoding a batch becomes: for each message byte, XOR the corresponding LUT row directly into the $K$ packet buffers at the codeword’s offset. We never materialize $\mathbf{x}$.

We persist the LUT to disk keyed by $(n, |\mathcal{I}|, K)$ (equivalently $(n, Q, K)$ since $|\mathcal{I}|=Q\,N$) and a CRC of $\pi$, so warm runs skip heavy precomputation.

### 1.4 Implementation techniques

- Packed bit operations: generator columns are packed into `uint64` words; encoding reduces to word-wise XORs.
- Interleave plan: for the non-fused path, a compact per-destination-byte plan avoids per-bit work.
- Deterministic data layout: stable $\pi$ and fixed packet byte layouts allow aggressive caching and fast inner loops.
- Persistence: `randomMap` and the packet LUT are saved/loaded to amortize setup cost.

### 1.5 Complexity

Recall $W$ denotes the machine word size in bits (typically $W=64$). When bitsets are packed into $\lceil N/W\rceil$ words, operations scale with $N/W$ rather than $N$.
- Baseline encode via transform: $\Theta(N\log N)$ boolean operations per codeword (butterfly).
- Column-XOR encode: $\Theta\!\big((Q\,N)\cdot N/W\big) = \Theta\!\big(Q\,N^2/W\big)$ word XORs per codeword.
- Interleaving (naïve): $\Theta(N)$ bit moves per codeword; with a byte-wise plan, $\Theta(N/8)$ byte ops.
- Fused encode-to-packets (ours): per codeword, $\Theta\!\big(((Q\,N)/8)\cdot (N/K)/8 \cdot K\big) = \Theta\!\big( Q\,N^2 / 64 \big)$ byte XORs, but with a small constant because $(N/K)/8$ is tiny (e.g., 4 bytes for $N{=}1024,K{=}32$). No intermediate codeword is materialized.

Empirically (single thread, $N{=}1024$, $K{=}32$, $Q{=}1/8$ i.e., 128 info bits), total encode time is below decode time after fusion and persistence.

---

## 2. Decoder

### 2.1 Unified fast path (loss or no-loss)

We route both loss and no-loss cases through the packed algebraic path for speed and a consistent performance profile. Conceptually, the no-loss case could apply the self-inverse butterfly on deinterleaved codewords ($\Theta(N\log N)$ per codeword). In practice, building the RHS directly from packets and applying the cached inverse achieves lower latency and avoids materializing codewords.

### 2.2 Erasure model and algebraic recovery

Let observed packets be indexed by $\mathcal{S}_\text{obs}\subseteq\{0,\dots,K-1\}$; missing packets are known erasures. Each received subset reveals a known row of $\mathbf{x}$ in the permuted domain. Using the inverse permutation $\pi^{-1}$, we map received subsets to a known-row set $\mathcal{R} \subseteq [0,\dots,N-1]$.

Let $\mathcal{I}$ index the $Q\,N$ columns of interest in $\mathsf{G}_N$. We select a deterministic set of $Q\,N$ row indices $\mathcal{R}_\star\subseteq\mathcal{R}$ that yield a full-rank square matrix
$$
\mathbf{A} \stackrel{\mathrm{def}}{=} \mathsf{G}_N[\mathcal{R}_\star,\,\mathcal{I}] \in \{0,1\}^{(Q\,N)\times (Q\,N)}.
$$
We then form the RHS matrix $\mathbf{B}$ by reading bits directly from packets at the appropriate offsets (no intermediate codeword assembly):
$$
\mathbf{B}[i, t] = \big(\text{subset of packet determined by } r_i \in \mathcal{R}_\star\big)[\,t\,],\quad t=0,\dots,T{-}1,
$$
where $T$ is the number of codewords in the batch and $r_i$ is the $i$-th selected row. The information bits for the whole batch follow from the batched solve
$$
\mathbf{U}^\top = \mathbf{A}^{-1}\,\mathbf{B}^\top,\quad \mathbf{U}\in\{0,1\}^{T\times (Q\,N)}.
$$
All arithmetic is over GF(2) and is implemented with packed `uint64` words. We cache $(\mathbf{A}^{-1},\mathcal{R}_\star)$ keyed by (loss mask $M$, $Q\,N$) to make latency invariant to the exact number of erasures.

### 2.3 Implementation techniques

- Packed Gaussian elimination to invert $\mathbf{A}$ once per loss mask (or warm-load from cache). Complexity $\tilde{\Theta}((Q\,N)^3/W)$ bit ops.
- Deterministic basis selection over received rows using packed columns from $\mathsf{G}_N$ ensures stability across drop rates.
- Direct RHS synthesis from packets avoids deinterleaving and intermediate codeword storage.
- Batched multiply-accumulate over GF(2) for $\mathbf{A}^{-1}\mathbf{B}$ amortizes work across codewords.

*Cold vs. warm inversion.* "Cold inversion" refers to the first-time computation of $\mathbf{A}^{-1}$ for a particular loss mask $M$ (and rate $Q$, i.e., dimension $Q\,N$), performed via packed Gaussian elimination. We then cache both the selected row indices $\mathcal{R}_\star$ and $\mathbf{A}^{-1}$ in memory. Subsequent batches that encounter the same mask use a "warm" path that reuses the cached inverse at $O(1)$ setup cost (decode then dominated by the batched multiply). In this implementation we do not persist the inverse to disk; only the interleaver map and packet-level LUT are persisted.

### 2.4 Complexity

Let $T$ be the number of codewords in the batch.
- Baseline (naïve) erasure recovery: deinterleave and materialize each codeword $\Rightarrow \Theta(T\cdot N)$ bit ops, then solve per codeword $\Theta((Q\,N)^2)$ with a precomputed inverse, or $\Theta((Q\,N)^3)$ without reuse.
- Our optimized pipeline:
  - Inversion (cold): $\tilde{\Theta}((Q\,N)^3/W)$ once per loss mask.
  - Build $\mathbf{B}$: $\Theta(T\cdot Q\,N\cdot S / W)$ word ops by reading packet bytes directly at computed offsets.
  - Batched multiply: $\Theta(T\cdot (Q\,N)^2 / W)$ word XORs.

Since $S=N/K$ is small (e.g., $32$ bits per subset byte-block for $N{=}1024,K{=}32$), RHS construction is inexpensive; the batched multiply dominates and scales linearly with $T$.

---

## 3. Complexity comparison with common packet-level FEC

We compare encoder/decoder leading-order costs using the same symbols: $N$ (bits/codeword), $K$ (packets), $S=N/K$ (bits per subset), $Q$ (code rate), $T$ (codewords per batch), $R$ (parity packets, where applicable), and $W$ (machine word size; packed GF(2) operations process $W$ bits at once).

| Scheme | Encoder complexity | Decoder complexity | Notes |
|---|---|---|---|
| Polar+interleave (ours), fused LUT | $\Theta\!\big(Q\,N^2/W\big)$ per codeword; no materialized codeword | Cold: $\tilde{\Theta}((Q\,N)^3/W)$ once per loss mask. Per batch: $\Theta\!\big(T\,(Q\,N)^2/W + T\,Q\,N\,S/W\big)$ | Inverse cached by (mask,$Q\,N$). RHS built directly from packets. |
| Polar (naïve) + interleave | $\Theta(N\log N)$ per codeword + interleave | No-loss: $\Theta(N\log N)$ per codeword. Erasures: as above after deinterleave | Higher memory traffic; slower in practice. |
| RS over GF($2^m$) | $\Theta(K\,R)$ finite-field ops per block | Erasures: typically $\Theta(K^2)$ per block (field ops); FFT-like speedups exist | MDS; coefficients over GF($2^m$); higher per-byte CPU. |
| XOR parity (R=1) | $\Theta(K)$ XORs per block | Recover 1 loss: $\Theta(K)$ XORs | Very low cost; limited recovery to $R$. |
| XOR parity (general R) | $\Theta(K\,R)$ XORs per block | Recover up to $R$ losses: $\Theta(K\,R)$ | Scales linearly; quickly grows for large $R$. |
| RLC over GF(2) | $\Theta(K\,R/W)$ word ops per block | Gaussian elim $\tilde{\Theta}(K^3/W)$; amortize with batching | Requires coding vectors; random rank can fluctuate. |
| RLC over GF(256) | $\Theta(K\,R)$ byte-field ops per block | $\tilde{\Theta}(K^3)$ field ops | Strong erasure recovery; higher arithmetic cost. |

### Advantages and disadvantages

- Polar+interleave (ours)
  - Advantages: pure GF(2) packed ops; no per-packet coefficients; caches keyed by loss mask; fused encoder minimizes memory traffic; predictable latency across drop rates.
  - Disadvantages: not MDS; recovery depends on rank of selected rows; parameters tuned to $N=1024$ in this implementation.
- RS codes
  - Advantages: MDS; optimal erasure correction for given parity.
  - Disadvantages: finite-field arithmetic overhead; coefficient management; can be CPU-heavy at high rates.
- XOR parity
  - Advantages: minimal CPU cost, trivial implementation.
  - Disadvantages: very limited erasure tolerance (at most $R$ losses). Large $R$ becomes bandwidth-expensive.
- RLC (GF(2)/GF(256))
  - Advantages: flexible, near-MDS performance with sufficient field size; robust to patterns.
  - Disadvantages: transmit coding vectors; decoding via elimination is $\tilde{\Theta}(K^3)$ (or $/W$ in GF(2)); rank variability.

### When which scheme is preferable

- Low loss budgets (e.g., 1 loss) and tight CPU limits: XOR parity.
- High optimality requirements with moderate $K$: RS codes.
- Large, dynamic sets with unpredictable patterns and sufficient CPU: RLC.
- Throughput-oriented binary pipelines with stable layout and strong caching: our Polar+interleave method tends to outperform in CPU efficiency while sustaining double-digit erasures (e.g., up to 20/32 observed), provided the selected rows remain full-rank.

### Equal data size and code rate: normalized comparison

Assume all schemes protect the same source size $B$ (bits) at the same code rate $Q\in(0,1)$. For RS/XOR/RLC, let $K_{\text{src}} = B / S_{\text{pkt}}$ be the number of source packets per block, so the total symbols is $M = K_{\text{src}}/Q$ and parity $R = M - K_{\text{src}} = \tfrac{1-Q}{Q}K_{\text{src}}$.

- Polar+interleave (ours): choose $T = B/(Q\,N)$ codewords ($|\mathcal{I}|=Q\,N$).
  - Encode total: $T\cdot \Theta\!\big(Q\,N^2/W\big) = \Theta\!\big( (B/(Q\,N))\cdot (Q\,N^2/W) \big) = \Theta\!\big( (N/W)\,B \big)$.
  - Decode total: $\Theta\!\big( T\,(Q\,N)^2/W \big) = \Theta\!\big( (B/(Q\,N))\cdot (Q\,N)^2/W \big) = \Theta\!\big( (Q\,N/W)\,B \big)$, plus a one-off cold inversion $\tilde{\Theta}((Q\,N)^3/W)$ per loss mask.
  - Both scale linearly in $B$ (constants depend on $N,W,Q$).
- RS (typical quadratic decoders):
  - Encode: $\Theta(K_{\text{src}}\,R) = \Theta\!\big( ((1-Q)/Q)\,K_{\text{src}}^2 \big) = \Theta\!\big( ((1-Q)/Q)\,(B/S_{\text{pkt}})^2 \big)$.
  - Decode: $\Theta(K_{\text{src}}^{\,2}) = \Theta\!\big( (B/S_{\text{pkt}})^2 \big)$ (FFT-accelerated variants can be closer to near-linearithmic but are complex and field-dependent).
- XOR parity:
  - Encode: $\Theta(K_{\text{src}}\,R) = \Theta\!\big( ((1-Q)/Q)\,(B/S_{\text{pkt}}) \big)$ (linear).
  - Decode: to recover up to $R$ erasures, $\Theta(K_{\text{src}}\,R)$ in worst case; for single-loss ($R{=}1$), $\Theta(B/S_{\text{pkt}})$.
  - Complexity is low but erasure capability limited to $R$.
- RLC over GF(2) or GF(256):
  - Encode: $\Theta(K_{\text{src}}\,R)$ (GF(2) can be packed to $/W$ word ops).
  - Decode: Gaussian elimination $\tilde{\Theta}(M^3)$ with $M\!=\!K_{\text{src}}/Q$, i.e., $\tilde{\Theta}\!\big((B/(Q\,S_{\text{pkt}}))^3\big)$; packing reduces bit-level constants but remains cubic.

Conclusion (complexity): For fixed $Q$ and large $B$, our polar+interleave pipeline has linear $\Theta(B)$ complexity (with small constants for fixed $N$), versus typical RS decoders at $\Theta((B/S_{\text{pkt}})^2)$ and RLC at $\tilde{\Theta}((B/S_{\text{pkt}})^3)$. XOR parity is also linear but cannot match strong erasure budgets without large $R$ (increasing bandwidth and constants). Hence, in terms of computational complexity under equal rate and size, our method is preferable among strong erasure-correction schemes.

*Note on precomputation:* The above "cold" term denotes on-demand precomputation of $\mathbf{A}^{-1}$ the first time a new loss mask is observed at a given code rate $Q$ (equivalently matrix size $Q\,N$). Precomputing $\mathbf{A}^{-1}$ for all masks is not practical (exponential in $K$), but the on-demand cache makes repeated patterns effectively amortized to the warm path.

Clarification on SC decoding: we do not use an SC decoder. Both loss and no-loss cases use the packed GF(2) linear solver with cached inverses; the self-inverse transform is an equivalent alternative in the no-loss case but is not used in our fast path.

### Numerical analysis (MTU 1500 B, choose $S_{\text{pkt}}{=}1024$ B)

Assumptions for a concrete rule-of-thumb comparison under equal data size and code rate:
- Packet payload cap: $S_{\text{pkt}} \le 1500$ B (typical MTU). We fix $S_{\text{pkt}} = 1024$ B $= 8192$ bits.
- Machine word size: $W = 64$.
- We compare total decode cost (dominant in practice) and give encode notes where helpful.

Our method (polar+interleave, unified algebraic decoder) has total decode cost $\Theta\big((Q\,N/W)\,B\big)$ plus a one-off cold inversion $\tilde{\Theta}((Q\,N)^3/W)$ per loss mask. Competing schemes:
- RS (typical quadratic decoders): $\Theta\!\big((B/S_{\text{pkt}})^2\big)$.
- RLC (Gaussian elimination): $\tilde{\Theta}\!\big((B/(Q\,S_{\text{pkt}}))^3\big)$ over GF(2) or GF(256) (different constants, same cubic scaling).

Set leading orders equal to find cross-over sizes $B_\star$ beyond which our method is asymptotically cheaper. Ignoring constant factors (focus on growth and parameters):

1) Against RS decode
$$
\frac{Q\,N}{W}\,B \;\approx\; \Big(\frac{B}{S_{\text{pkt}}}\Big)^{\!2}
\;\Rightarrow\; B_\star \;\approx\; \frac{Q\,N}{W}\,S_{\text{pkt}}^{\,2}.
$$
With $W{=}64$ and $S_{\text{pkt}}{=}8192$ bits,
$$
B_\star \;\approx\; (Q\,N)\,2^{20}\;\text{bits}\;=\; (Q\,N)\,2^{17}\;\text{bytes}.
$$
- Example $N{=}1024$: $B_\star \approx 128\,\text{MiB}\cdot Q$. For $Q{=}1/8$, $B_\star\approx 16\,\text{MiB}$.

2) Against RLC decode
$$
\frac{Q\,N}{W}\,B \;\approx\; \Big(\frac{B}{Q\,S_{\text{pkt}}}\Big)^{\!3}
\;\Rightarrow\; B_\star \;\approx\; Q^{2}\,\sqrt{\tfrac{N}{W}}\;S_{\text{pkt}}^{\,3/2}.
$$
With $W{=}64$ and $S_{\text{pkt}}{=}8192$ bits,
$$
B_\star \;\approx\; Q^{2}\,\sqrt{\tfrac{N}{64}}\;\underbrace{(8192)^{3/2}}_{\approx\,7.41\times10^5}\;\text{bits}
\;=\; Q^{2}\,\sqrt{\tfrac{N}{64}}\;\underbrace{\approx\,9.27\times10^4}_{(8192)^{3/2}/8}\;\text{bytes}.
$$
- Example $N{=}1024$: $\sqrt{N/64}{=}4 \Rightarrow B_\star\approx 3.71\times10^5\,Q^2\,\text{bytes}$. For $Q{=}1/8$, $B_\star\approx 5.8\,\text{KiB}$.

3) Amortizing the cold inversion
$$
	ilde{\Theta}\big((Q\,N)^3/W\big) \ll \Theta\big((Q\,N/W)\,B\big)\quad\Rightarrow\quad B \gg (Q\,N)^2\;\text{bits}.
$$
- Example $N{=}1024$, $Q{=}1/8$: $(Q\,N)^2{=}(128)^2{=}16384$ bits $\approx 2\,\text{KiB}$, i.e., negligible once batches exceed a few kilobytes.

Summary conditions (decode-dominant):
- For any fixed $(Q,N)$ and sufficiently large $B$, our method’s linear $\Theta(B)$ decode beats RS’s quadratic and RLC’s cubic scaling.
- Concrete thresholds with $S_{\text{pkt}}{=}1024$ B, $W{=}64$:
  - vs RS: our method is preferable for $\;B \gtrsim (Q\,N)\,2^{20}\;$ bits $\;\big(\approx (Q\,N)\,2^{17}\;\text{bytes}\big)$.
  - vs RLC: our method is preferable for $\;B \gtrsim Q^{2}\,\sqrt{\tfrac{N}{64}}\,(8192)^{3/2}\;$ bits $\;\big(\approx 9.27\times10^4\,Q^{2}\,\sqrt{\tfrac{N}{64}}\;\text{bytes}\big)$.
  - Cold inversion becomes negligible once $\;B \gg (Q\,N)^2\;$ bits.

Notes:
- XOR parity can be cheaper at very low redundancy (e.g., single-loss recovery), but it cannot meet strong erasure budgets without large $R$; compare only when requirements match.
- Constants hidden by $\Theta(\cdot)$ vary across implementations (GF arithmetic, cache behavior). The above gives practical order-of-magnitude guidance for when our polar+interleave pipeline is CPU-favorable given the MTU constraint.
