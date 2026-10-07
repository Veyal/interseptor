---
name: varstore-resolver
description: Rules for Interseptor's collection variable resolver (internal/varstore) and the redact secret registry, so UI preview, runner, CLI and MCP never diverge or leak secrets.
---

# Variable resolver

- One resolver only: `internal/varstore`. Never add a second `{{}}` implementation in JS or another package.
- Precedence narrow to wide: local > data > environment > folder (inner first) > collection > global. Add folder layers inner to outer; `NewStack` sorts by scope only (stable).
- Unresolved defaults to `PolicyBlock` (Result.Err). `PolicyLiteral`/`PolicyEmpty` must be an explicit per-request opt-in.
- Values are templates. Limits: depth 8, 10000 expansions, 1 MiB output; hitting the output/work limit returns `ErrTooLarge` with an empty Value. Cycles and depth are Problems plus unresolved, never panics.
- Determinism: inject `Clock` and a seeded `Rand` (`--seed`). Dynamic values are final and never re-expanded.
- Secrets: pass a `redact.Registry` in Options; every secret used (and pipe derivations) is registered. Call `Stack.RegisterSecrets` for unused ones. Results hold names/scopes in `Uses`, never values. Mask any text shown, logged or exported through `Registry.Mask`; values under 6 chars are not tracked.
- Tests: use canary strings and `FuzzResolve` (`go test -fuzz FuzzResolve ./internal/varstore`).
