# Writing custom ACTIVE checks

> The passive twin of this guide is [`docs/custom-checks.md`](custom-checks.md) — read that first if
> you haven't; this page only covers what's *different* about active checks.

Interceptor's **active** scanner is extensible too: drop a check written in **Starlark** into your
active-checks folder and it runs alongside the built-in active probes when you arm & run an active
scan, sending real mutated requests to confirm vulnerabilities. This page is the **standard** every
active check is written against.

- **Where checks live:** `~/.interceptor/active-checks/*.star` (global — shared across all projects,
  like the CA). The folder is created on first save; you can also create it by hand.
- **When they run:** only when you **arm & run an active scan** (the **Active scan** dialog, or
  `POST /api/activescan/start`). They never run passively. Files are re-read each run, so editing a
  check takes effect immediately — no restart.
- **Why it's safe to share checks:** Starlark is deterministic and **sandboxed** — a check cannot
  read files, open sockets, read the clock, or import anything. The *only* way a check can talk to
  the network is the `probe(payload)` callback we hand it, which goes through the engine (recorded,
  session-auth applied, scope-enforced, budget-counted).

> **Active checks send real traffic.** Authoring is unrestricted, but *execution* is consent-gated:
> a custom active check only fires when you explicitly arm & run an active scan against an in-scope
> target — exactly like the built-in probes. Always scan targets you're authorized to test.

## The contract

An active check is a `.star` file that defines one function:

```python
def check(point, baseline, probe):
    # send one or more probes at `point`, then return finding(...) or []
    return []
```

The file name (without `.star`) is the check's id. `check` is called **once per injection point** of
every in-scope target in the scan. Return a list of `finding(...)`; return `[]` (or `None`) when
there's nothing to report.

### The three arguments

| Arg | What it is |
|---|---|
| `point` | The injection point being tested (a query/form/json parameter, a path segment, a cookie, a request header, or the whole XML body). |
| `baseline` | The **un-mutated** response — what a normal request returns. Use it to suppress false positives (e.g. "the marker was *already* in the body"). |
| `probe`  | A callback: `probe(payload)` sends one mutated request — with `payload` injected at `point` — and returns its response. |

## The `point` object

| Field | Type | Notes |
|---|---|---|
| `point.kind` | str | `query`, `form`, `json`, `path`, `cookie`, `header`, or `body` (`body` is the whole XML body, for XXE-style checks). |
| `point.name` | str | The parameter/cookie/header name (e.g. `id`). For `path` points, the segment index; for `body` points, `_xml`. |
| `point.value` | str | The parameter's original value. |

## The response object (`baseline` and each probe result)

| Field | Type | Notes |
|---|---|---|
| `r.status` | int | HTTP status (0 if the request never completed). |
| `r.body` | str | response body (bounded). |
| `r.headers` | dict | canonicalized header name → first value. |
| `r.duration_ms` | int | round-trip time in milliseconds. |
| `r.flow_id` | int | the History flow id of this request (probe results only — useful for evidence). |

Method:

| Call | Returns |
|---|---|
| `r.header(name)` | header value (**case-insensitive**), or `""` if absent. |

## Builtins

| Builtin | Description |
|---|---|
| `probe(payload)` | Send one mutated request at `point`. **Real traffic** — recorded in History, session-auth applied, counts against the run's request budget. |
| `finding(severity, title, detail="", evidence="", fix="")` | construct one finding. `severity` ∈ `high` / `medium` / `low` / `info` (`critical` → high; anything else → info). |
| `re_search(pattern, text)` | first regex match (RE2 syntax) as a string, or `None`. |

## Example

```python
# ~/.interceptor/active-checks/error-based-sqli.star
def check(point, baseline, probe):
    r = probe("'")                          # ⚡ sends a real mutated request
    if re_search("(?i)SQL syntax", r.body):
        return [finding(
            "High",
            "Error-based SQL injection (custom)",
            detail="Injecting a quote into " + point.kind + " `" + point.name + "` triggered a DB error.",
            evidence=r.body[:120],
            fix="Use parameterized queries for all database access.",
        )]
    return []
```

More ready-to-copy examples ship in [`examples/active-checks/`](../examples/active-checks/).

## Authoring in the UI

Open **Scanner → ✎ Custom checks**. Under **CUSTOM · ACTIVE** ⚡ you can create, edit, delete, and
**Test** active checks. **Test** compiles your source and runs it against the first injectable
parameter of a captured flow (your latest in-scope one, or a flow you pick), sending a handful of
real probes so you can iterate before saving. (The AI **Describe** tab is passive-only and is hidden
for active checks.)

## Limits & safety

- **Sandboxed:** no file/network/clock access, no `load()`, no imports. The only network egress is
  the `probe()` callback, which is mediated by the engine.
- **Budgeted:** every `probe()` call counts against the run's request budget, so a runaway script
  can't infinite-loop the target. (The in-UI **Test** path is capped at ~6 probes.)
- **Step-bounded:** each `check()` call is also capped at a few million execution steps, so a pure
  compute loop aborts that one check instead of hanging the scan.
- **Isolated failures:** a check that fails to compile or errors at runtime is logged and skipped —
  it never aborts the scan or the other checks.
- **Scope-enforced:** probes are sent through the same sender as the built-in probes, so they obey
  your target scope, the circuit breaker, and the "never attack our own listeners" guard.
- **One finding per point:** a custom active check currently surfaces its **highest-severity**
  finding per injection point (the engine's *Hit* model is one-per-point). That covers the vast
  majority of real checks (confirm-one-vuln); if you later want a single check to emit multiple
  distinct findings per point, that's a small engine extension.
