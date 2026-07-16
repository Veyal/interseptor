# Interseptor

[![CI](https://github.com/Veyal/interseptor/actions/workflows/ci.yml/badge.svg)](https://github.com/Veyal/interseptor/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Veyal/interseptor?sort=semver)](https://github.com/Veyal/interseptor/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)

*"Interseptor"* — from the Indonesian slang **intersep** (to intercept), plus `-tor`. It does exactly
what it says on the tin: it **intersep**s your HTTP/HTTPS traffic so you don't have to squint at
DevTools like it's 2014.

A native **intercepting proxy + security-testing toolkit**, shipped as **one static Go binary** — no
JVM to warm up, no license dongle, no "upgrade to Pro to export more than 3 findings." Point traffic
at it, watch it flow, break things (yours, or things you're allowed to break).

**Built for the AI-assisted pentester.** You drive a fast web UI; your AI assistant drives the exact
same engine through a real **MCP server** and a **REST/SSE API** — listing flows, replaying and
mutating requests, fuzzing, scanning, toggling intercept — all under your direction, all on **your
machine**, nothing phoned home.

> ⚖️ **Responsible use.** Interseptor is for testing systems you **own or are explicitly authorized
> to test**. Pointing it at traffic that isn't yours, or systems you don't have permission to test, is
> illegal in most places. That one's on you, not us.

---

## What it does

- **Intercept & edit** HTTP/HTTPS live (hold, forward, drop — requests *and* responses) via a local
  CA, with match-&-replace rules.
- **Repeater & Intruder** — resend and mutate requests by hand, or run Sniper/Pitchfork/Race attacks
  with grep-match/extract and anomaly flagging.
- **Scanner** — passive checks out of the box, plus a deterministic **active-scan** engine, plus an
  autonomous **Autopilot** mode that plans and runs its own testing and files only findings it can
  prove (differential repro → adversarial review → OOB proof → human confirm for the scary ones).
- **Extensible** — write your own passive/active checks in sandboxed Starlark, no fork required.
- **Mobile-ready** — Android and iOS setup for HTTPS interception on real devices.
- **AI & API native** — a full MCP server (90 tools) and a REST/SSE API so an agent or script drives
  the same core as the UI, plus BYO-key AI assist for explaining requests and suggesting payloads.

That's the highlight reel — the **[full feature list](docs/FEATURES.md)** covers WebSockets, HAR
import/export, project bundles, collaboration/remote access, session & login macros, and more.

## Get it running

The quickest path — grab a prebuilt binary (no Go toolchain needed):

```bash
# macOS / Linux (pick your OS+arch from the Releases page)
curl -L https://github.com/Veyal/interseptor/releases/latest/download/interseptor_<version>_<os>_<arch>.tar.gz | tar xz
./interseptor
```

Or, if you have Go 1.25+:

```bash
go install github.com/Veyal/interseptor/cmd/interseptor@latest
interseptor
```

That starts the proxy on `127.0.0.1:8080` and the UI on `127.0.0.1:9966`. Point your traffic at the
proxy, trust the CA from **Settings** to decrypt HTTPS, and you're intersep-ing.

Prebuilt binaries, `interseptor update`, building from source, environment variables, and running
several projects at once are all in **[Getting started](docs/getting-started.md)**.

## Docs

| | |
|---|---|
| **[Getting started](docs/getting-started.md)** | Install, quick start, HTTPS setup, configuration, multi-project |
| **[Full feature list](docs/FEATURES.md)** | Every capability, in detail |
| **[API & MCP](docs/api-and-mcp.md)** | Drive Interseptor from an AI agent or a script |
| **[Architecture](docs/architecture.md)** | Security model, package layout, UI structure |
| **[Custom checks](docs/custom-checks.md)** · [active checks](docs/custom-active-checks.md) | Author your own Starlark checks |
| **[Rule packs](docs/rule-packs.md)** | Share, install, and manage bundles of checks |
| **[Contributing](CONTRIBUTING.md)** | Code standards, TDD, commit style, cutting a release |
| **[Security policy](SECURITY.md)** | Reporting a vulnerability *in* Interseptor |
| **[Roadmap & strategy](docs/product/)** | Why this exists, what's next, benchmarks |
| **[Changelog](CHANGELOG.md)** | What shipped, when |

## License

[MIT](LICENSE) © 2026 veyal.
