# Benchmarks

The product thesis is "lightweight and instant." This page records how we measure that and the
current numbers. Methodology is reproducible; comparative numbers vs Burp/ZAP are a follow-up that
needs those tools installed (the order-of-magnitude gap is the point — see below).

*Measured 2026-06-22 on an Apple-silicon Mac, Go 1.25, `CGO_ENABLED=0` static binary.*

## Headline numbers

| Metric | Interseptor | Reference (widely reported) |
|---|---|---|
| **Idle RSS** | **~20 MB** | Burp: ~3,500 MB idle; enterprise installs "16–17 GB RAM" |
| **Cold start → serving UI** | **~1 s** (first run also generates the CA) | JVM tools: several seconds + JVM warmup |
| **Binary size** | **~16.7 MB**, one static file, no runtime | Burp/ZAP: JVM + install; HTTP Toolkit: Electron |
| **Capture throughput** | **~444 MB/s**, 1.5 KB + 18 allocs/op | — (we stream; we don't buffer bodies) |

The idle-RSS figure is the differentiator: Interseptor uses roughly **1/100th** of Burp's idle
memory because it's a compiled native binary that streams bodies to disk instead of holding full
HTTP history (with bodies) in a JVM heap.

## How to reproduce

**Capture hot path** (proves bodies stream, not buffer — note the tiny B/op):

```bash
go test ./internal/capture/ -bench BenchmarkTeeBody -benchmem -run '^$'
# BenchmarkTeeBody-10   4740   248933 ns/op   444.26 MB/s   1519 B/op   18 allocs/op
```

**Cold start + idle RSS:**

```bash
CGO_ENABLED=0 go build -o ib ./cmd/interseptor
INTERCEPTOR_NO_BROWSER=1 ./ib &           # time until http://127.0.0.1:9966/ answers
ps -o rss= -p $!                          # idle resident memory (KB)
```

## What this validates

- **Streaming capture:** `BenchmarkTeeBody` allocates ~1.5 KB regardless of body size — RAM does
  not grow with traffic, which is exactly the property Burp lacks.
- **Native start:** sub-second to a usable UI, no JVM warmup.

## Follow-up (tracked in the v2 roadmap)

- Comparative runs **with Burp Suite Pro and ZAP installed** (same machine, same traffic): idle RSS,
  cold start, and large-history (10k+ flow) scroll responsiveness, published as a reproducible script.
- A sustained-throughput proxy benchmark (replay N-thousand requests; assert a RAM ceiling and a
  throughput floor) wired into CI as a regression guard.
