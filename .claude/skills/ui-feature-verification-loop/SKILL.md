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
   For ordered target cards, reject reorder/remove saves and then edit a
   different card. Keep each visible index attached to its original evidence
   references until the cards are rebound. Build structural PATCH payloads from
   copies, block overlapping mutations, and preserve failed intent for Retry.
   A successful structural save must rebind cards even when a separate field
   retains a draft; guard late rendering by the selected finding identity.
   Switch Notes from Preview back to Edit while its save is delayed; the active
   control and visible pane must still agree after acknowledgement. View changes
   that render a local draft must not wait for persistence to finish.
   Reject a Notes save, then attempt a project switch: the draft and Retry must
   survive, with no switch request sent. Repeat with an unselected Finding draft
   and persisted Settings fields. Search/navigation fields must not block a clean
   switch. After acceptance, verify the Projects dialog prevents new edits and
   dismissals until identity confirmation, and restores controls on failure.
   Include Cmd/Ctrl+K: a global command palette must not open over that lock.
   Treat `AbortController.abort()` as a cancellation request, never as proof
   that the wrapped promise settled: race every startup-critical read against
   an independently settling deadline and handle late completion safely.
   Fault-inject both a failed transitive ES-module request and a `fetch` promise
   that ignores its abort signal. A pre-module watchdog must replace any static
   loading shell with one keyboard-reachable recovery action. Repeat these two
   cases in Chromium, Firefox, and WebKit because profile/engine-specific state
   is a common reason one browser hangs while another starts normally.
   Also delay a module past the watchdog threshold and then let it finish: every
   surface made inert by the guard must recover, without restoring disabled
   states owned by feature code. Seed malformed and high-cardinality tab state,
   verify boot remains bounded, preserve the original value until an explicit
   edit, and confirm that the first recovery edit—not the second—is persisted.
   Make `localStorage.setItem` throw and prove the durable project write still
   occurs with persistent feedback. Exercise every tab-creation entry point,
   not only the visible Add button, at the reload-safe limit; imported items
   must have unique IDs and capacity rejection must be atomic. Test project
   names that collided under older key sanitization, including both a uniquely
   migratable value and an ambiguous value that must remain untouched.
6. Repeat keyboard and focus checks at 1440×900, 1024×768, and 390×844. Check
   Arrow/Home/End tab behavior, modal traps and focus return, context menus,
   Escape priority, generated controls, visible focus, document overflow, and
   horizontally scrolled dense headers.
   An inventory count is not interaction coverage: record an open/close result
   for each dialog and disclosure. Distinguish entry through the feature's
   button from a shell-only check using the shared modal helper. Findings
   checks must cover browser Back/Forward, a missing shared link, an open record
   excluded by filters, failed edits, and open HTTP evidence during SSE refresh.
   Exercise floating controls near every viewport edge and after a resize.
   Scroll and focus a custom dropdown trigger before waiting for its position
   to settle, then open it. Focusing an offscreen trigger queues a scroll event
   that can legitimately dismiss a menu opened in the same automation tick.
   Diagnose unexpected dismissal from its event/close stack before weakening
   the assertion. Preserve disclosure IDs so saves retain expanded content.
   Document overflow alone does not prove usability. Check nested scroll areas,
   editor heights and the reachability of trailing actions: fixed-height sibling
   controls can shrink a flex editor to a single line without overflowing the page.
7. Repeat with `prefers-reduced-motion: reduce`. Meaning must remain visible in
   text, status, border, icon, selection, or final position; non-essential
   transitions, smooth scrolling, and one-shot animations must be absent.
8. Profile a panel switch, a sustained capture burst, an Intercept queue
   acknowledgement, and Map render/Fit/focus. Record long tasks, interaction
   latency, rendered row/node bounds, console errors, and external requests.
   Use real progress and local traffic; never fabricate progress.
9. Capture the three required viewport screenshots after the final restart and
   compare them with the pre-change baseline.
   Keep the runtime digest, executed probe hashes and screenshot hashes
   together. Never update the identity on an older screenshot collection to
   satisfy a source-identity test. Store new evidence separately and explicitly
   mark the earlier review as historical.
   Replace machine/device discovery responses with labelled generic fixtures
   before retaining screenshots that would otherwise contain personal host data.
   Run steps 2–9 independently in Chromium, Firefox, and WebKit against fresh
   disposable candidates. Retain engine-qualified cases, screenshots, and
   performance metrics; Chromium-only CDP counters may supplement, but never
   replace, the portable timing and bounded-DOM evidence for the other engines.
   Stabilize a new focused probe in one engine before running the full matrix.
   Flush each case result or exception immediately and retain per-engine results
   as they finish; a later timeout must not erase the earlier diagnostic output.
   Resolve selector, fixture, and browser-driver API mistakes before classifying
   a failed probe as an application defect. Keep each executed probe immutable.
   Change themes through `#themeToggle`: dark mode removes `data-theme`, while
   light mode sets it to `light`. Assigning `data-theme="dark"` creates a state
   the app never uses and can leave controls styled for the wrong theme.
10. Rerun `go test ./...`, `go test -race ./...`, `go vet ./...`, the no-cgo
    build, JavaScript syntax checks, documentation checks, and the repository's
    ship gate.

Do not call the loop complete while a reproducible state-integrity,
accessibility, keyboard, data-loss, or unreachable-action defect remains.
Cosmetic ideas and competitor parity are not automatically features: implement
only changes that answer a real operator question and fit the single-binary,
passive-first product boundary. Report deferred ideas with the evidence and
reason they were not added.
