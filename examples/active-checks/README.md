# Example custom ACTIVE checks

These are ready-to-copy examples of **user-authored active checks** — Starlark scripts that
*send real mutated requests* to confirm a vulnerability, the active twin of the passive checks in
[`../checks/`](../checks/).

## How to use one

1. Copy the `.star` file into `~/.interseptor/active-checks/`
   (create the folder if it doesn't exist — it lives alongside the CA and the passive `checks/`
   folder, shared across all projects).
2. Open the UI → **Scanner → ✎ Custom checks**. The script appears under **CUSTOM · ACTIVE** ⚡.
3. Click it to **Test** against a captured flow, then arm & run an active scan.

Custom active checks fire **only when you arm & run an active scan** against in-scope targets —
they never run passively, exactly like the built-in active probes. See
[`docs/custom-active-checks.md`](../../docs/custom-active-checks.md) for the full API.

## The examples

| File | What it confirms | Severity |
|---|---|---|
| [`error-based-sqli.star`](error-based-sqli.star) | Error-based SQL injection (DB error string in a probed response) | High |
| [`reflected-xss.star`](reflected-xss.star) | Reflected XSS (a marked payload echoed back unescaped) | High |

Both demonstrate the same authoring model: send one or more `probe(payload)` calls at the current
injection `point`, then inspect the response(s) and `return [finding(...)]` when the vuln is
confirmed. Return `[]` (or nothing) when it's not.
