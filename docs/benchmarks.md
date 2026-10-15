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

## Store write path

Measured on **2026-10-15**, Apple M4, Go 1.26.5, APFS, `-benchtime 8000x -count 3` (median shown).
"Before" is one SQLite transaction per queued write; "after" is one transaction per chunk of 512.

```bash
go test ./internal/store -bench 'BenchmarkFlowDrain$|BenchmarkFlowCaptureIngest' \
  -benchtime 8000x -run '^$' -count 3
```

| Benchmark | Before | After |
|---|---|---|
| `BenchmarkFlowDrain/batch512` — flusher applying a queued chunk | 129.8µs/write | 25.7µs/write |
| `BenchmarkFlowDrain/batch64` — small chunk | 129.8µs/write | 29.4µs/write |
| `BenchmarkFlowCaptureIngest` — enqueue insert + update, then flush | 261–431µs/flow | 64µs/flow |

In flows per second, the drain figure is roughly 7,700 queued writes per second before and 39,000
after; the end-to-end capture figure is roughly 2,300–3,800 flows per second before and 15,600
after.

A per-write savepoint was measured as the alternative failure policy and rejected: it cost 46µs per
write against 23µs, on every chunk, failing or not, because SQLite keeps a sub-journal while a
savepoint is open.

### Measured against the running binary

The benchmark above measures the flusher with nothing else on the machine. Under real proxy load it
is not SQLite that limits capture but `bodyMu`, which the proxy takes for every finalized body: the
flusher was releasing body ownership once per queued write, so it queued behind capture up to 512
times per chunk. Releasing the whole chunk under one lock hold is what the figures below reflect.

Synthetic traffic through a loopback proxy against a loopback origin, half plain HTTP and half
intercepted TLS, one project per run (Apple M4, ten logical CPUs):

| Load | Requests | Forwarding | Queued writes dropped |
|---|---|---|---|
| 32 clients × 400, releasing per write | 12,800 at 5,389 req/s | 0 failed | 15,360 of 25,600 |
| 32 clients × 400, releasing per chunk | 12,800 at 4,939 req/s | 0 failed | **0** of 25,600 |
| 64 clients × 300 | 19,200 at 6,378 req/s | 0 failed | **0** of 38,400 |
| 128 clients × 200 | 25,600 at 5,791 req/s | 0 failed | 8,192 of 51,200 |
| 256 clients × 150 | 38,400 at 5,929 req/s | 0 failed | 27,648 of 76,800 |

So capture keeps up to roughly 6,400 flows per second on this machine and sheds beyond that, which
is the intended trade: forwarding did not fail a single request in any run, including the ones that
dropped a third of their metadata writes. The issue that prompted the change reported the buffer
filling above roughly 200 flows per second; nothing here reproduces that on this hardware, so treat
200 as a report from a much slower disk.

A 6,400-flow run at 64 clients with 25 **Reclaim space** passes firing concurrently captured all
6,400 flows with no gap in the id sequence, `flows_fts` holding exactly 6,400 matching rows, zero
capture errors, and every GC pass reclaiming nothing — no body written during a walk was lost.

### SQLite pragmas

Measured on **2026-10-15**, Apple M4, Go 1.26.5, against one project database of 40,000 captured
flows, each variant in its own process (`-benchtime 60x -count 3`, median shown). "before" is the
pragma set the store shipped with; each variant adds settings to it.

```bash
go test ./internal/store -bench 'BenchmarkStorePragmas/<variant>/' \
  -benchtime 60x -run '^$' -count 3
```

| Variant | History sort by size | History sort by path | Full-text search | Host size breakdown | Body-GC reference query |
|---|---|---|---|---|---|
| `before` | 286µs | 300µs | 3.35ms | 20.21ms | 14.97ms |
| `cache32` (shipped) | 287µs | 301µs | 3.40ms | **16.33ms** | **11.68ms** |
| `cache64` | 292µs | 301µs | 3.46ms | 16.42ms | 11.77ms |
| `mmap256` | 297µs | 305µs | 3.42ms | 17.41ms | 11.65ms |
| `tempmem` | 295µs | 303µs | 3.42ms | 20.91ms | 15.36ms |
| `cache32mem` | 292µs | 308µs | 3.42ms | 16.50ms | 11.79ms |
| `issue101` (`cache64` + `mmap256` + `tempmem`) | 303µs | 311µs | 3.43ms | 17.49ms | 11.71ms |

Only `cache_size` earns its place. It is worth about 19% on the host size breakdown and about 22% on
the body-GC reference query, the two queries that read the whole flows table. The index-backed reads
— every History sort, and full-text search — are unchanged, because they walk a covering index and
never touched more pages than even the 2 MiB default held; `EXPLAIN QUERY PLAN` confirms no temp
B-tree in those plans, so the premise that keyset sorting spills to disk does not hold here.

`mmap_size` never beat `cache_size` on the scan-heavy queries, cost a few percent on the
index-backed reads, and was slightly worse on the host breakdown. `temp_store` moved nothing at all,
and had nothing left to move on the write path once the flusher stopped opening a savepoint per
write; adding it to `cache_size` (`cache32mem`) changed no figure outside the noise. Both are left
unset, as is the combination the issue proposed, which is the slowest variant on the History sorts.

Raising `cache_size` to 64 MiB per connection was no better than 32 MiB, so the smaller value ships:
four pooled connections put the ceiling at 128 MiB rather than 256 MiB.

Run-to-run spread on the two scan queries is a few percent, so re-measure all variants together
before drawing a new conclusion from a single number.

## Body garbage collection

Measured on **2026-10-15**, Apple M4, with one goroutine capturing bodies as fast as it can while
`GCBodies` runs. `TestGCBodiesDoesNotBlockCaptureDuringWalk` prints the figures under `-v`; its
`gcWalkOrphans` and `gcWalkCaptures` constants set the sample size.

| Unreferenced files on disk | | Before | After |
|---|---|---|---|
| 6,000 (the test's default) | Bodies captured during the pass | 1 | 300 (its cap) |
| | Slowest single capture | 390ms | 1.6–5.8ms |
| 20,000 | Bodies captured during the pass | 1 | 3,574–4,121 |
| | Slowest single capture | 1.64s | 6.1–11.3ms |

Before, the pass held the body lock across the whole directory walk, so capture was frozen for its
duration; the one capture that completed was the one already blocked when the walk ended. After, the
lock is held for a map copy and for each individual unlink, so the worst case a capture waits for is
one `unlink` rather than the whole pass.

The "before" column was reproduced by taking `bodyMu` for the length of the walk again, which is
what the pass used to do.

## TLS leaf minting

Measured on **2026-10-15**, Apple M4, Go 1.26.5, `-benchtime 2000x -count 3` (range shown), ten
logical CPUs.

```bash
go test ./internal/tlsca -bench BenchmarkLeafForHost -benchtime 2000x -run '^$' -count 3
```

| Benchmark | Before | After |
|---|---|---|
| `BenchmarkLeafForHostDistinctHosts` — parallel handshakes to fresh hosts | 83.9–88.1µs | 18.0–18.3µs |
| `BenchmarkLeafForHostCached` — parallel handshakes to a known host | 129–146ns | 71–82ns |
| `BenchmarkLeafForHostSameHostBurst` — 16 parallel connections, one new host | 93.9–98.7µs | 88.9–106.9µs |

The first two improve because minting no longer runs inside an exclusive mutex and cache hits take a
read lock. The third is deliberately unchanged: it is the cost of exactly one mint, which is what
collapsing duplicate concurrent mints for the same host preserves now that the lock no longer
serializes them.

For reference on the same machine, an ECDSA P-256 keygen is 7.3–11.0µs — about a tenth of one mint,
which is why leaf keys are not shared across hosts. See
[Leaf certificate keys](architecture.md#leaf-certificate-keys).

## Measuring the full application

Use a separate project with synthetic local traffic. Record startup time, idle and loaded resident
memory, request throughput, error rate, and UI responsiveness with a stated history size. Keep the
traffic fixture, body sizes, concurrency, and TLS conditions alongside the results.

A fast microbenchmark alone does not establish that the full application meets those goals.
