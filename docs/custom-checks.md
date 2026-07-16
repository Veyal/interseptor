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

Methods:

| Call | Returns |
|---|---|
| `flow.req_header(name)` / `flow.res_header(name)` | header value (**case-insensitive**), or `""` if absent |
| `flow.req_header_all(name)` / `flow.res_header_all(name)` | **all** values for a header as a list (use for multi-`Set-Cookie`, etc.); `[]` if absent |
| `flow.query_param(name)` | query-string value, or `""` |

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

## See also

- [Writing custom **ACTIVE** checks](custom-active-checks.md) — the active twin of this page: send
  real mutated requests with `probe(payload)` to *confirm* a vulnerability (SQLi, XSS, …), gated
  behind arming an active scan.
