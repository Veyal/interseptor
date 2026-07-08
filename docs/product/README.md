# Product

Product-management home for Interseptor: why we're building it, for whom, what's next, and how we
decide. These are living documents — PRs that change product direction update them.

## The docs

| Doc | What it answers |
|---|---|
| [strategy.md](strategy.md) | Vision, target users/personas, value prop, competitive positioning, non-goals |
| [roadmap.md](roadmap.md) | What's Now / Next / Later, with prioritization (RICE) and themes |
| [improvements.md](improvements.md) | UX/ease + API/MCP gap analysis (what to improve and why) |
| [metrics.md](metrics.md) | North Star (Weekly Active Hunters) + funnel KPIs + privacy-first measurement |
| [prd-0001-target-scope.md](prd-0001-target-scope.md) | Flagship PRD for the #1 next feature — and the PRD template |
| [prd-0002-active-scanning.md](prd-0002-active-scanning.md) | Active scanning (with & without AI) — shipped |
| [prd-0003-rule-packs.md](prd-0003-rule-packs.md) | Decoupled, signed scan-rule distribution — draft |

## TL;DR

**What:** a single static Go binary intercepting proxy + security toolkit.
**For whom:** bug-bounty hunters first, then pentesters, security-minded devs, and automation/agents.
**Why we win:** Caido-class speed at Hetty's price (free + open), with a scriptable REST/SSE + MCP
API none of the lightweight competitors match. We out-precision ZAP and out-friction Caido.
**What's next:** close table-stakes gaps (target scope → benchmarks → system-proxy → HAR), then press
differentiators (response interception, full MCP server).

## How we do product (lightweight)

1. **Discover** — keep [strategy.md](strategy.md) and the competitive picture current; let user
   signal and [metrics.md](metrics.md) inform priorities.
2. **Prioritize** — score the backlog in [roadmap.md](roadmap.md) (RICE); commit a small "Now" slice.
3. **Define** — a graduating item gets a **PRD** (problem → goals → requirements → acceptance
   criteria → metrics), following [prd-0001](prd-0001-target-scope.md) as the template. Number PRDs
   `prd-NNNN-slug.md`.
4. **Build** — PRD → a TDD implementation plan in `docs/superpowers/plans/` (existing engineering
   convention) → code with tests + a `CHANGELOG.md` entry.
5. **Measure & learn** — track against the North Star and guardrails; re-rank the backlog.

Engineering standards live in [CONTRIBUTING.md](../../CONTRIBUTING.md); the architecture is in
[README.md](../../README.md).
