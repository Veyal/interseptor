# Writing custom scanner checks

> The control UI embeds [`internal/control/checks_reference.md`](../internal/control/checks_reference.md)
> for the in-app **Docs** tab — keep both files in sync when editing this guide.

Interseptor's passive scanner is extensible: drop a check written in **Starlark** (a small,
Python-like language) into your checks folder and it runs on every scan, right alongside the
built-in checks. This page is the **standard** every check is written against.

- **Where checks live:** `~/.interseptor/checks/*.star` (global — shared across all projects, like the CA).
- **When they run:** every time you **Run scan** (or `POST /api/scanner/run`). Files are re-read
  each run, so editing a check takes effect immediately — no restart.
- **Why Starlark:** it's deterministic and **sandboxed** — a check cannot read files, open network
  connections, read the clock, or import anything. That makes checks safe to download and share.

## The contract

A check is a `.star` file that defines one function:

```python
def check(flow):
    # inspect `flow`, return a list of finding(...) — or [] for "nothing found"
    return []
```

The file name (without `.star`) is the check's id. `check` is called once per captured in-scope
flow. Return a list of `finding(...)`; return `[]` (or `None`) when there's nothing to report.

## Test a check before saving it

`POST /api/checks/test` compiles source and runs it against one captured flow without writing
anything:

```bash
curl -s -X POST http://127.0.0.1:9966/api/checks/test \
  -H 'Content-Type: application/json' \
  -d '{"source":"def check(flow):\n    print(flow.path)\n    return []\n"}'
```

Body: `{source, flowId?}` — omit `flowId` and it uses the most recent captured flow. The response is
`{findings, logs, flowId}`, or `{error, logs}` when the check fails to compile or errors at runtime.
A bad check is always a `200`, so a caller can show the error inline.

`logs` is the check's `print()` output, in the order it happened: module-level prints from the
compile first, then the run's. It comes back even when the check errored — what it printed before
failing is usually the reason. Over MCP the same thing is `test_check`.

## The `flow` object

| Field | Type | Notes |
|---|---|---|
| `flow.method` | str | `GET`, `POST`, … |
| `flow.scheme` | str | `http` / `https` |
| `flow.host` | str | hostname |
| `flow.port` | int | |
| `flow.path` | str | path + query, e.g. `/search?q=1` |
| `flow.status` | int | response status (0 if the request never completed) |
| `flow.mime` | str | response Content-Type |
| `flow.req_body` / `flow.res_body` | str | bodies (bounded) |
| `flow.req_headers` / `flow.res_headers` | dict | canonicalized name → first value |
| `flow.query_params` | dict | every query parameter, url-decoded, as name → **list** of values. A list because a query string can repeat a name meaningfully (`?id=1&id=2`) and a check that enumerates parameters has to see both. Iteration follows the order they appear in the query string. `{}` when there is no query string. |
| `flow.cookies` | dict | request cookies parsed from the `Cookie` header, as name → value. A plain string per name, not a list: unlike a query string a `Cookie` header does not repeat names meaningfully, so the first occurrence wins. Malformed segments are skipped. `{}` when there is no `Cookie` header. |

Methods:

| Call | Returns |
|---|---|
| `flow.req_header(name)` / `flow.res_header(name)` | header value (**case-insensitive**), or `""` if absent |
| `flow.req_header_all(name)` / `flow.res_header_all(name)` | **all** values for a header as a list (use for multi-`Set-Cookie`, etc.); `[]` if absent |
| `flow.query_param(name)` | first value of one query parameter, or `""`. Use `flow.query_params` to enumerate them. |

## Builtins

| Builtin | Description |
|---|---|
| `finding(severity, title, detail="", evidence="", fix="")` | construct one finding. `severity` ∈ `high` / `medium` / `low` / `info` (`critical` → high; anything else → info). |
| `re_search(pattern, text)` | first regex match (RE2 syntax) as a string, or `None`. |
| `json_decode(text)` | parse JSON into dicts/lists/strings/ints/floats/bools/`None`. |
| `json_encode(value)` | serialize a Starlark value to a compact JSON string. |
| `b64decode(text)` / `b64encode(text)` | base64 decode/encode. |
| `url_decode(text)` / `url_encode(text)` | percent-encoding decode/encode (query escaping). |
| `hash(algo, text)` | hex digest; `algo` ∈ `sha256`, `sha1`, `sha512`, `md5`. |
| `hmac(algo, key, message)` | lowercase-hex HMAC digest; same algorithms as `hash`. |
| `print(...)` | Starlark's own `print`. Its output is captured, not discarded: `POST /api/checks/test` returns it as `logs`, and during a scan it goes to the server log labelled with the check id and flow id. This is how you debug a check that returns an empty list. Capture is bounded at 200 lines or 32 KiB per run, then cut with a truncation notice. |

## Example

```python
# ~/.interseptor/checks/missing-hsts.star
def check(flow):
    if flow.scheme == "https" and not flow.res_header("Strict-Transport-Security"):
        return [finding(
            "medium",
            "Missing Strict-Transport-Security (HSTS)",
            evidence="(no HSTS response header)",
            fix="Send Strict-Transport-Security: max-age=63072000; includeSubDomains.",
        )]
    return []
```

More ready-to-copy examples live in [`examples/checks/`](../examples/checks/).

## Limits & safety

- **Sandboxed:** no file/network/clock access, no `load()`, no imports. Checks are pure functions of
  the flow you pass in.
- **Bounded:** each `check()` call is capped at a few million execution steps, so a runaway loop
  aborts that one check instead of hanging the scan.
- **Isolated failures:** a check that fails to compile or errors at runtime is logged and skipped —
  it never aborts the scan or the other checks.
- **Bounded output:** `print()` is captured per run, up to 200 lines or 32 KiB, then cut with a
  truncation notice. A check cannot flood memory or the log with output.
- **Run in parallel:** a scan runs one goroutine per CPU, so your check runs on several flows at
  once. Checks are pure functions of the flow handed to them and hold no state between calls, so
  this needs nothing from you — but a check that depends on being called in flow order never had
  that guarantee and now definitely does not have it.
