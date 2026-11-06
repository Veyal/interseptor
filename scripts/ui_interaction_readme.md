# UI interaction audit (`ui_interaction_audit.py`)

A repeatable, committed browser sweep over the control UI's *interactions* — not just
that panels render, but that the workflows behave: History filter/search/copy-as,
every response view (including the sandboxed Render frame), Repeater, Intruder,
Scanner, Findings create/rename/persist, Collections, per-flow notes, theme,
identities, intercept hold → edit → forward, the command palette and the phone dock.

It seeds its own traffic through the instance's proxy, so a fresh throwaway project is
enough. Nothing is read from or written to a real engagement.

## Start a throwaway instance

Ports **9966/8080 are refused** by the script: those are the product defaults where a
real user instance lives, and this audit creates findings, notes, identities, settings
and hundreds of flows. Use spare ports instead:

```bash
go build -o /tmp/ui-audit-bin ./cmd/interseptor
INTERSEPTOR_NO_UPDATE_CHECK=1 INTERSEPTOR_NO_BROWSER=1 /tmp/ui-audit-bin \
  --data-dir /tmp/ui-int-audit --project demo \
  --control-port 19176 --proxy-port 19177 --headless &

python3 scripts/ui_interaction_audit.py \
  --base-url http://127.0.0.1:19176 --proxy 127.0.0.1:19177 \
  --json /tmp/ui-audit.json
echo "exit: $?"          # 0 = all checks passed, 1 = a check failed, 2 = refused to run
```

## Options

| Flag | Meaning |
|---|---|
| `--base-url` | control UI origin (must be loopback unless `--allow-remote`) |
| `--proxy` | proxy listener the seeded traffic goes through, `host:port` |
| `--target` | target origin; omit it and the script starts its own loopback target server on an ephemeral port |
| `--chrome` | Chrome binary; defaults to `$UI_AUDIT_CHROME`, then `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome` |
| `--json` | write the machine-readable report (per-check PASS/FAIL, console errors, 5xx) |
| `--headed` | run Chrome with a visible window (debugging) |
| `--allow-remote` | permit a non-loopback `--base-url` (only for a disposable instance you control) |

Requirements: `python3` + `playwright` only (no Playwright browser download — the
script drives the system Chrome through `executable_path`, because Playwright's
bundled browsers are not installed here).

## What it covers (53 checks)

1. **Boot** — every rail tab present/enabled, every panel activates visibly with
   content, no uncaught exception, no unexpected console error during the tour.
2. **Layout** — no horizontal overflow on any panel at 1440x900, 1024x768, 390x844.
3. **DOM** — no duplicate element ids anywhere in the document.
4. **Seeding** — GET+query, POST JSON, POST form, 404, redirect, 300 KB body and an
   HTML page are captured through the proxy; no 5xx among the captured flows.
5. **History** — list, inspector request line + response body, all response views
   (raw/headers/json/decoded/hex/pretty), status filter 4xx, search, clearing search,
   copy-as menu, row context menu.
6. **Render** — mounts a sandboxed `srcdoc` frame, carries the page content, and the
   sandbox does **not** include `allow-scripts`.
7. **Repeater** — receives the inspector's flow and sends it for a 200.
8. **Intruder** — a 3-payload sniper run completes and the results table lists all 3.
9. **Scanner** — a run produces issues for the seeded flows and the list opens one.
10. **Findings** — editing gate on, create, rename through the edit-mode affordance,
    and the renamed title survives a reload.
11. **Collections** — New prompts for a name; the collection survives a reload.
12. **Notes** — View/Edit segments, and a per-flow note survives a reload.
13. **Theme** — the toggle changes the theme and the choice survives a reload.
14. **Identities** — the sheet saves a new identity, closes, and Escape closes it.
15. **Intercept** — toggle, request held in the queue, held editor shows the message,
    an edited `#heldRaw` forwards the edit to the target (verified at the seed target),
    the item leaves the queue, drop offers the Undo toast and Undo keeps it held.
16. **Map** — the graph view renders endpoint nodes (Tree is the default view).
17. **Command palette** — `#cmdk` opens with Ctrl+K *and* Cmd+K, `f:` lists flows,
    Enter opens `#flowDrawer`, and the drawer closes again.
18. **Phone** — the rail is hidden at 390x844, the bottom dock (`#dock` groups +
    `#dockSeg`) switches panels, and the target panel renders.

## Known expected console message

Mounting the Render view for the seeded HTML page logs **one** console error:

```
Blocked script execution in 'about:srcdoc' because the document's frame is sandboxed
and the 'allow-scripts' permission is not set.
```

That is the sandbox doing its job (check 6 asserts `allow-scripts` is absent), so the
script whitelists exactly this message and fails on anything else. It is recorded
under `expected_console` in the JSON report.

## Notes and caveats

- 786 lines, 53 checks, stdlib + `playwright` only. The size is the per-check
  interaction code; trimming further would mean dropping checks.
- Re-runs are safe: Findings/Collections/Identities are named with the run's
  `HHMMSS`, so a second run against the same instance stays at 53/53 (verified).
- The audit is deliberately self-contained: it starts its own target HTTP server, so
  no external network or example data is involved.
- If the UI genuinely breaks mid-flow, Playwright raises and the run stops after
  recording `audit: run completed without an exception` with the exception text — the
  last PASS line names the section that hung.
