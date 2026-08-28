---
name: technical-console-ui-system
description: Maintain Interseptor's operator-first visual system across the embedded control UI. Use when changing login, shell chrome, navigation, toolbars, panels, tables, forms, dialogs, or state surfaces.
---

# Technical console UI system

Interseptor is a local security workstation, not a marketing site. UI changes
should make interception, evidence review, replay, and verification easier to
scan under pressure.

## Visual language

- Use the shared tokens in `internal/control/ui/app.css`; do not introduce
  page-local colours, external fonts, or remote image dependencies.
- Keep the base matte and near-black in dark mode, with layered framed surfaces
  for elevation. Light mode must remain independently readable.
- Reserve emerald for active, ready, or security-relevant signals. Severity
  colours keep their semantic roles; avoid neon washes and multicolour chrome.
- Use the proportional UI face for prose and controls. Use the monospace face
  for methods, URLs, status codes, raw HTTP, paths, and operational metadata.
- Prefer thin rails, inset rules, corner/frame cues, and a very subtle grid to
  large decorative backgrounds or rounded dashboard cards.
- Follow the [UI motion specification](../../../docs/ui-motion-spec.md) for
  allowed motion, shared tokens, performance limits, and reduced-motion behavior.
  Never use video on the login gate. A static visual must be low contrast,
  self-contained, and leave the access-key action as the clear focal point.

## Workflow

1. Preserve the existing interaction and keyboard/accessibility contracts.
2. Add or update a design-system test before changing global tokens or shared
   primitives.
3. Edit the existing selector block rather than appending duplicate selectors;
   `TestUIStylesheetHasNoDuplicateSelectors` enforces this.
4. Check both themes, narrow layouts, loading/error/empty states, and dense
   request/response views.
   - At narrow widths, inspect the right edge of descendants as well as the
     document scroll width. A clipped toolbar can be unusable even when the
     page itself reports no overflow.
   - Main-tab state and focus must commit synchronously. Apply finite entrance
     motion after the panel switch; do not defer ARIA/tab state inside a View
     Transition callback.
5. Run the focused UI tests, then `go test ./...`, `go test -race ./...`,
   `go vet ./...`, and a no-cgo build before release.

Keep the embedded UI self-contained: it must work on localhost, in an air-gapped
engagement, and without leaking an operator's activity to a third-party asset
host.
