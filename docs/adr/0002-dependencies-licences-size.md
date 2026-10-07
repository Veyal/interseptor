# ADR 0002: New dependencies, licences, binary size

Status: accepted (goja and yaml.v3 pre-approved by the owner). Date: 2026-10-07.

## Added in WP0 (go.mod)
| Module | Version | Licence | Role |
|---|---|---|---|
| github.com/dop251/goja | v0.0.0-20261006212518-44620e89763c | MIT | JS engine (direct) |
| github.com/dlclark/regexp2/v2 | v2.8.1 | MIT | goja regexp (lookbehind etc.) |
| github.com/go-sourcemap/sourcemap | v2.1.3+incompatible | BSD-2-Clause | goja stack traces |
| github.com/google/pprof | v0.0.0-20250317173921-a4b03ec1a45e | Apache-2.0 | goja `profile` import |

All pure Go, no cgo. `gopkg.in/yaml.v3` (MIT/Apache-2.0) is approved but NOT added here; the YAML WP adds it. (Observation: `go mod tidy` also resolved `goccy/go-yaml` and `Masterminds/semver` downloads as transitive test deps of goja; they did not enter go.mod.)

## Binary size (measured, `CGO_ENABLED=0 go build ./cmd/interseptor`, darwin/arm64)
- Before: 28,987,730 bytes
- After (with `internal/jsrt` linked): 40,183,858 bytes
- Delta: +11,196,128 bytes (+38.6%, about +10.7 MiB).

This is larger than the plan assumed. Accepted by the owner decision (pure-Go single binary is the priority); flagged for review. Levers if it matters: build with `-ldflags='-s -w'` for release, and avoid pulling `pprof/profile` (goja links it via its profiler).
