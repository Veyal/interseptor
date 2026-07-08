# Interseptor — Product Strategy

*Owner: Product · Last updated: 2026-06-22 · Status: living document*

## Vision

**Make intercepting-proxy security testing instant, scriptable, and free.** A single static
binary you can run anywhere in under a second, that captures and manipulates traffic without the
weight of a JVM — and that an AI agent or CI job can drive as easily as a human.

## Intent (read this first)

**Interseptor is an intercepting proxy built to be operated by a penetration tester *and their AI
assistant together.*** The human and the AI are **both first-class users of the same engine**:

- the **human** gets a fast, low-friction web UI to watch, intercept, replay, fuzz, and scan traffic;
- the **AI** gets the *same capabilities* through a real **MCP server** and a clean **REST/SSE API**,
  so it can autonomously list/inspect flows, replay and mutate requests, run Intruder/Scanner, toggle
  intercept, and add match-&-replace rules — under the tester's direction.

Everything we build serves that pairing: **great UI/UX for the tester, and an equally first-class
MCP/API surface for the AI.** If a capability exists in the UI, it must be reachable by the agent,
and vice-versa. Captured traffic stays on the tester's machine; the AI drives the local engine, it
doesn't ship your traffic anywhere.

## The problem

Web/API security testing is dominated by two heavy, JVM-based tools:

- **Burp Suite** is the industry standard but **costs ~$475/user/yr** for Pro, and is widely
  criticized for **JVM memory weight** (idle RSS in the gigabytes, leaks that force restarts;
  PortSwigger shipped a "game-changing performance update" in Sept 2024 conceding the core needed
  to be lighter). Its free Community edition deliberately omits the scanner, throttles Intruder,
  and can't save sessions.
- **OWASP ZAP** is free and open, but its **UI is cluttered** and it carries a **higher
  false-positive / triage burden**, especially on SPAs and authenticated APIs.

Meanwhile a new generation of **native tools (Caido in Rust, Hetty in Go)** is taking share among
bug-bounty hunters who started after 2024 — validating the thesis that *native, fast, and
lightweight beats the JVM incumbents*. But Caido is **freemium** (free tier capped at 2 projects /
7 workflows / 3 plugins; Individual ~$200/yr) and Hetty's development is slow and feature-thin.

There is room for a tool that is **as fast and native as Caido, as free and open as Hetty, with a
scriptable API the others lack** — built for the way security work is heading: API-first,
CI-integrated, and increasingly agent-driven.

## Target users & personas

Our **primary target is the AI-assisted penetration tester**: a tester who works *with* an AI
assistant (Claude, etc.) in the loop. This is a **pair** — the human and the agent — and we design
for both as first-class users of the same engine.

| Persona | Who | Primary jobs-to-be-done | What they value |
|---|---|---|---|
| **Priya — the AI-assisted pentester** (primary, human half) | Pentester/bug-bounty hunter who drives an AI assistant while testing | Direct the AI to capture/inspect/replay/fuzz/scan; jump in via the UI to verify, intercept, and edit | A fast UI to supervise + take over; an AI that can actually *do* the work in the tool |
| **Atlas — her AI agent** (primary, agent half) | An LLM agent (Claude Code/Desktop, custom) connected over MCP | List & read flows, replay/mutate requests, run Intruder/Scanner, toggle intercept, add rules — autonomously | A complete, well-described **MCP** toolset + **REST/SSE** API; predictable, machine-readable results |
| **Bug-bounty hunter "Bea"** (secondary) | Independent, price-sensitive, post-2024 cohort | Capture & replay fast; fuzz; find auth/IDOR/injection bugs | Zero cost, instant start, portability |
| **Security-minded dev "Devi"** (secondary) | Builds APIs, self-tests | Inspect own traffic; catch missing headers/secrets | Clean UI, low noise, runs locally |

**Win first: the Priya + Atlas pair.** No incumbent is built for the human+AI loop — Burp bolts AI
on for itself; the lightweight natives (Caido, Hetty) have no real agent surface. A proxy where the
**AI is a first-class operator** is our defensible wedge, riding the hottest 2024–25 trend (MCP/agents).

## Value proposition

> **A Burp-class manual-testing workflow in a single free Go binary — instant, light, and fully
> scriptable.**

Three pillars:

1. **Lightweight & instant** — one static binary, no JVM, no install, sub-second start, flat RAM
   (bodies stream to disk; only a bounded index in memory). *Proof obligation: publish benchmarks.*
2. **Free & open** — uncapped, MIT/Apache-style OSS. No paywall on the scanner, Intruder, or
   "projects" the way Burp Community and Caido's free tier impose.
3. **Scriptable & agent-native** — a first-class REST + SSE control API and an MCP surface, so
   scripts, CI, and AI agents drive the same core the UI does. The incumbents bolt this on; we
   build around it.

## Positioning vs the market

The market is **barbell-shaped**: free OSS (ZAP, mitmproxy, Hetty) at one end, **$475/yr Burp Pro**
(and 5-figure Enterprise) at the other, with **Caido (~$200/yr) as the disruptive middle**.

| | Burp Pro | OWASP ZAP | Caido | **Interseptor** |
|---|---|---|---|---|
| Runtime | JVM (heavy) | JVM (heavy) | Rust (native) | **Go (native, 1 binary)** |
| Price | ~$475/yr | Free | Freemium (~$200/yr) | **Free + open** |
| Scanner | Best-in-class | Free but noisy | Weak (plugin) | Quiet & precise (passive) |
| Scriptable API | Extensions (Java) | API + automation | Workflows (JS) | **REST + SSE + MCP** |
| Best for | Pro pentests | Free automation/CI | Bug bounty (paid) | **Bug bounty + dev + agents (free)** |

**Our two-sentence position:** *Interseptor gives you Caido-class speed at Hetty's price (free and
open), with a scriptable API none of the lightweight tools match. It's the intercepting proxy for
people who want Burp's workflow without Burp's weight, cost, or lock-in.*

**Where we compete and where we don't:**
- **Don't** try to out-scan Burp or out-ecosystem its extensions — that moat is unwinnable short-term.
- **Do** out-precision ZAP (quiet, low-false-positive scanner) and **out-friction Caido** (uncapped
  free, single binary) and **out-API everyone** (scriptable + MCP for the agent era).

## Market tailwinds (why now)

- **API security + DAST is the fastest-growing slice** of a market projected to grow from ~$1.83B
  (2025) to ~$7.60B (2031), ~26.7% CAGR — "driven by API proliferation." Our API-first, headless
  design rides this.
- **Agent/MCP tooling is brand-new and hot** (post-late-2024). Security toolkits are racing to
  expose themselves to AI agents via MCP. "Drive an intercepting proxy from an AI agent" is a fresh,
  defensible angle the JVM incumbents don't have natively.
- **AI-assisted testing is now table-stakes at the top** (Burp AI: Explore Issue, Explainer,
  AI login sequences, Shadow Repeater). We don't need to match it — being the *best substrate for an
  external agent* (via MCP) is our pragmatic lane.

## Non-goals (for now)

- **Not** an enterprise DAST/CI-scale automated scanning platform (that's Burp Enterprise / ZAP).
- **Not** chasing extension-ecosystem parity with Burp.
- **Not** building heavyweight collaboration/multi-user in the near term.
- **Not** shipping our own hosted AI — at most bring-your-own-key assist + MCP.
- Privacy is a feature: **captured traffic never leaves the machine**; any telemetry is opt-in.

## Strategic risks

- **Caido is executing in the same lane.** "Native and fast" alone is no longer differentiating —
  our durable edges must be **free-and-open + scriptable/agent-native**, and speed of execution.
- **Table-stakes gaps** (target scope, HAR export, HTTP/2, upstream proxy, session handling) can
  lose a "Burp alternative" evaluation before our differentiators matter. Close them early
  (see [roadmap.md](roadmap.md)).
- **Trust**: a security tool that mishandles TLS/secrets is dead on arrival. Correctness and a
  clean security posture are non-negotiable.

*Competitive facts above are from a 2024–2025 market scan; see commit history for the source notes.
Re-verify pricing against vendors' live pages before external publication.*
