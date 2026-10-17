# Interseptor

[![CI](https://github.com/Veyal/interseptor/actions/workflows/ci.yml/badge.svg)](https://github.com/Veyal/interseptor/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Veyal/interseptor?sort=semver)](https://github.com/Veyal/interseptor/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/Veyal/interseptor?logo=go&logoColor=white&color=00ADD8)](go.mod)

*"Interseptor"* — from the Indonesian slang **intersep** (to intercept), plus `-tor`. It does exactly
what it says on the tin: it **intersep**s your HTTP/HTTPS traffic so you don't have to squint at
DevTools like it's 2014.

A native **intercepting proxy + security-testing toolkit**, shipped as **one static Go binary** — no
JVM to warm up, no license dongle, no "upgrade to Pro to export more than 3 findings." Point traffic
at it, watch it flow, break things (yours, or things you're allowed to break).

**Built for human-led, agent-assisted testing.** You drive a fast web UI; external AI assistants and
agents drive the exact same engine through a real **MCP server** and a **REST/SSE API**. Interseptor
provides deterministic capture, replay, mutation, scanning, scope, evidence, and finding tools. The
external agent owns reasoning and sequencing. Nothing phones home.

> ⚖️ **Responsible use.** Interseptor is for testing systems you **own or are explicitly authorized
> to test**. Pointing it at traffic that isn't yours, or systems you don't have permission to test, is
> illegal in most places. That one's on you, not us.

---

## What it does

- **Intercept & edit** HTTP/HTTPS live (hold, forward, drop — requests *and* responses) via a local
  CA, with match-&-replace rules.
- **Repeater & Intruder** — resend and mutate requests by hand, or run Sniper, Battering, Pitchfork, Cluster, and
  no-payload Repeat (race-window) attacks with grep-match/extract and anomaly flagging. Import Postman Collection v2.0/v2.1 JSON directly into
  editable Repeater tabs (optionally with an exported environment) when a collection is the fastest
  way to bring an API test plan into a pentest.
- **Scanner** — passive checks out of the box. External agents compose Repeater, Intruder, Authz, OOB,
  and verification workflows through MCP while Interseptor records requests, evidence, and findings.
- **Extensible** — write your own passive checks in sandboxed Starlark, no fork required.
- **Mobile-ready** — Android and iOS setup for HTTPS interception on real devices.
- **MCP & API native** — a full MCP server and a REST/SSE API so an external agent or script drives
  the same core as the UI. Interseptor has no built-in model provider or autonomous decision loop.

That's the highlight reel — the **[full feature list](docs/FEATURES.md)** covers WebSockets, HAR and
Burp Suite traffic import, project bundles, collaboration/remote access, session & login macros,
and more.

## Get it running

The quickest path — grab a prebuilt binary (no Go toolchain needed):

```bash
# macOS / Linux — GitHub CLI picks the latest release; swap darwin_arm64 for
# darwin_amd64, linux_amd64, or linux_arm64
gh release download --repo Veyal/interseptor --pattern '*_darwin_arm64.tar.gz'
tar xzf interseptor_*_darwin_arm64.tar.gz
./interseptor
```

No `gh`? Download the archive for your OS and architecture from the
[Releases page](https://github.com/Veyal/interseptor/releases/latest) and extract it. Release
archives list their SHA-256 sums in `checksums.txt` on the same page.

Or, if you have Go 1.25+:

```bash
go install github.com/Veyal/interseptor/cmd/interseptor@latest
interseptor
```

Package managers are **not currently current**: the Homebrew tap is pinned to v2.9.0 and the
automated publish is disabled, so these install an old build. Use `go install` or a release binary
until the tap and bucket are re-seeded (see [packaging/](packaging/README.md)):

```bash
brew install Veyal/tap/interseptor
```

```powershell
scoop bucket add Veyal https://github.com/Veyal/scoop-bucket
scoop install interseptor
```

That starts the proxy on `127.0.0.1:8080` and the UI on `127.0.0.1:9966`. Point your traffic at the
proxy, trust the CA from **Settings** to decrypt HTTPS, and you're intersep-ing.

Prebuilt binaries, `interseptor update`, building from source, environment variables, and running
several projects at once are all in **[Getting started](docs/getting-started.md)**.

## For AI agents

Give any AI agent the URL below and it knows how to install Interseptor, connect over MCP or REST, stay
in scope, and write findings in one consistent format:

```text
Read https://veyal.github.io/interseptor/llms-full.txt and follow it.
```

The guide lives in [docs/agents.md](docs/agents.md) (published as
[Interseptor for AI agents](https://veyal.github.io/interseptor/ai-agents/)); `llms.txt` and
`llms-full.txt` are generated from the docs by `go run ./tools/docscheck generate`.

## Docs

| | |
|---|---|
| **[Getting started](docs/getting-started.md)** | Install, quick start, HTTPS setup, configuration, multi-project |
| **[Interseptor for AI agents](docs/agents.md)** | One URL for agents: rules, install, MCP and REST setup, workflow, and the finding-writing contract |
| **[Workspace guide](docs/workspace.md)** | Menus, captured response views, session inspection, Notes, Activity, Decoder |
| **[Settings](docs/settings.md)** | Section map, search, saved changes, and project-switch recovery |
| **[Full feature list](docs/FEATURES.md)** | Feature overview and guide links |
| **[Proxy, TLS & networking](docs/proxy-and-tls.md)** | Listener auth, the `interseptor` browser prompt, CA trust, pinning, upstream proxies |
| **[Mobile testing](docs/mobile-testing.md)** | Android/iOS USB, Wi-Fi, CA trust, and cleanup |
| **[Findings & reporting](docs/findings-and-reporting.md)** | Reading and editing, evidence, revisions, readiness, report export |
| **[Collections](docs/collections.md)** | Postman migration, variables, scripts, runner, CI with JUnit, importers |
| **[Projects & data](docs/projects-and-data.md)** | Storage, project boundaries, export formats, retention |
| **[API & MCP](docs/api-and-mcp.md)** | Drive Interseptor from an AI agent or a script |
| **[CLI reference](docs/cli-reference.md)** | Commands, flags, environment variables, checks, rules, vault |
| **[Troubleshooting](docs/troubleshooting.md)** | No traffic, TLS, 407 auth, Repeater, UI, ports, disk use |
| **[History search](docs/history-search.md)** | Anywhere filters and saved Starlark predicates |
| **[Architecture](docs/architecture.md)** | Security model, package layout, UI structure |
| **[Custom checks](docs/custom-checks.md)** | Author your own passive Starlark checks |
| **[Rule packs](docs/rule-packs.md)** | Share, install, and manage bundles of checks |
| **[Project vault](docs/vault.md)** | Always-on backup / import / merge (Tailscale Serve) |
| **[Engagement close-out](docs/engagement-closeout.md)** | Checklist to leave a report-ready project |
| **[MCP cookbook](docs/product/mcp-cookbook.md)** | Copy-paste agent recipes |
| **[Contributing](CONTRIBUTING.md)** | Code standards, TDD, commit style, cutting a release |
| **[Security policy](SECURITY.md)** | Reporting a vulnerability *in* Interseptor |
| **[Roadmap & strategy](docs/product/)** | Why this exists, what's next, benchmarks |
| **[Changelog](CHANGELOG.md)** | What shipped, when |

## License

[MIT](LICENSE) © 2026 veyal.
