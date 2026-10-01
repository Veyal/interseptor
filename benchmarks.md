---
layout: default
title: Benchmarks
classification: reference
source: docs/benchmarks.md
---
# Benchmarks

These are historical measurements recorded on **2026-06-22**, before the current release.
They are retained as a baseline, not as performance claims for v2.1.0. Current measurements
need a fresh run against the release being evaluated.

## Historical baseline

The original run used an Apple-silicon Mac, Go 1.25, and a `CGO_ENABLED=0` build.

| Metric | Recorded result |
|---|---|
| Idle resident memory | Approximately 20 MB |
| Cold start to serving the UI | Approximately 1 second; first run also generates the CA |
| Binary size | Approximately 16.7 MB |
| Capture microbenchmark throughput | 444.26 MB/s |
| Capture microbenchmark allocations | 1,519 B/op and 18 allocations/op |

The capture result measures one component. It does not measure complete proxy throughput, TLS,
database writes, browser responsiveness, or memory growth over a long session. No controlled
same-machine comparison with other proxy products is included here.

## Capture microbenchmark

Run the existing benchmark from the source tree:

```bash
go test ./internal/capture/ -bench BenchmarkTeeBody -benchmem -run '^$'
```

Record the Git tag or commit, Go version, operating system, hardware, command, and full output
with any new measurements. Compare results under the same conditions.

## Measuring the full application

Use a separate project with synthetic local traffic. Record startup time, idle and loaded resident
memory, request throughput, error rate, and UI responsiveness with a stated history size. Keep the
traffic fixture, body sizes, concurrency, and TLS conditions alongside the results.

A fast microbenchmark alone does not establish that the full application meets those goals.

