# Integrating **RaptorQ FEC** with QUIC — Engineering Guide (No Code)

**Context.** You already have **RLC**, **RS**, and **packet-level Polar** integrated with QUIC. This guide adds **Systematic RaptorQ** to the same harness with compatible framing, pacing, and measurement. Focus: **end-to-end latency, success rate, and fairness** under QUIC congestion control.

---

## 1) Scope & Success Criteria

### 1.1 Scope
- Add **Systematic RaptorQ** (RFC 6330 family) as a fourth FEC option in the existing QUIC-based sender/receiver harness.  
- Keep **test parity** with the other FECs: same experiment runner, loss injectors, CSV logging, dashboards.

### 1.2 Success Criteria
- **Correctness:** at **p=0** (no loss), **zero-decode fast path** (receiver concatenates systematic symbols), exact byte match.
- **Fairness:** RaptorQ parity **obeys QUIC pacing & cwnd**; FEC does **not** mask loss signals in ACK processing.  
- **Performance:** meets target p95 tail latency and success rate vs. RLC/RS/Polar at equal overhead.  
- **Maintainability:** consistent wire format; configurable `N,K,L`; togglable parity pacing profile.

---

## 2) Operating Parameters & Profiles

| Parameter | Default | Notes |
|---|---:|---|
| **N** (total symbols / generation) | 32 | Keep aligned with your baselines |
| **K** (systematic symbols) | 26 | Systematic first, then repair |
| **R = N-K** (repair) | 6 | Fixed-rate profile for apples-to-apples |
| **L** (symbol bytes) | 1500 (link-MTU profile) | For QUIC-min-dgram profile, set **L ≈ 1200** |
| **Tuning ε** (decode overhead target) | 2–4 (when rateless) | For fixed N, ε is implicit (=R/K) |

Two deployment profiles:
- **Link-MTU profile**: `L=1500` (IPv4 LAN/WAN with PMTU≈1500).  
- **QUIC-min-datagram profile**: `L≈1200` to fit QUIC min-size and typical Internet PMTU; confirm with Path MTU Discovery (PMTUD).

> Keep a single configuration object `{N,K,L,object_id,gen_id}` shared by sender and receiver. Persist in logs per trial.

---

## 3) Wire Format Over QUIC

Use the **same wire discipline** you used for RLC/RS/Polar to ease instrumentation. Recommended: **QUIC DATAGRAM** for FEC symbols (no HOL blocking), or a dedicated low-priority **STREAM** if DATAGRAM is unavailable.

**FEC Symbol Header (varint fields, QUIC-style):**
```
object_id, gen_id, N, K, L, sym_id, payload_len_flag
```
- `object_id` — groups generations of one object/file.  
- `gen_id` — generation index within object.  
- `N,K,L` — fixed per object or per session (may be elided if negotiated).  
- `sym_id ∈ [0..N-1]` — 0..K−1 = **systematic**; K..N−1 = **repair**.  
- `payload_len_flag` — set once for the final generation; used to trim padding at the receiver.

**Payload:** one symbol of length `L` (except possibly the last trimmed symbol).  
All fields travel **inside** QUIC encryption; integrity is guaranteed by QUIC AEAD.

> Keep the header identical across RaptorQ/RLC/RS/Polar to reuse your parsers and logs.

---

## 4) Sender: Scheduling & Congestion Discipline

### 4.1 Symbol Production
- For each generation, produce **K** systematic symbols followed by **R** repair symbols from the RaptorQ encoder.  
- **Systematic first** ensures zero-decode path at `p=0` and reduces CPU on the receiver.

### 4.2 Parity Pacing (latency-friendly)
Adopt the same “**no harm**” principle you used for other FECs:
- **Under cwnd, obey pacing**: FEC must not bypass congestion control.  
- **Interleave repair**: avoid sending all parity at the end of the generation—e.g., emit 1–2 repair early, spread the remainder uniformly (stride `≈ ⌊K/R⌋`).  
- **When app has no fresh data**: permit parity to fill pacing gaps (prevents idle tail).  
- **Priority**: give app-data a higher priority than parity unless tail-loss policy requires otherwise; document the policy per experiment.

### 4.3 Retransmission Policy
- Prefer **not** retransmitting original lost systematic symbols; **send extra repair** instead when feedback indicates deficit (optional rateless “top-off”).  
- When fixed budget experiments require exactly `N`, disable top-off to remain comparable with RS/Polar/RLC.

---

## 5) Receiver: Collection & Decoding

### 5.1 Collection
- Group by `(object_id, gen_id)`; maintain a bitmap for `sym_id` presence.  
- **Fast path**: if all `0..K−1` are received, **concatenate** and trim (`payload_len`), **skip RaptorQ decode**.

### 5.2 Decode Threshold & Invocation
- In fixed-N tests, attempt decode once **`received ≥ K`** (or `K+ε` if you require a margin).  
- Invoke the RaptorQ decoder with the set of received symbols (any mix).  
- On success: output the `K` source symbols, trim the tail using `payload_len` if flagged.

### 5.3 Loss Visibility & ACK
- Do **not** mark “repaired” data as “ACKed” to congestion control. Maintain QUIC’s view of loss to remain fair.  
- Maintain **per-generation** stats: first-arrival time, decode-invocation time, decode-duration, completion time.

---

## 6) Control Plane: Negotiation & Telemetry

### 6.1 Negotiation (out-of-band or per-session)
- `fec_scheme = {raptorq, rlc, rs, polar}`  
- `{N,K,L}` and whether **rateless top-off** is enabled  
- Parity pacing profile: `{uniform, early+uniform, tail-heavy}`

### 6.2 Telemetry
- **Sender**: for each generation—bytes app vs. bytes parity, pacing budget usage, cwnd at emission, per-symbol send timestamps.  
- **Receiver**: arrival bitmap, inter-arrival gaps, decode attempts, success/failure, completion latency.  
- **Both**: RTT samples, loss events (from QUIC), ECN marks if available.

---

## 7) Test Matrix & KPIs

### 7.1 Functional
- **p=0** end-to-end on `test_data/train_FD001.txt` × 200 repeats → 100% byte-exact, decode≈0 ms.

### 7.2 Performance (fixed budget; apples-to-apples)
- **Object**: random 3 MB.  
- **Schemes**: RaptorQ / RLC / RS / Polar.  
- **Loss**: \(p \in \{0, 0.1\%, 0.5\%, 1\%, 5\%, 10\%, 15\%\}\) i.i.d. per packet.  
- **Repeats**: 10k per (scheme, p).  
- **Profiles**: `L=1500` (MTU profile) and `L≈1200` (QUIC-min-dgram).  
- **Metrics** (per scheme, per p):  
  - Success rate (object reconstructed).  
  - Avg/Total **encode time**; Avg/Total **decode time**.  
  - **Application latency**: generation completion time CDF (p50/p95/p99).  
  - **Goodput** (app bytes delivered / wall time).  
  - **Fairness**: pacing compliance, cwnd evolution vs. baseline without FEC.

### 7.3 Optional: Rateless Top-Off
- Enable small **on-demand extra repair** (e.g., up to +2 symbols) when deficit detected. Compare **tail latency** to fixed-N.

---

## 8) Risk & Mitigations

| Risk | Mitigation |
|---|---|
| FEC hides loss → unfair QUIC | Keep loss visible in ACK accounting; parity under cwnd |
| Parity bursts → queueing delay | Uniform pacing; early parity to reduce tails |
| MTU mismatch / fragmentation | Enforce `L` ≤ PMTU – headroom; enable PMTUD |
| Decoder CPU spikes at high p | Cap per-generation top-off; decode budget & backoff |
| Reordering | Use symbol IDs; decoder robust to reorder |
| Clock skew in timing | Use monotonic timers; timestamp at send/recv edges |

---

## 9) Implementation Order (Milestones)

1. **Parity pacing & wire format alignment** with existing schemes (no decode yet).  
2. **Systematic fast path** at p=0; byte-exact verification on `train_FD001.txt`.  
3. **Decoder integration**; fixed-N experiments (no top-off).  
4. **Telemetry** and CSV aggregation parity with RLC/RS/Polar.  
5. Full **test matrix** (two profiles for `L`), dashboards (success rate & latency CDFs).  
6. **(Optional)** Rateless top-off + parity pacing sensitivity study.

---

## 10) Configuration Defaults (for CI)

```
fec_scheme = raptorq
N = 32
K = 26
L = 1500          # use 1200 for QUIC-min-dgram profile
object_size = 3 MiB
loss_grid = [0, 0.001, 0.005, 0.01, 0.05, 0.10, 0.15]
repeats = 10000
parity_pacing = early+uniform    # 1–2 early, rest uniform
rateless_topoff = disabled       # enable in optional study
metrics = [success_rate, avg/sum encode/decode, goodput, latency CDF, fairness]
```

---

## 11) Deliverables

- Wire-format & negotiation note (1–2 pages).  
- CSV & plots for all schemes across the loss grid (both `L` profiles).  
- Short report: success rate, latency CDFs (p50/p95/p99), encode/decode cost; fairness plots vs. no-FEC.  
- (Optional) Rateless vs. fixed-N comparison (RaptorQ only).

---

## 12) Checklist

- [ ] QUIC frame handler extended for **RaptorQ** headers (DATAGRAM or STREAM).  
- [ ] **Systematic fast path** verified at p=0 (200×, byte-exact).  
- [ ] **Parity pacing** implemented and logged.  
- [ ] **Decoder** integrated; decode threshold policy documented.  
- [ ] **Telemetry parity** with RLC/RS/Polar; CSV aggregation identical.  
- [ ] Full **test matrix** executed; dashboards generated.  
- [ ] (Optional) Top-off study done; trade-offs documented.
