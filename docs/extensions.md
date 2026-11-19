# Writing Interseptor extensions

Interseptor has two extension layers for extending the tool without forking it (see [PRD-0005](product/prd-0005-extensions.md), issue [#25](https://github.com/Veyal/interseptor/issues/25)):

- **Phases 2 & 3 — Copy Abilities (`internal/abilities`, **Configure → Abilities**):** installable ability folders or archives that inject UI sidebar panels, flow inspector tabs, context-menu actions, command-palette commands, sandboxed Starlark `flow.captured` hooks, and MCP actions under a SHA-256 quarantine trust model. See **[Copy Abilities](abilities.md)**.
- **Phase 1 — In-process Go hooks (`internal/plugin`):** the low-level Go hook registry described below, used by first-party Go packages at startup (including the Copy Abilities Starlark hook bridge).

## The hook API — `internal/plugin`

One lifecycle hook is emitted today. Hooks pass **only IDs and small
scalars** — never `store` or `proxy` types — so an extension can never create an
import cycle and never stall the proxy hot path.

```go
// Fired after a flow reaches its final recorded state (request + response, or a
// terminal error). Fires for every persisted flow, MITM or plain HTTP.
plugin.OnFlowCaptured(func(flowID int64) { ... })
```

`plugin.OnScanIssue` remains in the registry for source compatibility, but nothing emits it: the
passive scanner it reported on was removed in favour of Playbooks, which the connected agent runs
with the ordinary tools.

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

A copy-paste starting point lives in [`examples/extensions/hello/`](../examples/extensions/hello).

## Copy Abilities (Phases 2 & 3)

Phases 2 and 3 of [PRD-0005](product/prd-0005-extensions.md) shipped as **Copy Abilities** (`internal/abilities`), managed in **Configure → Abilities** and via `interseptor ability`.

- **UI slots (Phase 2)** — declared sidebar menus/panels, flow inspector tabs, context-menu actions, and command-palette entries using ES modules (`mount(container, ctx)` / `unmount(container)`).
- **Sandboxed Starlark host (Phase 3)** — quarantined-until-trusted `flow.captured` hooks (`on_flow(flow)`) bridged through `internal/plugin` and on-demand/MCP actions (`run(ctx)`).

If you want to extend Interseptor without recompiling Go code, build or install a Copy Ability following [Copy Abilities](abilities.md). Phase 1 in-process Go hooks remain in `internal/plugin` for compiled first-party integrations.
