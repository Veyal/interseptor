---
name: ui-feature-verification-loop
description: Run a repeatable, evidence-backed browser audit of every Interseptor UI feature and the workflows that connect them before shipping embedded frontend changes.
---

# UI feature verification loop

Use this after changing `internal/control/ui/`, before a UI release, or when an
operator asks for a thorough UI/UX audit.

The Go binary embeds the frontend. Restart a freshly built isolated instance
after every implementation slice; reloading a binary started before the edit
does not exercise the new assets. Use a temporary data directory, unique local
ports, and only generic `example.com` or local-target evidence.

For a mutating full audit, do not choose a port by binding and closing a
"free-port" probe. The audit parent must retain both loopback listening sockets
for the entire run and pass those exact descriptors to its owned child, which
serves without rebinding. Stop the child before closing the parent reservations;
fail closed where descriptor passing is unsupported. This prevents a failed or
replaced child from redirecting audit mutations into another workstation.
Any partial-start cleanup must use a bounded shutdown context so an accepted,
non-terminating request cannot hang the audit before its sentinel cleanup runs.

## One loop

1. Run the Go design-system and UI contract tests before opening the browser.
2. Exercise every panel independently: Proxy/History and Inspector, Intercept,
   Repeater, Intruder, Scanner and Checks, Findings, Map, Notes, Activity, and
   every Settings section including Codecs, OOB, REST/MCP, and Authz entry
   points.
3. For each surface, inspect populated, empty, loading, success, error, retry,
   disabled, selected, and live-update states that the feature supports.
4. Repeat the operator dependency journeys:
   - History → Inspector → Repeater / Intruder / Finding / Map;
   - Intercept acknowledgement → History → Repeater;
   - Repeater / Intruder → Scanner / Finding evidence;
   - Scope and session settings → capture, Repeater, Intruder, and Authz;
   - OOB → generated payload → observed interaction;
   - Activity flow link → the correct History selection.
5. Inject delayed, failed, reordered, and aborted requests. A selected row,
   visible detail, enabled action, editor draft, and acknowledgement must all
   describe the same entity and generation. Live refreshes must not overwrite
   dirty Settings or move focus to a different object.
6. Repeat keyboard and focus checks at 1440×900, 1024×768, and 390×844. Check
   Arrow/Home/End tab behavior, modal traps and focus return, context menus,
   Escape priority, generated controls, visible focus, document overflow, and
   horizontally scrolled dense headers.
7. Repeat with `prefers-reduced-motion: reduce`. Meaning must remain visible in
   text, status, border, icon, selection, or final position; non-essential
   transitions, smooth scrolling, and one-shot animations must be absent.
8. Profile a panel switch, a sustained capture burst, an Intercept queue
   acknowledgement, and Map render/Fit/focus. Record long tasks, interaction
   latency, rendered row/node bounds, console errors, and external requests.
   Use real progress and local traffic; never fabricate progress.
9. Capture the three required viewport screenshots after the final restart and
   compare them with the pre-change baseline.
10. Rerun `go test ./...`, `go test -race ./...`, `go vet ./...`, the no-cgo
    build, JavaScript syntax checks, documentation checks, and the repository's
    ship gate.

Do not call the loop complete while a reproducible state-integrity,
accessibility, keyboard, data-loss, or unreachable-action defect remains.
Cosmetic ideas and competitor parity are not automatically features: implement
only changes that answer a real operator question and fit the single-binary,
passive-first product boundary. Report deferred ideas with the evidence and
reason they were not added.
