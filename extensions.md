---
layout: default
title: Extensions
classification: current
source: docs/extensions.md
---
<p class="eyebrow">CURRENT</p>
# Writing Interseptor extensions

Interseptor has a small, stable **in-process hook API** for extending the tool
without forking it. This is Phase 1 of [PRD-0005](https://github.com/Veyal/interseptor/blob/main/docs/product/prd-0005-extensions.md)
(issue [#25](https://github.com/Veyal/interseptor/issues/25)): first-party Go
packages register hooks at startup. Later phases add UI panel slots and an
untrusted (WASM / Starlark) host; the hook contract described here is the
foundation they build on.

## The hook API — `internal/plugin`

Two lifecycle hooks are available today. Both pass **only IDs and small
scalars** — never `store` or `proxy` types — so an extension can never create an
import cycle and never stall the proxy hot path.

```go
// Fired after a flow reaches its final recorded state (request + response, or a
// terminal error). Fires for every persisted flow, MITM or plain HTTP.
plugin.OnFlowCaptured(func(flowID int64) { ... })

// Fired after the scanner records an issue against a flow.
plugin.OnScanIssue(func(flowID int64, severity, title string) { ... })
```

### Two rules every hook must follow

1. **Never block.** The emitter runs your hook inline, best-effort. By the time
   `OnFlowCaptured` fires the proxy has already answered the client, but a slow
   hook still delays connection reuse — so hand off to a goroutine and return
   immediately.
2. **Never import proxy/control types.** Reach the host through a narrow
   interface you declare yourself (see below). This keeps your extension
   decoupled and unit-testable, and keeps the dependency graph acyclic.

Both emitters snapshot their hook slice under a lock and invoke hooks **outside**
the lock, so registering a hook from inside another hook is safe.

## The official example — a flow annotator

`internal/plugin/annotator` is a complete, tested example extension. It tags
every captured flow whose host matches a configured substring — e.g. auto-label
everything that touches an internal admin host.

It talks to the host through a minimal interface rather than importing the
concrete store:

```go
type Store interface {
    GetFlow(id int64) (*store.Flow, error)
    AddFlowTags(flowID int64, tags []string) ([]string, error)
}
```

`*store.Store` satisfies it. The hook body just spawns a goroutine:

```go
plugin.OnFlowCaptured(func(flowID int64) {
    go annotate(st, flowID, needles, tag)
})
```

### Enabling it

The annotator is wired in `cmd/interseptor` but **off by default**. Opt in with
environment variables:

```bash
# Tag every captured flow whose host contains "internal" or "admin".
INTERSEPTOR_EXT_ANNOTATE_HOSTS="internal,admin" \
INTERSEPTOR_EXT_ANNOTATE_TAG="internal-host" \
  interseptor
```

`INTERSEPTOR_EXT_ANNOTATE_TAG` defaults to `annotated`. With no
`INTERSEPTOR_EXT_ANNOTATE_HOSTS` set, the extension is never registered.

## Writing your own

1. Add a package under `internal/plugin/<name>/`.
2. Declare a narrow `Store`-style interface for exactly the host calls you need.
3. Expose an `Enable(...)` that registers your hooks via `plugin.OnFlowCaptured`
   / `plugin.OnScanIssue`, guarding against a no-op config so an accidental
   enable can't misfire.
4. Do real work (DB, network, notifications) in a goroutine.
5. Wire `Enable` into `cmd/interseptor`, gated behind an opt-in setting or env
   var so default behavior is unchanged.
6. Unit-test the logic against a fake store — no proxy needed. See
   `internal/plugin/annotator/annotator_test.go`.

A copy-paste starting point lives in [`examples/extensions/hello/`](https://github.com/Veyal/interseptor/tree/main/examples/extensions/hello).

## Roadmap

- **Phase 2** — declared UI panel slots (ES module under
  `~/.interseptor/extensions/`), so an extension can add a tab, not just a hook.
- **Phase 3** — WASM / Starlark host for untrusted third-party logic.

See [PRD-0005](https://github.com/Veyal/interseptor/blob/main/docs/product/prd-0005-extensions.md) for the full plan.

