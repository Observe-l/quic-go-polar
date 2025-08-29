# Polar FEC end-to-end failure postmortem

Summary
- Symptom: With Polar selected, some E2E runs failed even at very low or zero loss, while RS and RLC succeeded.
- Setup: QUIC DATAGRAM, N=8, K=6, L around 1300 bytes, Polar params from 3GPP by default.
- Root causes:
  1) DATAGRAM size: `header + L` exceeded the peer MaxDatagramFrameSize on some paths. That led to send errors or kernel drops, effectively creating losses even with the dropper disabled. Polar appeared flaky at L=1300; it worked consistently with L≈1100.
  2) Early-decode assumptions: a first-K subset of symbols can be rank-deficient for Polar. If decode is attempted once and failure is latched without retry when more symbols arrive, the block may be marked failed unnecessarily. Our server already retries when new symbols arrive; we verified this behavior.
  3) Parameter drift risk: Polar parameter selection (3GPP vs artifacts vs runtime) must match on both sides. Mismatches will cause decode failure even if transport is lossless.
  4) Receiver queue overflow at PLR=0: QUIC DATAGRAM receive queue is bounded (max ~128 buffered entries in `datagram_queue.go`). Sending many blocks back-to-back without pacing can overflow this queue, causing transport-level drops even when the user-configured loss is 0. In practice, the large file test stressed Polar first and exposed this.
  5) Early connection close (no completion handshake): The client originally closed the connection immediately after sending the last DATAGRAM. The server often logged `Application error: bye` and stopped draining pending DATAGRAMs, resulting in fewer bytes written than expected. This manifested on large files and looked like a Polar-specific failure because those runs used a different timing/size profile.

What we changed
- Lowered the default client symbol size `-L` from 1300 to 1100 to fit typical DATAGRAM limits out of the box. File: `cmd/quicfec-client/main.go`.
- Added an optional `-dgram-warn` flag to emit a one-time warning if a DATAGRAM exceeds a threshold (defaults to 1200 bytes), guiding users to reduce `-L`.
- Documented recommended `L≈1100` and the need to keep Polar selection flags in sync (see `doc/quickstart.md`).
 - Added pacing and end-of-transfer handshake for large transfers:
   - Client: `-pace` (per-DATAGRAM sleep, default 300µs) and `-block-pause` (sleep between blocks, default 2ms) to avoid receiver queue overflow.
   - Client waits for a tiny completion ack on a new stream (`-post-wait` is the timeout) so it won't close before the server drains DATAGRAMs.
   - Server: extends its receive timeout on progress and sends the 1-byte completion ack when the entire file is written.

Verification
- Unit: `TestPacketPolar_EncodeDecode_Simple` (packet-level math) passes.
- E2E small: Polar (3GPP) with PLR=0 and PLR=1% using L=1100 on a ~200 KB file → MATCH.
- E2E large: Polar (3GPP) on `test_data/train_FD001.txt` (~3.35 MB) at PLR=0 and PLR=1% with pacing and completion ack → server wrote full size, MATCH.

Lessons
- Always validate or warn about DATAGRAM sizing to avoid silent losses.
- Polar decoding should be incremental; don't latch early failures.
- Make parameter sources explicit and aligned between endpoints.
 - Pace DATAGRAMs for large transfers and keep a completion handshake to avoid receiver queue overflow and early close.

Why RS and RLC looked fine (even at PLR=0)
- Different test profile: RS/RLC were validated first on smaller files and/or with slightly different `L`, which didn't stress the DATAGRAM receive queue or early close path. Polar was exercised on a large file earlier, which made the non-FEC issues visible.
- Parameter drift is Polar-only: RS and RLC don't depend on external parameter tables. A client/server mismatch in Polar parameter mode (3GPP vs artifacts vs runtime) breaks Polar decode even with zero packet loss; RS/RLC are unaffected.
- Robustness to subset choice: All three codes need any K independent symbols. Polar's naive "first K" subset can be rank-deficient more often than RS's Vandermonde structure or well-conditioned RLC draws. If you attempt a single early decode and give up, Polar will appear to fail more. Our receiver now retries when more symbols arrive.
- Timing sensitivity: Without pacing and a completion ack, large transfers could drop transport DATAGRAMs or close early. This isn't Polar-specific, but in practice Polar tests hit those conditions first; once pacing/ack were added, Polar behaved like RS/RLC.
