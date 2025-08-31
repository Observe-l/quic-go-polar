# Systematic RaptorQ FEC — Design Plan (Go), **using `raptorq`**
**Scope.** We will implement **Systematic RaptorQ** FEC in our Go project using the open‑source **`raptorq`** package (with in‑memory encode/decode), and benchmark it against our **RLC**, **RS**, and **packet‑level Polar** baselines. This document is **conceptual only** (no code).

---

## 1) Selected Open‑Source Package (single choice)
- **Repository:** https://github.com/xssnick/raptorq  
- **Go import path:** `github.com/xssnick/raptorq`  
- **Why this package:** It exposes **in‑memory** RaptorQ primitives suitable for streaming/data‑plane FEC (create encoder from bytes, generate symbols; feed symbols to a decoder, reconstruct bytes). This avoids file I/O and makes kernel timing fair vs. our in‑memory RLC/RS/Polar baselines.

> **IPR note:** RaptorQ (RFC 6330) has IETF IPR declarations. Run a legal review before any commercial deployment.

---

## 2) Operating Model (Systematic RaptorQ)
- **Generation configuration (fixed across tests):**  
  **N = 32** total symbols, **K = 26** source (systematic) symbols, **L = 1500 B** per symbol (≤ MTU).  
  The remaining **N − K = 6** are repair symbols.
- **Systematic behavior:** first **K** outputs are the original symbols. With **zero loss**, the receiver **concatenates K** and finishes (no decoding work).
- **Object segmentation:** larger payloads are split into consecutive **generations** of size `K × L`. Track `gen_id` and original object length to **trim padding** on the tail generation.
- **Parity pacing (recommended):** interleave repair symbols (e.g., emit 1–2 early and spread the rest) to reduce **tail‑loss latency** without changing the fixed budget `N`.

---

## 3) Data Plane (Conceptual, no code)
### Sender
1) Load object bytes in memory.  
2) Partition into **K** chunks of **L** bytes (pad last as needed).  
3) Use **`raptorq`** to produce exactly **K systematic + 6 repair** symbols for each generation.  
4) Schedule transmission (data + paced parity) through your transport (UDP/QUIC harness).

### Receiver
1) Group incoming symbols by `gen_id`.  
2) **Fast path @ p=0:** if all **K** systematic IDs are present, **concatenate and trim** (no decode).  
3) Otherwise, once **received ≥ K + ε** symbols (ε small at K=26), invoke **`raptorq`** decode; on success, trim to the original length.

---

## 4) Experiment A — Correctness @ p = 0 (200 repeats)
**Input:** `test_data/train_FD001.txt`.  
**Goal:** prove systematic fast path and **byte‑exact** reconstruction without loss.  
**Plan:** run **200** end‑to‑end transmissions at **p = 0**. Expect **100% success**; **decode time ≈ 0**. Log encode/decode timing and equality check.

**Acceptance:** 200/200 exact matches; decode timing effectively zero.

---

## 5) Experiment B — Bake‑Off vs RLC / RS / Packet‑Polar
**Common configuration for all schemes:**
- **Object:** random **3 MB** buffer.  
- **Per generation:** **N = 32**, **K = 26**, **L = 1500 B**; fixed budget (**send exactly N**).  
- **Loss model:** i.i.d. Bernoulli per packet,  
  \(p \in \{0,\ 0.1\%,\ 0.5\%,\ 1\%,\ 5\%,\ 10\%,\ 15\%\}\).  
- **Repeats:** **10,000** trials per \(p\) per scheme.  
- **Schemes:** **RaptorQ (`raptorq`)**, **RLC**, **RS (n=32,k=26)**, **packet‑level Polar** (same N/K; interleaver optional for i.i.d. loss).

**Metrics to report (per scheme, per p):**
- **Reliability:** success rate (full object reconstructed).  
- **Performance:** **average** and **total** **encode time**, **average** and **total** **decode time**.  
- **Methodology controls:** in all schemes, measure **kernel time** only (exclude file I/O); pre‑allocate buffers; use monotonic timers.

**Sanity expectations:** with `(N,K)=(32,26)`, success is **extremely likely** for ≤ 6 losses per generation for RaptorQ/RS; RLC may show rare rank failures; Polar is not MDS but comparable under i.i.d. loss at this rate.

---

## 6) QUIC/Transport Integration (Conceptual)
- Carry FEC symbols in QUIC DATAGRAM/STREAM with a compact header `{gen_id, N, K, sym_id, L}`.  
- **Congestion‑controlled repair:** send repair under cwnd; do **not** bypass congestion control.  
- **No loss masking:** do **not** treat “repaired” as “ACK‑received” for congestion control; keep loss signals visible.

---

## 7) Timing & Logging
- **Exclude disk I/O** from measured intervals.  
- Use **monotonic clocks** (ms/µs precision).  
- **Pre‑allocate** and reuse symbol/scratch buffers.  
- Record **random seeds** for reproducibility.  
- Emit **CSV** per trial and aggregated per (`scheme`,`p`):
  - `ok_rate`, `avg_encode_ms`, `sum_encode_ms`, `avg_decode_ms`, `sum_decode_ms`.

---

## 8) Acceptance Criteria
- **Experiment A:** 100% byte‑exact at p=0; decode ≈ 0 ms across 200 repeats.  
- **Experiment B:** curves consistent with theory (RaptorQ/RS near‑certain up to 6 losses); encode/decode in the **sub‑ms to few ms** per generation range on commodity CPUs.

---

## 9) Checklist (single‑package plan)
- [ ] Vendor and pin **`github.com/xssnick/tonutils-go/adnl/rldp/raptorq`**; document commit hash.  
- [ ] Fix generation parameters: **N=32**, **K=26**, **L=1500 B** (systematic).  
- [ ] Build transport‑agnostic harness (Tx/Rx, loss injector, timers, CSV).  
- [ ] **Experiment A** (200× p=0 on `train_FD001.txt`) passes with exact equality.  
- [ ] **Experiment B** (10k× per p) logged for **RaptorQ/RLC/RS/Polar** with identical N/K/L.  
- [ ] Produce CSVs and a short comparative summary table.  
- [ ] IPR review for RaptorQ prior to any commercial deployment.
