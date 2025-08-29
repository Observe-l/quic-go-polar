# QUIC FEC demo — quick start

This guide shows how to run the demo server and client, and what parameters to use. It covers Polar (default), RS, and RLC.

## Prerequisites
- Go 1.23+ (module uses a Go 1.24 toolchain).
- This repo checked out on the `fec` branch.

## Build
You can either run directly or build binaries.

```bash
# From repo root
go build -o bin/quicfec-server ./cmd/quicfec-server
go build -o bin/quicfec-client ./cmd/quicfec-client
```

Or run without building:

```bash
# Server (terminal 1)
go run ./cmd/quicfec-server

# Client (terminal 2)
go run ./cmd/quicfec-client
```

## Important notes
- Both sides must use the same FEC scheme and the same N, K, L parameters.
- For Polar, both sides must also use the same polar parameter mode/flags (3GPP/artifacts/runtime). These are configured via flags on each side rather than sent over the wire.
- Symbol size L: To avoid QUIC DATAGRAM size issues on typical paths, prefer L around 1100 bytes. Larger L (e.g., 1300) may exceed path MTU and cause send errors.

## Server usage
```
quicfec-server [flags]
```
Flags:
- -addr string: listen addr (default ":4242")
- -out string: output directory for received file (default "test_data")
- -timeout duration: idle timeout, extended after progress (default 5s)
- Polar flags (must match client when using Polar):
  - -polar-mode string: 3gpp|artifacts|runtime (default "3gpp")
  - -polar-3gpp-table string: path to 3GPP table (default "docs/polar_table_5_3_1_2_1_inverted.txt")
  - -polar-artifacts string: base dir for artifacts (default "tables")
  - -polar-art-e int: artifact epsilon bucket (default 60)
  - -polar-eps float: runtime epsilon for BEC selection (default 0.01)

Examples:
```bash
# Default (Polar 3GPP), longer timeout recommended for big transfers
./bin/quicfec-server -timeout 15s

# Use artifacts instead of 3GPP
./bin/quicfec-server -polar-mode artifacts -polar-artifacts tables -polar-art-e 60 -timeout 15s
```

## Client usage
```
quicfec-client [flags]
```
Flags:
- -addr string: server addr (default "localhost:4242")
- -file string: input file to send (default "test_data/train_FD001.txt")
- -scheme string: fec scheme: rlc|rs|polar (default "polar")
- -N int: block length (default 8)
- -K int: information symbols (default 6)
- -L int: symbol bytes (default 1100)
- -loss float: sender drop probability (Bernoulli) (default 0.005)
- -seed int: RNG seed for dropper (default 1)
- -post-wait duration: timeout to wait for server completion ack after sending (default 2s)
- -pace duration: sleep between DATAGRAM sends to avoid receiver queue overflow (default 300us; 0=disable)
- -block-pause duration: sleep after finishing each block (default 2ms; 0=disable)
- Polar flags:
  - -polar-mode string: 3gpp|artifacts|runtime (default "3gpp")
  - -polar-3gpp-table string: path to 3GPP table (default "docs/polar_table_5_3_1_2_1_inverted.txt")
  - -polar-artifacts string: base dir for artifacts (default "tables")
  - -polar-art-e int: artifact epsilon bucket (default 60)
  - -polar-eps float: runtime epsilon for BEC selection (default 0.01)

## Quick recipes

### Polar (3GPP) with 1% loss and L=1100
Server:
```bash
./bin/quicfec-server -timeout 15s -polar-mode 3gpp
```
Client:
```bash
./bin/quicfec-client -scheme polar -N 8 -K 6 -L 1100 -loss 0.01 -file test_data/small.bin
```
Verify:
```bash
cmp -s test_data/small.bin test_data/small.bin.recv && echo MATCH || echo MISMATCH
```

### RS baseline
Server:
```bash
./bin/quicfec-server -timeout 15s
```
Client:
```bash
./bin/quicfec-client -scheme rs -N 8 -K 6 -L 1100 -file test_data/small.bin
```

### RLC baseline
Server:
```bash
./bin/quicfec-server -timeout 15s
```
Client:
```bash
./bin/quicfec-client -scheme rlc -N 8 -K 6 -L 1100 -file test_data/small.bin
```

## File locations and outputs
- The server writes the received file to: `<out>/<basename>.recv` (default: `test_data/<name>.recv`).
- The client sends metadata over a reliable stream first, then data/parity over QUIC DATAGRAMs, and finally waits for a small completion ack from the server on a new stream (bounded by `-post-wait`).

## Troubleshooting
- SendDatagram error or no progress: reduce `-L` (e.g., 1100) to fit path MTU.
- Timeouts on server: increase `-timeout` (e.g., 15s) to accommodate decode latency and large transfers.
- Mismatch with Polar: ensure both sides use the same `-polar-mode` and related flags.
- Large files with many blocks: if receiver seems to lag, keep `-pace` and `-block-pause` enabled (defaults) to avoid overwhelming the peer's DATAGRAM receive queue.

---
For more details on Polar parameter sources, see the 3GPP table under `docs/` or your artifact set under `tables/`.