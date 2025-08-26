# Polar Encoder and Decoder for Packet-Level FEC

Date: 2025-08-26

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
# Polar Coding with Packet-Level Interleaving for Erasure Recovery: An IEEE-Style Exposition

Date: 2025-08-26

Abstract—We present a binary polar-coding pipeline tailored to packet-level erasure recovery via bit-level interleaving across K packets. The encoder uses a column-XOR realization of the polar transform fused with direct packet assembly; the decoder performs algebraic erasure recovery by selecting known rows of the transform and solving a packed GF(2) linear system batched across codewords. We derive encoder complexity Θ(QN^2/W) (word operations) and show warm-path decoder latency independent of the number of lost packets for a fixed loss mask. Implementation details enable high throughput via word packing and caching. Empirical measurements validate the analysis.

Index Terms—Polar codes, erasure decoding, GF(2), interleaving, forward error correction, packed bit operations.

## I. Introduction

Polar coding [Arıkan] provides a structured binary transform with low-complexity encoding. We target a practical setting in which codewords are bit-interleaved across packets; packet loss induces erasures at known positions. Our contributions are: (i) an encoder that realizes the polar transform as a column-wise XOR superposition fused directly into packet buffers; (ii) an algebraic decoder over GF(2) that selects full-rank known rows and performs a batched solve; (iii) a complexity analysis evidencing $\Theta(QN^2/W)$ encoder cost and warm-path decoder latency invariant to loss count, given a fixed loss mask.

## II. Notation and System Model

 - Polar parameters: kernel $\mathsf{F}=\begin{bmatrix}1&0\\1&1\end{bmatrix}$, exponent $n$, length $N=2^n$.
 - Reliability sequence and information set: $\mathcal{I}\subseteq[0{:}N{-}1],\ |\mathcal{I}|=Q\,N$, with rate $Q\in(0,1]$.
 - Generator: $\mathsf{G}_N = \mathsf{F}^{\otimes n}$ (no bit-reversal inside $\mathsf{G}_N$); implementation aligns the given reliability sequence by bit-reversing indices.
 - Interleaver: a fixed permutation $\pi:[0{:}N{-}1]\to[0{:}N{-}1]$.
 - Packetization: $K$ packets; each codeword is split into $K$ equal subsets of $S=N/K$ bits. Packet $s$ stores, consecutively, the $s$-th subset for codewords $t=0,\dots,T{-}1$.
 - Machine model: word size $W$ (e.g., 64). Packed GF(2) bitsets use $\lceil N/W\rceil$ words.

Let $\mathbf{u}^{(t)}\in\{0,1\}^N$ denote the information vector for codeword index $t$ (zeros outside $\mathcal{I}$). The encoded codeword is
$$
\mathbf{x}^{(t)} = \mathbf{u}^{(t)}\,\mathsf{G}_N.\tag{1}
$$
The interleaver output $\pi(\mathbf{x}^{(t)})$ is partitioned into $K$ blocks of length $S$. Packet $s$ stores block $s$ at byte offset $t\cdot(S/8)$.

Loss model: a subset $\mathcal{S}_{\text{miss}}\subseteq\{0,\dots,K{-}1\}$ is erased (entire packets), known at the receiver.

## III. Encoder

### A. Column–XOR Realization

Write $\mathsf{G}_N=[\mathbf{g}_0\;\mathbf{g}_1\;\cdots\;\mathbf{g}_{N-1}]$. Over GF(2), (1) equals
$$
\mathbf{x}^{(t)} = \bigoplus_{j=0}^{N-1} u^{(t)}_j\,\mathbf{g}_j = \bigoplus_{j\in\mathcal{I}} u^{(t)}_j\,\mathbf{g}_j,\tag{2}
$$
because $u^{(t)}_j\in\{0,1\}$ and addition is XOR. Thus encoding is the XOR superposition of those columns for which $u^{(t)}_j=1$.

Proposition 1 (Column-XOR equivalence). Precomputing packed columns $\{\mathbf{g}_j\}_{j\in\mathcal{I}}$ and XORing those with $u^{(t)}_j=1$ produces exactly $\mathbf{x}^{(t)}$.

Proof: Immediate from (2).

### B. Fused Encode-to-Packets

We avoid materializing $\mathbf{x}^{(t)}$ by composing (2) with interleaving and packet assembly. For each message byte position $b_y\in[0{:}(Q\,N)/8{-}1]$ and value $v\in[0{:}255]$, a LUT slice $\mathcal{L}[b_y,v]$ of length $N/8$ bytes encodes the XOR contribution across all $K$ subsets (concatenated). Encoding XORs $\mathcal{L}[b_y,v]$ at offset $t\cdot(N/8)$ into the $K$ packet buffers (strided by subset). Persistence keys the LUT by $(n,|\mathcal{I}|,K,\text{CRC}(\pi))$.

### C. Encoder Complexity

Let $H(\mathbf{u}^{(t)}_{\mathcal{I}})$ be the Hamming weight of information bits. The column-XOR encoder performs
$$
C_\text{col} = H(\mathbf{u}^{(t)}_{\mathcal{I}})\cdot (N/W) = \Theta\big(Q\,N^2/W\big)\quad\text{word XORs},\tag{3}
$$
in the worst case (and on average for i.i.d. bits up to a 1/2 factor). The fused LUT encoder executes
$$
C_\text{LUT,bytes} = \frac{Q\,N}{8}\cdot\frac{N}{8} = \frac{Q\,N^2}{64}\quad\text{byte XORs},\tag{4}
$$
equivalently $\Theta(Q\,N^2/W)$ word ops up to constants. Note that (4) is independent of $K$ since
$$
\frac{N}{8} = \sum_{s=0}^{K-1} \tfrac{(N/K)}{8}.\tag{5}
$$

## IV. Decoder

### A. Problem Formulation (Inputs/Outputs)

Given a family of packets $\{\mathbf{P}_s\}_{s=0}^{K-1}$, where $\mathbf{P}_s$ is either a byte string or empty (erased), the goal is to reconstruct the sequence of messages $\{\mathbf{m}^{(t)}\}_{t=0}^{T-1}$, where $\mathbf{m}^{(t)}\in\{0,1\}^{Q\,N}$ are the information bits, packed as bytes (little-endian bit order within bytes). The mapping from packet bytes to known rows is determined by $\pi^{-1}$ and fixed offsets: for a selected row index $r_i$, let
$$
\big(s_i, o_i, m_i\big) = \text{subset index, byte offset, and bit mask},\tag{6}
$$
so that the right-hand side (RHS) bits are read as
$$
\mathbf{B}[i,t] = \left(\mathbf{P}_{s_i}[\,t\cdot(S/8)+o_i\,] \;\&\; m_i\right)\neq 0.\tag{7}
$$
The batched recovery computes
$$
\mathbf{U}^\top = \mathbf{A}^{-1}\,\mathbf{B}^\top,\quad \mathbf{A}=\mathsf{G}_N[\mathcal{R}_\star,\mathcal{I}],\tag{8}
$$
where $\mathcal{R}_\star$ is a full-rank selection of $|\mathcal{I}|=Q\,N$ known rows induced by the non-erased packets.

### B. Unified Algebraic Decoder

We select $\mathcal{R}_\star$ deterministically from the set of known rows (e.g., by a greedy pivot rule compatible with the structure of $\mathsf{G}_N$) and invert $\mathbf{A}$ over GF(2) using packed Gaussian elimination. The inverse $\mathbf{A}^{-1}$ and the tuple list $\{(s_i,o_i,m_i)\}$ are cached per loss mask. For each batch we synthesize $\mathbf{B}$ by (7) and compute (8) via packed XOR MACs.

Remark (no-loss fast path): when no packets are erased, one may apply $\mathsf{F}^{\otimes n}$ (self-inverse) directly to $\pi(\mathbf{x}^{(t)})$ to obtain $\mathbf{u}^{(t)}$ and then extract the $Q\,N$ data indices. Our implementation routes both cases through the algebraic path for uniformity and cache reuse.

### C. Decoder Complexity and Loss-Rate Invariance

Let $T$ be the number of codewords in the batch. With packed word operations:
$$
	ext{Cold inversion: }\ \tilde{\Theta}\big((Q\,N)^3/W\big)\ \text{ once per loss mask},\tag{9}
$$
$$
	ext{RHS build: }\ \Theta\big(T\,Q\,N\,S/W\big),\tag{10}
$$
$$
	ext{Batched multiply: }\ \Theta\big(T\,(Q\,N)^2/W\big).\tag{11}
$$
Since $S=N/K$ is small for typical $K$, (11) dominates. For a fixed loss mask (warm path), decode latency depends on $Q,N,T,W$ and is independent of how many packets were lost, because (9) is amortized and (10)–(11) do not depend on the loss count once $\mathcal{R}_\star$ is fixed.

Implementation notes: (i) cache $\mathbf{A}^{-1}$ and row tuples $(s_i,o_i,m_i)$ per mask to remove setup; (ii) synthesize $\mathbf{B}$ using (7) with no divides/modulo in the inner loop to improve cache locality.

### D. Packet-Loss Analysis: Input Construction and Packet Repair

We formalize how packet erasures induce known/unknown codeword positions and how the decoder constructs inputs and repairs missing packets.

1) Interleaver-induced row partition. Let $S=N/K$ and define the row index sets
$$
\mathcal{J}_s \triangleq \big\{ j\in[0{:}N{-}1] : \big\lfloor \pi(j)/S \big\rfloor = s \big\},\quad s\in\{0,\dots,K{-}1\}.
$$
Thus, after interleaving, packet $s$ carries exactly the $S$ bits at row indices $\mathcal{J}_s$ (for every codeword $t$).

2) Loss mask and known rows. Let $\mathcal{M}\subseteq\{0,\dots,K{-}1\}$ be the set of erased packets and $\mathcal{R}_\mathrm{cv} = \{0,\dots,K{-}1\}\setminus\mathcal{M}$ the received set. The set of known row indices is
$$
\mathcal{R} \triangleq \bigcup_{s\in\mathcal{R}_\mathrm{cv}} \mathcal{J}_s\subseteq[0{:}N{-}1].
$$
The decoder selects a full-rank subset $\mathcal{R}_\star\subseteq\mathcal{R}$ with $|\mathcal{R}_\star|=|\mathcal{I}|=Q\,N$ (by a deterministic pivoting rule) and forms
$$
\mathbf{A} = \mathsf{G}_N[\mathcal{R}_\star,\mathcal{I}]\in\{0,1\}^{|\mathcal{I}|\times|\mathcal{I}|}.
$$

3) RHS construction from received packets. For each selected row index $r_i\in\mathcal{R}_\star$, we precompute a tuple $(s_i,o_i,m_i)$ such that, for codeword $t$, the interleaved bit $y^{(t)}_{r_i}$ is the $m_i$-masked bit of packet $s_i$ at byte offset $t\cdot(S/8)+o_i$. This yields the RHS per (7):
$$
\mathbf{B}[i,t] = \big(\mathbf{P}_{s_i}[\,t\cdot(S/8)+o_i\,] \;\&\; m_i\big)\neq 0.
$$
Note there are no contributions from erased packets: unknown rows are not part of $\mathcal{R}_\star$ and thus never appear on the RHS.

4) Algebraic erasure solve (hard decisions). With $\mathbf{A}^{-1}$ cached per mask, we recover the information bits for all $t=0,\dots,T{-}1$ via
$$
\mathbf{U}^\top = \mathbf{A}^{-1}\,\mathbf{B}^\top,\qquad \mathbf{U}\in\{0,1\}^{|\mathcal{I}|\times T}.
$$
This is a deterministic GF(2) solve; unlike SC, we do not use likelihoods or LLRs (e.g., 0.5 for erasures). Erasures are treated as unknown variables eliminated from the equations; only received rows constrain the solution.

5) Reconstructing missing packets. Let $\mathcal{J}_s$ denote the row set of missing packet $s\in\mathcal{M}$. The corresponding codeword bits for codeword $t$ satisfy
$$
\mathbf{y}^{(t)}_{\mathcal{J}_s} = \mathsf{G}_N[\mathcal{J}_s,\mathcal{I}]\;\mathbf{u}^{(t)}_{\mathcal{I}}.
$$
Two equivalent repair paths exist:

- Message-first repair: compute $\mathbf{u}^{(t)}_{\mathcal{I}}$ as above and then form $\mathbf{y}^{(t)}_{\mathcal{J}_s}$ by multiplying $\mathsf{G}_N[\mathcal{J}_s,\mathcal{I}]$; write the $S$ recovered bits into packet $s$ at offset $t\cdot(S/8)$.

- Direct repair from RHS: precompute the mask-specific repair matrix
$$
\mathbf{R}_s \triangleq \mathsf{G}_N[\mathcal{J}_s,\mathcal{I}]\;\mathbf{A}^{-1}\in\{0,1\}^{S\times |\mathcal{I}|},
$$
so that, for every codeword $t$,
$$
\mathbf{y}^{(t)}_{\mathcal{J}_s} = \mathbf{R}_s\;\mathbf{b}^{(t)},\qquad \mathbf{b}^{(t)} \equiv \mathbf{B}[\cdot,t].
$$
This avoids materializing $\mathbf{u}^{(t)}_{\mathcal{I}}$ and repairs packets directly from received bytes. $\{\mathbf{R}_s\}_{s\in\mathcal{M}}$ are cached per loss mask alongside $\mathbf{A}^{-1}$.

Complexity. Warm-path repair via $\mathbf{R}_s\mathbf{b}^{(t)}$ costs $\Theta\big(T\,|\mathcal{I}|\,S/W\big)=\Theta\big(T\,Q\,N\,S/W\big)$ per missing $s$, matching the dominant terms in (10)–(11). The algebraic method succeeds whenever $\mathbf{A}$ is full-rank; if rank deficiency occurs (extreme loss patterns or high $Q$), the decoder may reselect pivots or defer until more packets arrive.

Relation to SC. An SC decoder on a BEC commonly sets LLRs of erased positions to zero (i.e., probability 0.5) and proceeds sequentially. Here we exploit the linearity of the code over GF(2): known (received) rows yield linear equations, erased rows are unknowns, and we solve the resulting square system on the information set. This is optimal for erasures given sufficient independent equations, and it yields loss-rate–invariant warm-path latency as discussed in §IV-C.

## V. Complexity Comparison

| Scheme | Encoder | Decoder | Notes |
|---|---|---|---|
| Polar + interleave (fused) | $\Theta(Q\,N^2/W)$ per codeword | Cold: $\tilde{\Theta}((Q\,N)^3/W)$. Warm batch: $\Theta\big(T\,(Q\,N)^2/W + T\,Q\,N\,S/W\big)$ | Inverse and row tuples cached by loss mask. |
| Polar (butterfly) + interleave | $\Theta(N\log N)$ + copy | No-loss: $\Theta(N\log N)$/cw; erasures: as left | More memory traffic. |
| RS (GF$(2^m)$) | $\Theta(KR)$ | $\Theta(K^2)$ typical | MDS, heavier arithmetic. |
| XOR parity | $\Theta(KR)$ | $\Theta(KR)$ | Limited to $R$ erasures. |
| RLC (GF(2)) | $\Theta(KR/W)$ | $\tilde{\Theta}(K^3/W)$ | Needs coding vectors. |

Under equal data size $B$ and rate $Q$, the polar pipeline is linear in $B$ (constants depend on $N,W,Q$), whereas RS decoders are typically quadratic and RLC cubic.

## VI. Experimental Illustration

Setup: $N=1024$, $K=32$, Linux x86_64, single thread. Packets carry eight codewords per subset ($S/8=4$ bytes per codeword per packet). We varied $|\mathcal{I}|$ and loss masks across batches.

Illustrative outcomes (single run): encode is faster than decode; decode scales roughly linearly with $Q$ and is insensitive to the number of lost packets along a warm mask, matching (11).

## VII. Conclusion

A fused column–XOR polar encoder with packet-level interleaving realizes $\Theta(Q\,N^2/W)$ complexity independent of packet count. An algebraic erasure decoder over GF(2), with per-mask caching, yields warm-path latency linear in batch size and independent of loss count for a fixed mask. Packed operations and simple caches provide a practical, high-throughput implementation in software.
