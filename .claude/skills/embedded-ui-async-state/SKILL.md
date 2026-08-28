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
- Serialize full-state mutations that can begin from the same generation so a
  faster earlier response cannot invalidate a later user write.
- Do not put emergency or safety controls behind an unrelated, potentially
  unbounded persistence queue. Use independent mutation lanes when the server
  operations have different state ownership or notification behavior.
- Merge field-scoped acknowledgements into the latest authoritative snapshot;
  never replace queue or selection state with a response that owns only filter
  or preference fields. Scope generations to the state a response owns so a
  filter-only commit cannot invalidate an unrelated safety acknowledgement.
- Serialize writes per entity and coalesce still-pending fields. Track the
  latest intent per field so an older completion cannot trigger an authoritative
  reload that restores its stale value. Compare edits with that pending value,
  not only the last server value—a return to the original value can itself be
  the newest intent. On failure, restore the value currently acknowledged in
  the live collection only if the control still shows the failed attempt.
- When a wholesale list snapshot races live events, buffer and replay the
  events over the accepted snapshot. Coalesce by stable entity ID rather than
  counting raw events, and explicitly mark buffer overflow inexact so it forces
  an authoritative retry instead of silently omitting visible records.
- Defer server-driven editor remounts while a control is focused, a debounced
  draft is dirty, or a write is in flight, then apply the latest deferred view.
  Capture and restore any stable focusable detail control—not only form fields—
  when that deferred remount becomes safe.
- Resolve the active project before importing any module that reads
  project-scoped storage during evaluation. Route every cross-feature entry
  point through one shared readiness-aware loader rather than importing the
  feature directly from a second module.

Add focused source-contract coverage for each generation, readiness boundary,
and persistence guard because the embedded UI has no frontend build step.
