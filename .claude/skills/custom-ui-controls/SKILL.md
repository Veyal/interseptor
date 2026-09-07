---
name: custom-ui-controls
description: Preserve Interseptor's app-rendered controls and concise contextual help.
---

Use the shared custom dropdown enhancer; hidden select elements are only
value/change adapters. Never expose an OS select menu or add a native
alert/confirm/prompt fallback. Retain semantic buttons, inputs and labels with
the shared custom appearance and focus states.

Keep setting values, save/error state and material consequences visible. Place
optional explanations in a themed hint or keyboard/touch-accessible disclosure.
Do not replace essential instructions with hover-only text. `hints.js` converts
static and dynamically assigned title hints into the app tooltip.

Preserve existing DOM IDs, async ownership and project-readiness guards when
moving controls. Browser tests must interact with the visible custom trigger
and listbox, never force a value into the hidden select adapter. A runtime change
requires fresh source-bound browser audit evidence; never update its digest
without executing the audit.

Edit/refresh guards must recognize focused combobox triggers and open menus,
including portaled menus outside the editor subtree. Otherwise a previous
field's save can replace a dropdown before its selection completes.

When replacing the mobile tab rail, keep workspace startup warnings and recovery
actions outside the hidden rail. Return focus to the visible navigation control
after dismissing a warning. Settings selectors must query `button[data-sec]`,
not every button in the navigation: the custom dropdown injects its own buttons.

Editor drafts and acknowledged server values have different ownership. Retain a
failed per-request note draft across selection changes, and let an old save
acknowledgement clear only its own draft. Verify A-saving → B-edited → A-saved
leaves B visible and unsaved. Use mocked UI states for presentation/recovery
checks; keep their evidence separate from full operational release audits.

Keep steady-state navigation spacing on `.tab`, not a loading-only selector.
Contrast-check actual filled label recipes (`.badge`, `.ai-tag`) as well as token
pairs: a text-safe light accent cannot serve as the fill behind white text.
Screenshot fixtures must keep list data and diagnostic/statistics state coherent;
preserve diagnostic variants separately rather than hiding real warnings.

Keep palette transport values compatible with the API (tag colors use hex).
Theme tokens belong in rendering, not saved payloads. Exercise every preset and
Clear through the visible menu, then verify persistence and light/dark contrast.

Open a short context menu before a long one when checking placement. Reset the
previous inline width and measure wrapped height before positioning. Check text
Range bounds against adjacent values and the row, not only menu-box overflow.
Labels must remain readable while secondary values may ellipsize. Verify focus
returns to the trigger after dismissal and survives an acknowledged tag refresh.
