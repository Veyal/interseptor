# Interseptor — Master UI/UX Redesign & Refactoring Roadmap

> **Historical snapshot** — preserved for archaeology. Counts, IA claims, and open items may be stale relative to current `main`.


Produced by a three-way fan-out audit (Feature Archaeologist, UI/UX Architect, Performance
Engineer) against the current codebase on `redesign/ui-overhaul`, synthesized into one execution
plan. Read-only audit — no source files were changed to produce this document.

---

## 1. Executive Summary

Interseptor's backend (`internal/store`, `internal/proxy`, `internal/control`) is well-engineered:
streaming body capture, real keyset pagination, bounded body search, circuit-breakered active
scanning, and a REST/MCP surface that stays in sync by construction (all 84 MCP tools map onto
existing REST routes, confirmed by direct grep). The redesign should **not** touch that
foundation. The problem is entirely in `internal/control/ui/`: an 11-tab flat navigation, ~389
inline `style=` attributes in `index.html` standing in for a design system, four different visual
encodings of "on/off" state, three independently reimplemented list-virtualization engines, and
three independently reimplemented debounced-autosave patterns — all symptoms of feature growth
without a shared component vocabulary, not of bad engineering per feature.

The frontend has no build step (native ES modules, no bundler, no framework) — every
recommendation below is chosen to work within that constraint, not to introduce React/Vite/etc.

**Diagnosis in one line:** the product has scaled its *feature count* faster than its *design
system*, and its live-update strategy (SSE) has scaled with **five different event contracts**
that a single 20-branch dispatcher in `app.js` must interpret correctly. Both are fixable without
a rewrite — this is a consolidation project, not a from-scratch rebuild.

**What's already good and must be preserved:** the store's streaming body writer, keyset flow
pagination, the `patchFlowRow`/virtualized-window rendering model in `proxy.js`, the Map tab's
lazy `<details>` tree hydration, `flowmodal.js`'s single "quick-look" modal reused by five
different list panels, and the MCP↔REST 1:1 mapping. None of this needs architectural change —
it needs to be **extracted into shared primitives** so new panels stop reinventing it.

---

## 2. Master Feature Matrix

11 tabs, 16 modals, ~140 REST routes, 21 SSE event types, 84 MCP tools — all cross-verified to
map onto the same underlying capabilities (no MCP-only backend capability exists). Full detail
lives in the fan-out transcripts; the durable reference table below groups by the navigation
domains proposed in §3.

| Domain (new IA) | Tabs today | Core capabilities | Backend surface |
|---|---|---|---|
| **Capture** | Proxy, Intercept | Live history w/ filter+search, req/res inspector, tags, per-flow notes, compare/diff, cURL export, match & replace, hold queue (req+res) | `/api/flows*`, `/api/intercept*`, `/api/rules*`, `/api/tags*` — SSE `flow.new/update`, `intercept.update`, `rules.update`, `tags.update` |
| **Test** | Repeater, Intruder | Multi-tab manual resend (+ WS repeater), Sniper/Pitchfork/Race fuzzing, decoder, authz/IDOR replay, session/login macros | `/api/repeater/*`, `/api/intruder/*`, `/api/decode`, `/api/authz/*`, `/api/session/*` — SSE `intruder.update` (nudge-then-poll), `session.update` |
| **Recon** | Scanner, Discover, Map | Passive scanner + custom Starlark checks, active scan (consent-gated) + active checks, OOB catcher, forced-browse discovery, endpoint map (tree/table/graph/params), SSL-pinning diagnosis | `/api/scanner/*`, `/api/checks*`, `/api/active-checks*`, `/api/activescan/*`, `/oob/*`, `/api/discovery/*`, `/api/endpoints`, `/api/tls-diagnosis` — SSE `scanner.update`, `checks.update`, `activescan.update`, `oob.update`, `discovery.update` |
| **Report** | Findings, Notes, Activity | Curated finding builder (chain/report views), report export, project Markdown notebook + AI-organize, live AI/tool activity feed, human-in-the-loop banner (no tab) | `/api/findings*`, `/api/notes*`, `/api/activity*`, `/api/human-input*`, `/api/ai/*` — SSE `findings.update`, `notes.update`, `activity`/`activity.clear`, `human.input` |
| **Configure** | Settings (9 flat sections) | Network/listeners, TLS/CA + passthrough, mobile device automation (Android/iOS/iOS-SSH), scope rules, AI provider config, scanner toggles, session, project switch/import/export/danger-zone, API keys + REST/MCP reference | `/api/settings`, `/api/network/hosts`, `/api/sysproxy`, `/api/android/*`, `/api/ios/*`, `/api/scope*`, `/api/project*`, `/api/keys*`, `/api/reference`, `/api/mcp` — SSE `settings.update` (fans out to 6+ reload calls), `scope.update` |

**Confirmed orphans / redundancies to resolve during the rebuild** (not new work items, cleanup
items to fold into the relevant phase):

- Dead code: `proxy.js`'s `applySort()` (no-op passthrough) and `updateSearchNoteBanner()` (empty
  function called twice); orphaned null-guarded DOM lookups for `#helpBtn`, `#fScheme`,
  `#mapHint`, `#proxySuggestedHint`.
- Dead store method: `store.ClearIssues()` (no route calls it — confirmed via full-repo grep,
  removed in Phase 1). `store.NotesImageExists` looked dead from `internal/control` alone but is
  live test-only infrastructure (`internal/store/notes_images_test.go`) — kept as-is.
- `store.QueryFlowsListFilter` vs `store.QueryFlowsFilter` — **investigated and found to be
  intentional, not a defect.** They're a deliberate performance split (cheap list-view projection
  vs. full projection with headers/body hashes for callers that need it); `flows_inscope.go`
  deliberately calls both side by side for exactly this reason. No collapse — a clarifying doc
  comment was added to `QueryFlowsListFilter` instead.
- Two unrelated concepts both called "notes" (per-flow note vs. project notebook) with separate
  storage — naming collision worth a rename pass (e.g. "flow annotation" vs. "notebook").
- `registerAuthzRoutes` bundles `/api/readiness` and `/api/tls-diagnosis` onto the `authzAPI`
  receiver for no architectural reason — cosmetic Go-side cleanup, zero UI impact.
- README claims "49 MCP tools"; actual count is 84 — doc fix, unrelated to the UI work but cheap
  to land alongside.
- Discover and Map have no off-screen progress/unread affordance (unlike Intercept's `heldBadge`
  or Activity's `actBadge`) — fold into the new navigation rail's badge system (§3).
- 5 distinct SSE contracts (payload-inline / nudge-then-poll / conditional-nudge /
  debounce-then-poll / hello-handshake) coexist with no shared convention — this is the top
  architectural item in the Performance Architecture section below.

---

## 3. UI/UX Design System & Layouts

### Diagnosis

- Flat 11-tab strip, all tabs equal visual weight, `overflow-x:auto` already a smell as more tabs
  get added.
- Binary on/off state rendered four different ways across the app (`.toggle` pills, `.icpt-sw`
  LED switches, permanently-`.accent` buttons masquerading as toggles, and bare `aria-pressed`
  with no visual `.on` class).
- 389 inline `style=` attributes in `index.html` — the actual design system today, and
  un-auditable/un-themeable by construction.
- Settings is 9 flat nav items spanning "which port do I bind" to "SSH into a jailbroken
  iPhone" with no sub-grouping beyond a search box.
- The built command palette (`⌘K`) is under-discovered — only a tiny icon button, no mention in
  empty states or onboarding.
- Empty/loading/error states differ per panel (Proxy: plain `.empty` div; Intercept: illustrated
  empty state; Scanner/Discover/Map: raw inline `hint` strings; errors: hand-rolled `innerHTML`
  with inline red spans, no shared component).
- Toolbar density: Scanner's toolbar puts "run a passive scan" at equal weight with "launch active
  scan" and "launch OOB" — three different tools compressed into one row.

### Design tokens (drop into `app.css`'s `:root`)

```css
:root{
  /* Spacing (8px base, 4px half-step) */
  --sp-1:4px; --sp-2:8px; --sp-3:12px; --sp-4:16px;
  --sp-5:24px; --sp-6:32px; --sp-7:48px; --sp-8:64px;

  /* Radii */
  --r-sm:4px; --r-md:7px; --r-lg:10px; --r-xl:14px; --r-pill:999px;

  /* Type scale */
  --fs-caption:10px; --fs-micro:11px; --fs-body:12.5px; --fs-base:13px;
  --fs-lg:15px; --fs-xl:20px;
  --lh-tight:1.35; --lh-normal:1.6; --lh-loose:1.7;

  /* Elevation */
  --elev-1:0 2px 6px var(--shadow);
  --elev-2:0 8px 24px var(--shadow);
  --elev-3:0 16px 48px var(--shadow);

  /* Semantic color roles */
  --sev-critical:#ef4444; --sev-high:#f97316; --sev-medium:#f59e0b;
  --sev-low:#3b82f6; --sev-info:#94a3b8;
  --state-on:var(--accent); --state-off:var(--fg3);
  --state-pending:var(--amber); --state-danger:var(--red);
  --content-note:var(--violet); /* frees --amber for state-only use */

  /* Layout constants */
  --nav-rail-w:180px; --side-panel-w:280px;
  --topbar-h:38px; --ctxbar-h:40px;
}
```

Rule: every inline `width:Npx` in `index.html` collapses onto this scale. No element gets an
inline `style=` for color, spacing, or width going forward.

### Component rules (one primitive per concept, no ad hoc variants)

| Primitive | Rule |
|---|---|
| `.btn` | 3 variants only: `.btn` (neutral), `.btn-primary` (max one per toolbar), `.btn-danger` (destructive, confirm-on-click) |
| `.seg` | Mutually-exclusive view modes only (Raw/Pretty/Render, Sniper/Lists/Repeat). Max 4 segments, else use `<select>` |
| `.toggle` | Binary persisted state only. Single visual form app-wide (adopt Intercept's LED-dot `.icpt-sw` pattern everywhere); `aria-pressed` always mirrors the `.on` class |
| `.badge` | Numeric/status counters only, never interactive |
| `.chip` | Interactive/filterable, always has a clear affordance |
| row states (`.trow` etc.) | One shared row primitive, state via `data-state` (`default/hover/selected/multi-selected/flagged/annotated`), not combinable classes |
| modal | One shell: header+title+close, scrollable body, sticky footer, primary action last |
| toolbar | Max 2 tiers: primary actions + filters. Everything else goes in a `<details>` "Options" disclosure with a descriptive summary |
| empty/loading/error | One shared component: icon + title + one-line hint + optional action, used identically everywhere |

### Navigation & IA blueprint

Replace the flat tab strip with a **persistent left rail grouped into 5 sections** (matches §2's
domains), collapsible to icon-only (~56px), plus a slim top context bar (breadcrumb + global
proxy/intercept status + theme + command palette):

```
CAPTURE   → Proxy · Intercept
TEST      → Repeater · Intruder
RECON     → Scanner · Discover · Map
REPORT    → Findings · Notes · Activity
CONFIGURE → Settings
```

Rationale: this is expert-facing, information-dense tooling where panels are already vertically
tight — stealing a second horizontal row for group tabs compounds the density problem being
diagnosed. A left rail costs vertical space to nothing and degrades gracefully as features are
added (new items slot into an existing group instead of widening a strip).

Tradeoff mitigations: keep existing left-to-right/top-to-bottom item order within groups, keep all
keyboard shortcuts unchanged, and make the command palette an explicit, visible "Jump to…"
affordance (surfaced in every empty state) rather than a small icon — it's the muscle-memory
escape hatch for users disrupted by the regroup. Badges (held-queue count, unread activity) move
from tab-suffix to rail-item-suffix, same mechanism, new location; Discover/Map gain a badge for
the first time.

### Layout specs (priority panels — ASCII, ready to build from)

**Proxy** — split the toolbar into a primary row (search + 2-3 daily filters) and a collapsed
secondary row (notes/scope/manual/hide-TLS, all real `.toggle` pills) so the primary row never
wraps; keep the existing resizable req/res split unchanged.

**Scanner** — separate "browse passive findings" from "launch a tool": Run scan stays in the
primary row; Active Scan and OOB become a demoted secondary action pair opening a dedicated
overlay via the unified modal shell, not equal-weight buttons in the filter row.

**Settings** — same 9 sections, regrouped into 3 labeled nav clusters (Network / Testing /
System) with dividers; the existing card-grid body layout is unchanged — pure nav relabeling.

Full wireframes for all three are in the UI/UX Architect transcript; reproduce verbatim when
implementing Phase 2.

---

## 4. Performance Architecture

The backend needs **no changes** — store query patterns, pagination, and the MITM hot path were
all verified sound (slim list projections, real keyset pagination, streaming body capture bounded
by `maxTransformBody`, bounded 8000-flow body search with content-hash dedup). All work is
frontend, and all of it fits the no-build-step constraint.

### Findings (ranked by leverage)

1. **`app.js` statically imports every feature module at boot** (scanner.js, findings.js,
   tools.js, discovery.js, authz.js, etc. all execute top-level DOM-wiring code before the user
   ever opens those tabs). **Highest-leverage fix**: convert `activateTab()` to lazy-load each
   panel via dynamic `import('./scanner.js')` on first visit — mirrors the pattern `settings.js`
   already uses for `tlsdiag.js`/`apipanel.js`. Mechanical, since feature code is already isolated
   per module.
2. Proxy's virtualized live-update path (`flowRowLiveUpdate`) rebuilds the *entire visible
   window's* `innerHTML` per rAF tick once virtualization kicks in (>120 rows), tearing down and
   rewiring every row's listeners every frame instead of patching only changed/new rows. Bounded
   by rAF so it can't run away, but wasted work under sustained high-throughput capture.
3. `canIncremental()` bails to a full `/api/flows` refetch + full re-render whenever
   scope-only/search/exclude-filters are active — a common pentester workflow (scope mode is the
   default working mode) degrades every new flow event to a full reload (debounced 150ms, so
   bounded, but still O(N) per burst instead of O(1)).
4. `highlightHTTP`/`highlightJSON`/`highlightMarkup` re-run full regex passes over the entire body
   on every inspector render, including on every keystroke of in-body find (150ms debounced, but
   doubles highlighting cost while searching).
5. Minor: no covering index on `res_len` for size-sort — low severity, sort-by-size is a rare
   toolbar action; skip unless it becomes a hot path.

### Architecture recommendations (no-build-step compatible)

- **Dynamic `import()` per tab** (finding #1) — do this first, it's the cheapest, highest-impact
  change available.
- **`<link rel="modulepreload">`** for the 2-3 most-likely-next tabs (Repeater, Intruder, Scanner)
  after Proxy, so the lazy-load doesn't introduce a click-to-first-paint stall.
- **Keyed reconciliation for the virtualized live-update path**: apply the existing
  `patchFlowRow` single-row-patch pattern to the virtualized branch too (currently it abandons
  patching and falls back to full-window re-render once virtualized) — diff old visible ids
  against new, patch/insert/remove only what changed.
- **SSE drop-and-resync on stale reconnect**: `connectEvents()` replays every buffered event
  individually on wake with no compaction — a backgrounded tab reconnecting after a long gap
  should discard per-event replay in favor of one `loadFlows()` refetch (the mechanism already
  exists via `scheduleReload`, just not invoked on the stale-reconnect path).
- **List virtualization is already correct** in three places (Proxy rows, Map table,
  Intruder results) — the fix here is not "add virtualization," it's **extract the one working
  pattern into a shared helper** so it stops being reimplemented a fourth time as new panels grow.
- **Formalize the state store**: `state.flows` (array) + a separately-maintained `flowMap`
  (id→object) is already an ad hoc normalized store. Promote it into `core.js` as
  `flowStore = {byId: Map, order: []}` with `upsertFlow`/`removeFlow` helpers that both mutate and
  publish to subscribers, so `map.js` (which currently derives its own state from a separate
  fetch) and future panels react to `flow:<id>` updates directly instead of each module
  reimplementing dirty-tracking. Also formalize the memo-cache-key pattern already used ad hoc in
  `map.js` (`_dataVersion` + filter-keyed caches) into a shared `memoize(fn, keyFn)` utility.
- **Unify the SSE event contract**: collapse the current five contracts (payload-inline,
  nudge-then-poll, conditional-nudge, debounce-then-poll, hello) toward one convention —
  payload-inline where the payload is small (most events already qualify), with an explicit
  "this event means refetch X" registry instead of a 20-branch if/else in `app.js`. This directly
  supports the navigation-rail badge work in §3 (Discover/Map need an off-screen-update signal,
  which the unified contract should provide for free).

### Consolidation targets (found duplicated 3× independently, unify during the rebuild)

- List virtualization: Proxy rows / Map table / Intruder results.
- Debounced autosave: flow notes / project notebook / finding body blocks.
- Custom dropdown: core.js's generic `enhanceSelect` vs. two bespoke Android/iOS device pickers.
- Tab-manager-with-localStorage-persistence: Repeater tabs / Intruder tabs (~150 duplicated lines).

---

## 5. Phased Implementation Schedule

Ordered so nothing breaks core functionality mid-flight: extract shared primitives before
touching layout, ship the design system before the navigation regroup, and keep the backend
untouched until the very end (cosmetic-only Go changes, zero risk to the MITM path).

### Phase 1 — Foundation (no visible UI change; de-risks everything after)

- Land the CSS design tokens in `app.css` (`:root` additions from §3) alongside the existing
  variables — additive, doesn't break anything yet.
  1a. Build the shared component primitives (`.btn` variants, unified `.toggle`, shared empty/
      error state, shared modal shell) as new CSS classes, without ripping out old inline styles
      yet.
  1b. Extract the one working list-virtualization pattern (from `proxy.js`) into a shared
      `core.js` helper; do not rewire callers yet — just make the helper exist and pass parity
      tests against Proxy's current behavior.
  1c. Extract the debounced-autosave pattern into a shared `core.js` helper (`useAutosave`-style),
      same non-rewiring rule.
  1d. Formalize `flowStore` (`byId`/`order`) in `core.js`, migrate `proxy.js`'s internal `flowMap`
      to use it without changing external behavior.
  1e. Dead-code cleanup: remove `applySort()`, `updateSearchNoteBanner()`, the four orphaned
      null-guarded DOM lookups, and `store.ClearIssues()` (confirmed dead via full-repo grep,
      removed). `store.NotesImageExists` and the `QueryFlowsListFilter`/`QueryFlowsFilter` split
      were investigated and found intentional — left as-is, with a clarifying doc comment added
      to `QueryFlowsListFilter`.
  - **Verification**: `go test ./... && go test -race ./... && go vet ./...` green; manual smoke
    test of Proxy history + Repeater + Intruder (the three panels touched) confirms zero
    behavior change.

### Phase 2 — Component & panel rebuild (visible, panel-by-panel, feature-flaggable)

- Migrate `index.html`/`app.css` off inline styles onto the Phase-1 primitives, one panel at a
  time, in this order (lowest risk → highest): Notes → Activity → Discover → Map → Findings →
  Scanner → Intercept → Intruder → Repeater → Proxy → Settings (Proxy and Settings last since
  they're the largest/most-used surfaces).
  2a. Per panel: replace ad hoc toggles with the unified `.toggle`, ad hoc empty/error markup with
      the shared component, collapse toolbar rows per the "2-tier max" rule.
  2b. Rebuild Repeater/Intruder's duplicated tab-manager as one shared generic component; verify
      both panels' localStorage persistence still round-trips.
  2c. Apply the Proxy/Scanner/Settings layout specs from §3 verbatim.
  - **Verification**: after each panel, manual pass in the browser (per the project's UI
    verification workflow) — confirm no regressions in that panel's SSE-driven live updates.

### Phase 3 — Navigation & IA regroup (the highest-visibility, highest-disruption change — do last)

- Replace the flat tab strip with the 5-group left rail; move badges from tab-suffix to
  rail-item-suffix; add badges to Discover/Map for the first time.
  3a. Rebuild the command palette's visual affordance (explicit "Jump to…" entry point, surfaced
      in empty states) so muscle-memory disruption is mitigated before the rail ships, not after.
  3b. Unify the 5 SSE event contracts into one convention (§4); this is the moment Discover/Map's
      new badges get real data instead of a stub.
  3c. Backend cosmetic cleanup (README tool-count fix, `authzAPI` route regrouping) — zero
      behavioral risk, land alongside for changelog tidiness.
  - **Verification**: full manual pass across all 11 panels confirming every previously-reachable
    action is still reachable (tab click → rail click, keyboard shortcuts unchanged, command
    palette covers every panel); `go test`/`-race`/`vet` still green.

### Phase 4 — Performance hardening (can run in parallel with Phase 2/3 once Phase 1 lands)

- Convert `activateTab()` to dynamic `import()` per panel; add `modulepreload` hints for
  Repeater/Intruder/Scanner.
- Apply keyed reconciliation to the virtualized live-update path using the Phase-1 virtualization
  helper.
- Add SSE drop-and-resync on stale reconnect.
- Fix `canIncremental()` to patch rather than full-reload when scope/search/exclude filters are
  active, using the now-formalized `flowStore`.

Each phase ends with a CHANGELOG entry per project convention; Phase 3 is the natural point to cut
a release, since it's the first phase with user-visible IA change worth calling out on its own.
