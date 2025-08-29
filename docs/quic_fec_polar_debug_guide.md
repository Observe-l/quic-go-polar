# QUIC-FEC Polar Debug Guide (When PLR=0 Still Fails)
**Context.** You implemented QUIC-FEC using `quic-go` and three FEC schemes (RLC, RS, packet-level Polar). With **PLR=0** (dropper disabled), **Polar decoding still fails to recover the full file** even though RLC/RS work. This guide lists **likely root causes** and a **step-by-step triage plan** to locate and fix the issue quickly.

---

## A. Symptoms Recap
- QUIC transports `test_data/train_FD001.txt` via **DATAGRAM** frames.
- FEC parameters fixed: **N=8, K=6, R=2**, symbol size **L ≈ 1000–1400 bytes**.
- With PLR=0 (no intentional drops), **receiver does not reconstruct all data** (missing blocks or decode failure), specifically for **Polar** (both 3GPP URS and offline PolarArtifacts tables. When use PolarArtifacts, assume e = 1).

---

## B. High-Probability Root Causes (Shortlist)
1. **Header / Framing Mismatch**
   - `sym_id` off-by-one (1-based vs 0-based).
   - `block_id` wrap/duplication, or mis-ordered reassembly.
   - `payload_len (L)` mismatch between sender and receiver.
   - Endianness or struct packing differences in `FECHeader`.

2. **DATAGRAM Size / QUIC Limits**
   - `L + header > MaxDatagramFrameSize` (peer’s transport parameter). `quic-go` may return error or truncate; if you ignore `SendMessage` errors, packets effectively “lost” even when PLR=0.
   - Oversized PMTU causing kernel drops (even localhost can have limits).

3. **Early-Decode Logic Bug (K-of-N)**
   - You attempt decode immediately after **first K** symbols. For Polar, the **first K** can be **rank-deficient** (not full-rank subset). If on failure you **don’t retry** when new symbols arrive (K+1, K+2, …), the block **stays failed** even though all N eventually arrive.

4. **Polar Indexing / Permutation Inconsistency**
   - **Bit-reversal vs natural order**: encoder builds `G_N` (or parity matrix) in one order, decoder assumes the other.
   - **3GPP URS index base**: table indices misinterpreted (0-based vs 1-based; or wrong `N` truncation).
   - **Systematic mapping**: `G_{A,A}^{-1}` built with a different column/row order than used in transmission (`sym_id` order).

5. **Wrong or Non-Invertible Submatrix**
   - `G_{A,A}` not computed w.r.t. the same A and permutation as used on the wire.
   - Accidentally using a **non-systematic** parity matrix while assuming **systematic** decoding.

6. **Reassembly / Last-Block Handling**
   - Padded bytes not trimmed → file size mismatch → perceived “data missing”.
   - Blocks appended out of order (reordering not handled), or duplicate suppression incorrect.

7. **Concurrency / Race / Buffering**
   - Block state overwritten by concurrent goroutines (race in `map[block_id]` or slices).
   - End-of-transfer signaled **before** the final block decode completes.

8. **Table Selection / Table ID Drift**
   - For Polar, sender and receiver select different frozen/info sets (e.g., **different table name or K/N pairing**), or different systematic mapping versions.

---

## C. Hard Invariants You Can Assert (Fast Checks)
> Add these asserts/logs to immediately catch category errors.

1. **Handshake Metadata (reliable STREAM)**
   - Sender must send: `{scheme, N=8, K=6, L, total_blocks, table_id/hash, systematic=1}`.
   - Receiver must echo or log the exact values; fail fast if mismatched.

2. **Per-Datagram Validation (Receiver)**
   - Check: `header.N==8`, `header.K==6`, `0 <= sym_id < 8`, `payload_len==L`.
   - Drop and log any violation (count occurrences; should be 0 at PLR=0).

3. **SendMessage Error Check (Sender)**
   - Always check `if err != nil { log.Fatal("SendMessage failed:", err) }`.
   - Before sending, assert `L+header <= conn.MaxDatagramFrameSize()`; if not, **reduce L**.

4. **Accounting**
   - For each block: `sent_datagrams == N`, `received_datagrams == N` at PLR=0.
   - If not, the loss is **transport-level**, not FEC—fix DATAGRAM sizing / errors first.

5. **Polar Systematic Identity Check (Startup)**
   - Build `G_N` and info set A → compute `G_AA^{-1}` (GF(2)). Verify:
     - `hatG = G * P` where P applies your **wire permutation** (sym_id order).
     - `hatG[A, :]` (rows at info positions) is **identity** after systematic mapping.
   - If not, your permutation / A-indexing is inconsistent.

6. **Rank Test (No Erasure)**
   - Build coefficient matrix from **all N** received symbols. Verify rank ≥ K.
   - If rank < K with **all N present**, generator construction is wrong.

---

## D. Step-by-Step Triage Plan

### Step 0 — Baseline Offline (No QUIC, In-Process)
**Goal:** Prove Polar encoder/decoder is correct in isolation.
1. Generate random `K*L` bytes; build `N` code symbols with your Polar encoder.
2. Shuffle symbols; feed **all N** to decoder.
3. Expect: full recovery **byte-identical**.  
   - If FAIL: focus on **Polar math** (Section E).  
   - If PASS: proceed.

### Step 1 — QUIC DATAGRAM Sanity
**Goal:** Ensure no silent drops at PLR=0.
1. Set PLR=0. Instrument counters:
   - Sender: `blocks_sent`, `datagrams_sent_per_block` (==8), `send_errors` (==0).
   - Receiver: `datagrams_received_per_block` (==8).
2. If `recv < 8` while `send_errors==0` → **oversized datagrams** or **OS drops**.  
   - Reduce `L` (e.g., 1200 → 1100) and re-test.
   - Confirm `L+header <= conn.MaxDatagramFrameSize()`.

### Step 2 — Early-Decode Logic
**Goal:** Prevent false permanent failures.
1. Change policy: **do not finalize a block as failed** on the first `K` try.  
2. Maintain an **incremental Gaussian elimination state** (GF(2) for Polar) that **absorbs new rows** as additional symbols arrive.  
3. Attempt decode whenever the **current rank == K**; only **finalize failure** at end-of-transfer or timeout.

### Step 3 — Polar Indexing & Permutation
**Goal:** Align encoder & decoder index spaces.
1. Decide **one canonical index space**:  
   - **Natural order** of `F^{⊗n}`, **or** **bit-reversed** order.  
   - 3GPP URS sequence is typically given for the **bit-reversed** ordering (verify your source).
2. Ensure **A (info set)** is built in the **same index space** as `G_N` construction.
3. Ensure `sym_id` on the wire corresponds exactly to that index space (or explicitly apply a permutation matrix `P` consistently **both sides**).

### Step 4 — Systematic Mapping Consistency
**Goal:** Ensure the “info positions equal data” property actually holds.
1. Compute `G_AA`, invert over GF(2): `T = G_AA^{-1}`.
2. Build `hatG = G * M`, where `M` applies `T` in columns so that `hatG[A,:] = I`.
3. Verify by test encoding one block and checking `x[A] == data_symbols` bitwise.

### Step 5 — Reassembly & Last Block
**Goal:** Eliminate endgame bugs.
1. For final block: store original file size; after decode, **truncate padding** before writing.  
2. Make file write **block-id ordered**; buffer out-of-order completions in a map and flush in order.
3. Ensure no “double close” or premature “end-of-transfer” before last decode done.

### Step 6 — Concurrency Safety
**Goal:** Remove races that drop/overwrite data.
1. Build with `-race`; run under local loopback.
2. Guard shared `map[block_id]*BlockState` with a mutex; avoid sharing `[]byte` backing arrays across goroutines without copy.

---

## E. Polar Math Checklist (Common Pitfalls)

1. **G_N Construction**
   - Confirm `F = [[1,0],[1,1]]`, `G_N = F^{⊗n}` over GF(2).  
   - If you use **fast butterfly**, confirm stage wiring matches either natural or bit-reversed convention **consistently**.

2. **Ordering / Permutation**
   - If URS indices are bit-reversed but your `G_N` is natural, introduce permutation `π` such that all indexing, A, and `sym_id` refer to the **same** order.

3. **Systematic Encoder**
   - Use `G_AA^{-1}` computed in **exactly** the same row/column order as used on the wire.  
   - After mapping, verify `x[A] == u_A` for random test vectors.

4. **Decoder Rank**
   - With all N present, the selected K columns (your A) must be full rank. If not, your `A` / order is wrong.  
   - For partial sets (first K), do **not** assume full rank; keep adding rows.

5. **Tables**
   - Ensure both sides load the **same table id** (3GPP URS vs offline). Log the **hash** of the loaded table for safety.

---

## F. Minimal Diagnostic Experiments (Expected Outcomes)

1. **Offline Polar Self-Test (No QUIC)**
   - Input: random data; Output: exact match; Log: `rank(all_N) == K`.  
   - **Pass** → Polar math OK.

2. **QUIC Datagram Count at PLR=0**
   - Per block: sent=8, recv=8, send_errors=0.  
   - **Fail** → Fix `L` / datagram size / error handling.

3. **Incremental Decode Retest**
   - With PLR=0: even if first-K attempt fails, as soon as 7th/8th arrives, rank hits K → **decode succeeds**.  
   - **Fail** → Check early-decode policy and matrix update path.

4. **Systematic Identity Check**
   - Encode one block; verify `x[A] == data`.  
   - **Fail** → A/ordering/systematic mapping mismatch.

5. **End-to-End File Equality**
   - SHA-256(source) == SHA-256(received).  
   - **Fail** → Check last-block padding, block order, duplicate handling.

---

## G. Snippets / Pseudocode Hints

### G.1 Ensure datagram fits
```go
max := conn.MaxDatagramFrameSize()
if headerLen+L > int(max) {
  return fmt.Errorf("payload too large for datagram: have=%d need<=%d", headerLen+L, max)
}
if err := conn.SendMessage(buf); err != nil {
  log.Fatalf("SendMessage failed: %v", err)
}
```

### G.2 Incremental Gaussian (Polar, GF(2))
```go
// Keep a growing matrix A (rows = received symbols).
// Each new symbol adds a row; perform row-reduction incrementally.
// If rank == K, solve and emit the block.
```

### G.3 Systematic identity test (once at startup)
```go
// Build G_N, A, compute T = inv(G_AA). Apply to columns to form hatG.
// Check that hatG[A,:] == I (GF(2)). Fail fast if not.
```

---

## H. Acceptance Criteria (When Fixed)
- At **PLR=0**, for Polar:
  - **Per-block**: recv datagrams == 8; rank(all_N) == K; decode success.  
  - **End-to-end**: SHA-256 matches; no missing bytes; no premature EOF.
- Re-enable PLR=0.5%: Polar success rate high (as expected for N=8,K=6).

---

## I. If Still Failing
- Dump **one failing block**:
  - Log A (indices), permutation choice, `sym_id` list and arrival order, the `K×K` submatrix of first-K, and of all-N.
  - Attach the matrices (as hex rows) to quickly spot ordering/identity issues.
- As a temporary cross-check, send the **same block over a reliable STREAM** (one frame per symbol). If it decodes there, the bug is in **DATAGRAM path**; otherwise in **Polar math**.

---

**Checklist TL;DR:**  
1) Verify **no silent drops** (datagram size / SendMessage error).  
2) Make Polar **incremental decode** (don’t fail early).  
3) Align **indexing, permutation, and systematic mapping**.  
4) Fix **reassembly/padding** edge cases.  
5) Assert **rank(all_N)==K** and `x[A]==data` offline before re-testing QUIC.
