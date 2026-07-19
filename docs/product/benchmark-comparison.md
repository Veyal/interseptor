# Comparative benchmarks — Interseptor vs Burp & ZAP

*Methodology: same machine, idle RSS after 30 s settle, cold start to first UI response.
Discovery comparison uses an identical 200-word list against a local test server.
Numbers are directional — your host OS and JVM settings will shift absolute values.*

| Metric | Interseptor | Burp Suite Pro | OWASP ZAP |
|---|---|---|---|
| Idle RSS (approx.) | **~20 MB** | ~800 MB–1.5 GB | ~300–500 MB |
| Cold start to usable UI | **~1 s** | ~15–30 s | ~10–20 s |
| Binary + runtime deps | **single static Go binary** | JVM + installer | JVM + installer |
| Content discovery | built-in (scope-aware) | Pro (built-in) | fuzz add-ons |
| AI agent surface | **MCP tools + REST/SSE** | limited / bolt-on | API, no MCP |

## How we measure

See [benchmarks.md](../benchmarks.md) for the reproducible harness (`scripts/bench.sh`,
`BenchmarkInsertFlow`). Re-run locally:

```bash
go test -bench=BenchmarkInsertFlow -benchmem ./internal/store/...
./scripts/bench.sh   # idle RSS + cold start
```

## Why this matters

Bug-bounty hunters and AI-assisted pentesters run the proxy for hours alongside a browser,
an IDE, and an LLM agent. A 40× idle-RSS gap means fewer OOM restarts and a machine that
stays responsive — the core thesis behind Interseptor's native Go architecture.
