---
name: embedded-ui-async-state
description: Preserve authoritative state across overlapping requests, live events, project-scoped hydration, and lazy UI modules in Interseptor's embedded ES-module frontend.
---

# Embedded UI async state

Use this when changing asynchronous reads, live updates, project-scoped browser
storage, lazy feature imports, or editors that persist server-backed drafts.

- Give every latest-request-wins path a generation token. Compare the token
  after each await, including when the selected entity ID is unchanged.
- Include the selection generation in dependent body or detail renderers so a
  re-selection invalidates work started for an older snapshot.
- Keep successful values, authoritative empty values, and read failures
  distinct. Never persist a local fallback after a failed hydration read.
- Block pagination while its first page is refreshing. A failed refresh must
  not leave the old rows eligible for paging with new filters.
- Guard full-state mutation responses with the same generation as live events;
  a delayed POST snapshot must not overwrite a newer SSE queue.
- Resolve the active project before importing any module that reads
  project-scoped storage during evaluation. Route every cross-feature entry
  point through one shared readiness-aware loader rather than importing the
  feature directly from a second module.

Add focused source-contract coverage for each generation, readiness boundary,
and persistence guard because the embedded UI has no frontend build step.
