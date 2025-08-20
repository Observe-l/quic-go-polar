# QUIC + FEC helper (fecquic)

This small package wires the FEC codecs in `github.com/quic-go/quic-go/fec` into QUIC DATAGRAMs, so you can send and receive data using XOR, Random Linear Codes (RLC) or Polar-like parity.

It uses a tiny control unidirectional stream to announce parameters, then transmits K data chunks plus R parity chunks per block via DATAGRAM frames. The receiver attempts recovery per block on the fly and returns the recovered payload.

## Why a helper package

The core quic-go library intentionally keeps FEC out of the transport. This helper provides a simple, opt-in API without modifying the transport internals.

## Quick start

- Ensure you dial / listen with `EnableDatagrams: true` in `quic.Config`.
- Pick parameters: `Scheme` (xor|rlc|polar), `K`, `R`, and `ChunkLen`.
- Call `fecquic.Send` on the sender and `fecquic.Receive` on the receiver.

```go
package main

import (
    "context"
    "crypto/tls"
    "fmt"
    quic "github.com/quic-go/quic-go"
    "github.com/quic-go/quic-go/fec"
    "github.com/quic-go/quic-go/fecquic"
)

func server(addr string) error {
    ln, err := quic.ListenAddr(addr, &tls.Config{Certificates: []tls.Certificate{generateSelfSigned()}}, &quic.Config{EnableDatagrams: true})
    if err != nil { return err }
    defer ln.Close()
    conn, err := ln.Accept(context.Background())
    if err != nil { return err }
    defer conn.CloseWithError(0, "")
    data, stats, err := fecquic.Receive(context.Background(), conn)
    if err != nil { return err }
    fmt.Println("got", len(data), "bytes; frame completion:", stats.FrameCompletion)
    return nil
}

func client(addr string, payload []byte) error {
    conn, err := quic.DialAddr(context.Background(), addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"fec-demo"}}, &quic.Config{EnableDatagrams: true})
    if err != nil { return err }
    defer conn.CloseWithError(0, "")
    p := fecquic.Params{Scheme: fec.RLC, K: 8, R: 2, ChunkLen: 900}
    return fecquic.Send(context.Background(), conn, payload, p)
}
```

Notes:
- For XOR, `R` is forced to 1. For RLC / Polar, increase `R` to cope with expected loss.
- `ChunkLen` should be small enough to fit in a single QUIC packet together with headers.

## API

- `Send(ctx, conn, data, Params) error`
- `Receive(ctx, conn) (data []byte, stats Stats, err error)`

`Stats` contains `FrameCompletion` (fraction of data chunks recovered, ignoring zero padding) and `ReceivedBytes` (size of the returned payload after trimming to the original file size).

## Caveats

- This is an application-layer FEC. It doesn't retransmit lost datagrams.
- Congestion control still applies to DATAGRAMs.
- Choose `K`, `R`, and `ChunkLen` to match network conditions and PMTU (or disable DF/PMTUD at your own risk).
