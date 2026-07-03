# PRD-0003 — Rule Packs (decoupled scan-rule distribution)

*Owner: Product · Status: 📝 Draft · Priority: Next (P1) · Last updated: 2026-07-03*
*Links: [strategy.md](strategy.md) · [roadmap.md](roadmap.md) · [prd-0002-active-scanning.md](prd-0002-active-scanning.md)*

> Follows the [PRD-0001](prd-0001-target-scope.md) section template.

## 1. Summary

Move the **scan-rule catalog** (passive + active checks) into a separate, versioned, signed
**rules repo** that Interceptor can **sync on demand** into its check directories — so new detections
ship without a new binary. The built-in Go checks stay in the binary as the always-works **offline
baseline**; rule packs *augment* them. Sync is **explicit, pinned, and integrity-verified** — never a
silent background download — and **new active checks install disabled** because they emit attack
traffic. Every finding is stamped with the rule-pack version so scans stay reproducible.

## 2. Problem & context

Detection rules change on a different clock than the app. Today, adding an SQLi signature or a new
passive check means editing `internal/scanner`/`internal/activescan` and cutting a binary release —
slow, and it couples security content to app cadence. Every serious scanner decouples this: Nuclei
templates, Semgrep rules, Suricata/Snort signatures, ClamAV, Burp's BApp store. We should too.

We are **most of the way there already**: both engines load user `.star` checks from disk
(`~/.interceptor/checks` for passive via `internal/checkscript`, `~/.interceptor/active-checks` for
active via `internal/activescript`), both sandbox execution (no net/files/clock), same-id checks
override built-ins, and CI already compile-validates every template (`TestBuiltinTemplatesCompile`,
`TestActiveBuiltinTemplatesCompile`). What's missing is a **canonical rule source** and a **safe way
to pull from it**.

The trap to avoid: naive "auto-download rules." Interceptor is a **security tool that is itself a MITM
proxy**, prized for being a **self-contained, offline, no-phone-home** single binary. Two properties
make silent auto-update actively dangerous here:

- **Active rules are remote code that sends attack traffic.** A compromised repo or bad PR would make
  users' tools fire new payloads at client networks with nobody reviewing them — undermining the
  consent/scope guardrails PRD-0002 was built around.
- **Reproducibility matters.** PRD-0002 ties every finding to its confirming request; if the rule set
  changes silently between runs, a report becomes hard to defend ("found with *which* rules?").

## 3. Goals / Non-goals

**Goals**
- A separate **`interceptor-rules`** repo: curated passive + active `.star` checks with a manifest,
  CI-gated to compile and pass detection tests.
- Interceptor can **install/update a pinned, signed rule pack** into its check dirs, on demand.
- **Reproducibility:** record the installed pack id+version; surface it on findings and in reports.
- **Trust split by risk:** synced passive checks enable automatically (sandboxed, harmless); synced
  **active** checks install **disabled**, behind an explicit review + enable.
- **Offline-first preserved:** zero network unless the user syncs; support **air-gapped import** of a
  downloaded pack file.
- **Integrity:** packs are signed; the app verifies before trusting.

**Non-goals**
- Silent/background auto-update (especially of active checks). Sync is always user-initiated.
- Removing the built-in Go checks — they remain the offline baseline and the fast path.
- A general plugin marketplace / third-party registry — one canonical repo first (§12).
- Any telemetry or phone-home beyond the explicit sync request.

## 4. Users & use cases

- **Bug-bounty hunter (primary):** `Settings → Rules → Update`, gets this month's new passive
  detections and a couple of reviewed active checks, without re-installing.
- **Pentester on a locked-down client network:** runs fully offline; downloads a signed pack on a
  separate machine and **imports the file**; the report cites the exact pack version.
- **Contributor / the AI via MCP:** opens a PR of a new `.star` check against `interceptor-rules`; CI
  validates it; it ships in the next pack — no Go, no binary release.
- **Cautious operator:** never syncs; keeps the shipped built-ins only. Nothing changes for them.

## 5. Functional requirements

**FR1 — Pack format.** A rule pack is a tarball (or a git tag) containing:
- `passive/*.star`, `active/*.star` — the checks (same contracts as today's custom checks).
- `manifest.json` — `{ id, version (semver), minAppVersion, createdTS, checks: [{ id, kind:
  "passive"|"active", class, severity, title }], }`.
- A detached **signature** over the pack (ed25519 / minisign).

**FR2 — The rules repo.** `interceptor-rules` holds the sources + tests; CI (a) compiles every check
(reuse the existing compile gate), (b) runs per-check detection unit tests (positive + negative
fixtures — mirrors the strict-FP bar in the codebase), (c) validates the manifest, (d) builds and
**signs** the release tarball. Only signed, CI-green tags become installable packs.

**FR3 — Sync (explicit).** `interceptor rules status|update|pin <version>` CLI and a
`Settings → Rules` panel. Update fetches a specific tag, **verifies the signature against a public key
pinned in the binary**, checks `minAppVersion`, then unpacks into the check dirs. No auto-schedule.

**FR4 — Air-gapped import.** `interceptor rules import <file>` / a UI file-picker installs a
pre-downloaded signed pack with the same verification — no network.

**FR5 — Trust split.** On install, passive checks are active immediately. New **active** checks are
added to the `checks.disabled` set (reuse the existing toggle store) and surfaced in a "review"
list with their class/severity/diff; the operator enables them explicitly. Updates to an
already-enabled active check show a diff and keep prior enablement (with a "changed" badge).

**FR6 — Precedence.** Resolution order for a given check id: **user-local** `.star`
(hand-authored/edited) **>** installed pack **>** built-in Go. Same-id override semantics are today's
behavior; packs simply become a middle layer. The Checks manager labels each check's source.

**FR7 — Reproducibility stamp.** Persist the installed pack `id@version`. Include it on active-scan
runs and in `scan_report`/`export_report` output ("Rules: builtin + acme-pack@1.4.0").

**FR8 — Offline default & failure modes.** With no pack installed, behavior is exactly today's.
Sync failures (offline, bad signature, version skew) are explicit errors that never partially apply a
pack (atomic install to a temp dir, then swap).

## 6. UX

`Settings → Rules`:
- Installed: `builtin (29 passive / 15 active)` + `acme-pack@1.4.0 (signed ✓)`, with **Check for
  updates** and **Import file…**.
- Update flow: show the changelog and a **review list of new/changed active checks** (red, disabled
  by default) with per-check enable toggles; passive changes shown but auto-applied.
- The **Checks manager** (existing) gains a source badge per check: `built-in` / `pack` / `custom`,
  and pack checks are toggleable exactly like built-ins today.
- A verification banner if a pack fails signature/`minAppVersion` — install is refused.

## 7. API & data model

- **REST:** `GET /api/rules` (installed pack + version + verification state + per-kind counts);
  `POST /api/rules/sync {version?}`; `POST /api/rules/import` (multipart signed pack); reuse
  `POST /api/checks/disabled` for the active-check enablement. All guarded (loopback + the existing
  control-plane auth); sync/import are **operator actions**, not exposed to unauthenticated callers.
- **SSE:** `checks.update` already exists — fire it after an install so the Checks manager refreshes.
- **MCP:** `rules_status` (read-only). A `rules_sync` tool is **optional and gated** — the AI should
  not be able to pull new *active* payloads unprompted; if added, it requires the same arm/consent as
  active scanning and only ever installs (never auto-enables) active checks.
- **Storage:** a `rules.pack` setting (`{id, version, installedTS, publicKeyId}`); checks land in the
  existing dirs; enablement reuses `checks.disabled`. No new tables.
- **Manifest schema** as in FR1; the signature scheme and pinned public key ship in the binary.

## 8. Acceptance criteria (testable)

- A signed, CI-green pack installs; its passive checks appear enabled and its active checks appear
  **disabled** in the Checks manager.
- A pack with a bad/missing signature or `minAppVersion > app` is **refused**, atomically (no files
  written).
- With no network and no pack, scan behavior is byte-identical to the current build.
- Air-gapped `import` of a downloaded signed pack works with networking disabled.
- After install, `scan_report` output names the pack `id@version`.
- User-local `.star` with the same id overrides a pack check; a pack check overrides the built-in.
- The `interceptor-rules` CI fails a check that doesn't compile or fails its negative-FP fixture.

## 9. Success metrics

- **Time-to-ship a new detection:** from "merge check" to "available to users" drops from a binary
  release cycle to a pack release (hours).
- **Adoption:** % of active installs that have synced a pack at least once (guardrail: must not
  require it — offline users stay first-class).
- **Quality:** no increase in false-positive reports attributable to packs (the CI FP-fixture gate is
  the control).
- **Zero** unsigned/silent active-rule activations in the field (safety guardrail).

## 10. Rollout / phasing

1. **Phase 1 — content + reproducibility (no online sync):** stand up `interceptor-rules` with the
   manifest + CI compile/FP-fixture gate; support **signed file import** and the reproducibility
   stamp. Ships the safety-critical parts first, no network surface.
2. **Phase 2 — signed online sync + UI:** `rules sync/pin`, the `Settings → Rules` panel, the
   active-check review/enable flow, SSE refresh.
3. **Phase 3 — scale:** multiple packs / namespacing, `rules_sync` MCP tool (gated), and a path toward
   community-contributed packs.

## 11. Risks & open questions

- **Supply chain (top risk).** Signing key custody and rotation; who can cut a signed pack. Mitigation:
  single pinned key in v1, CI-only signing, active checks always land disabled.
- **Sandbox ceiling.** Some built-ins can't be expressed in today's Starlark sandbox (timing-based
  blind SQLi/cmdi, host-header, CORS need response timing / header control the sandbox doesn't grant).
  Those **stay Go built-ins**; packs cover what the sandbox can express. Open question: grow the active
  sandbox (safe timing primitive?) so more can live in packs.
- **Go/Starlark duplication.** Built-ins currently carry both a Go impl and a Starlark override
  template. Packs don't remove that, but give a single source of truth for the *editable* layer; decide
  long-term whether the Starlark templates migrate into the pack repo.
- **Version skew.** `minAppVersion` guards forward-incompat; need a deprecation story for checks that
  rely on newer sandbox builtins.
- **Trust UX.** How much friction on enabling active checks before it's ignored? Start strict
  (explicit per-check), relax only with evidence.

## 12. Out of scope / future

- A third-party **marketplace/registry** and multi-publisher trust (one canonical repo first).
- **Automatic/scheduled** updates of any kind.
- Native (non-Starlark) compiled plugins.
- Importing external formats (Nuclei templates, ZAP scripts) — possible converter, later.
