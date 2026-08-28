---
name: embedded-ui-async-state
description: Preserve authoritative state across overlapping requests, live events, project-scoped hydration, and lazy UI modules in Interseptor's embedded ES-module frontend.
---

# Embedded UI async state

Use this when changing asynchronous reads, live updates, project-scoped browser
storage, lazy feature imports, or editors that persist server-backed drafts.

- Give every latest-request-wins path a generation token. Compare the token
  after each await, including when the selected entity ID is unchanged.
- Track whether a visible collection came from a full load or a server-filtered
  query. When changing back to client-only filtering, invalidate the filtered
  request immediately and reload the full collection; a generation guard alone
  cannot restore rows the server omitted.
- Bind auxiliary counts and diagnostics to a complete filter-context signature.
  Their primary dataset may remain valid after a client-only filter changes,
  while a secondary “hidden” or “available” count no longer describes the
  visible context.
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
  Also capture the field-commit generation when a full-summary request starts;
  if a field acknowledgement lands before that summary returns, overlay only
  those newly committed fields so the older summary cannot restore stale input.
- Mark debounced controls dirty on the input event, not when the eventual
  request starts. Merge that draft over intervening live summaries, accept a
  returned full summary only while its summary generation is current, and do
  not let a field-only fallback clear an explicit unavailable state.
- Serialize writes per entity and coalesce still-pending fields. Track the
  latest intent per field so an older completion cannot trigger an authoritative
  reload that restores its stale value. Compare edits with that pending value,
  not only the last server value—a return to the original value can itself be
  the newest intent. On failure, restore the value currently acknowledged in
  the live collection only if the control still shows the failed attempt.
- Scope debounce timers by entity whenever selection can change before they
  fire. A snapshot prevents cross-entity corruption, but a global timer can
  still cancel another entity's unsent edit.
- When a wholesale list snapshot races live events, buffer and replay the
  events over the accepted snapshot. Coalesce by stable entity ID rather than
  counting raw events, and explicitly mark buffer overflow inexact so it forces
  an authoritative retry instead of silently omitting visible records.
- Defer server-driven editor remounts while a control is focused, a debounced
  draft is dirty, or a write is in flight, then apply the latest deferred view.
  Capture and restore any stable focusable detail control—not only form fields—
  when that deferred remount becomes safe.
- Keep authoritative lifecycle transitions separate from display-only renders.
  Filtering or opening history may repaint results, but must not change run
  ownership, locks, completion capture, polling, or the authoritative fallback.
  Track the displayed evidence snapshot independently, and make filters,
  follow-up actions, and error-only rerenders consume that same snapshot so the
  visible rows and their actions cannot silently refer to different runs. When
  live processing continues behind a history snapshot, provide a direct,
  keyboard-accessible return to the live evidence owner.
- Resolve the active project before importing any module that reads
  project-scoped storage during evaluation. Route every cross-feature entry
  point through one shared readiness-aware loader rather than importing the
  feature directly from a second module.

Add focused source-contract coverage for each generation, readiness boundary,
and persistence guard because the embedded UI has no frontend build step.
