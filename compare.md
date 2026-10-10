---
layout: default
title: Interseptor compared with Burp, ZAP, Caido and mitmproxy
description: What Interseptor does not do, a 16-row capability matrix against Burp Suite, ZAP, Caido and mitmproxy, and when you should use something else.
---
# Interseptor compared with Burp, ZAP, Caido and mitmproxy

Start with what Interseptor does not do.

It has no active scanner. The scanner is passive only: 31 built-in checks that run over traffic you already captured. It is about four months old (first commit 2026-06-22), where Burp's lineage dates to 2003 and ZAP's to 2010. It has no extension marketplace: you can write sandboxed Starlark checks and install signed rule packs, and that is all. It has no hosted equivalent of Burp Collaborator. It does not speak HTTP/3. It has no training estate like the Web Security Academy. It does not defeat certificate pinning, and neither does anything else on this page.

If one of those is a requirement, the sections below say which tool to use instead. Prices and licences on this page were checked on 2026-10-10 and each links its source. Prices change, so check the vendor page before you buy.

## The matrix

All 16 rows are below, including the six where Interseptor loses or ties. "Partial" always carries its reason. Every cell is written as a word, so the table does not depend on colour.

<div class="table-scroll">
<table class="comparison-table">
  <caption>Capability comparison, checked 2026-10-10</caption>
  <thead>
    <tr>
      <th scope="col">Capability</th>
      <th scope="col">Interseptor</th>
      <th scope="col">Burp Community</th>
      <th scope="col">Burp Pro</th>
      <th scope="col">ZAP</th>
      <th scope="col">Caido</th>
      <th scope="col">mitmproxy</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <th scope="row">1. Cost for full capability</th>
      <td><span class="cell-yes">Yes</span> Free, MIT</td>
      <td><span class="cell-partial">Partial</span> Free, with limits (rows 5 and 6)</td>
      <td><span class="cell-no">No</span> $499 per user per year</td>
      <td><span class="cell-yes">Yes</span> Free, Apache 2.0</td>
      <td><span class="cell-partial">Partial</span> Free tier is capped; paid tiers exist</td>
      <td><span class="cell-yes">Yes</span> Free, MIT</td>
    </tr>
    <tr>
      <th scope="row">2. Open source</th>
      <td><span class="cell-yes">Yes</span> MIT</td>
      <td><span class="cell-no">No</span> Proprietary</td>
      <td><span class="cell-no">No</span> Proprietary</td>
      <td><span class="cell-yes">Yes</span> Apache 2.0</td>
      <td><span class="cell-no">No</span> "Caido is not open source"</td>
      <td><span class="cell-yes">Yes</span> MIT</td>
    </tr>
    <tr>
      <th scope="row">3. No separate runtime to install</th>
      <td><span class="cell-yes">Yes</span> Static Go binary, no cgo</td>
      <td><span class="cell-no">No</span> JVM application</td>
      <td><span class="cell-no">No</span> JVM application</td>
      <td><span class="cell-no">No</span> Java 17+, bundled on macOS only</td>
      <td><span class="cell-yes">Yes</span> Rust binary</td>
      <td><span class="cell-partial">Partial</span> Python, but native installers ship</td>
    </tr>
    <tr>
      <th scope="row">4. Intercept and edit requests and responses</th>
      <td><span class="cell-yes">Yes</span></td>
      <td><span class="cell-yes">Yes</span></td>
      <td><span class="cell-yes">Yes</span></td>
      <td><span class="cell-yes">Yes</span></td>
      <td><span class="cell-yes">Yes</span></td>
      <td><span class="cell-yes">Yes</span> All six do this</td>
    </tr>
    <tr>
      <th scope="row">5. Unthrottled fuzzer</th>
      <td><span class="cell-yes">Yes</span> Sniper, Pitchfork, Race</td>
      <td><span class="cell-no">No</span> Rate-limited Intruder</td>
      <td><span class="cell-yes">Yes</span> Plus Turbo Intruder</td>
      <td><span class="cell-yes">Yes</span> Fuzzer</td>
      <td><span class="cell-yes">Yes</span> Automate, included on the free tier</td>
      <td><span class="cell-partial">Partial</span> Script it yourself</td>
    </tr>
    <tr>
      <th scope="row">6. Persistent project saving</th>
      <td><span class="cell-yes">Yes</span> SQLite and project bundles</td>
      <td><span class="cell-no">No</span> Temporary in-memory projects</td>
      <td><span class="cell-yes">Yes</span> Disk-based project files</td>
      <td><span class="cell-yes">Yes</span> Sessions</td>
      <td><span class="cell-partial">Partial</span> 2 projects on the free tier</td>
      <td><span class="cell-partial">Partial</span> Flow dumps, not projects</td>
    </tr>
    <tr>
      <th scope="row">7. MCP server with nothing extra to install</th>
      <td><span class="cell-yes">Yes</span> 133 tools in the binary</td>
      <td><span class="cell-partial">Partial</span> Official BApp, you install it</td>
      <td><span class="cell-partial">Partial</span> Official BApp, you install it</td>
      <td><span class="cell-partial">Partial</span> Official add-on, you install it</td>
      <td><span class="cell-partial">Partial</span> Community server, third-party</td>
      <td><span class="cell-no">No</span></td>
    </tr>
    <tr>
      <th scope="row">8. Findings workspace with enforced evidence</th>
      <td><span class="cell-yes">Yes</span> Final export returns 409 until readiness passes</td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-partial">Partial</span> Issue list and report, no evidence gate</td>
      <td><span class="cell-partial">Partial</span> Alerts and report, no evidence gate</td>
      <td><span class="cell-partial">Partial</span> Findings, no evidence gate</td>
      <td><span class="cell-no">No</span></td>
    </tr>
    <tr>
      <th scope="row">9. Evidence provenance enforced in the data layer</th>
      <td><span class="cell-yes">Yes</span> Generated renders are refused as screenshots</td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
    </tr>
    <tr>
      <th scope="row">10. Active vulnerability scanner</th>
      <td><span class="cell-no">No</span> Passive only, 31 checks</td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-yes">Yes</span> Plus built-in browser and API scanning</td>
      <td><span class="cell-yes">Yes</span> Tiered active-scan rule add-ons</td>
      <td><span class="cell-no">No</span> Plugins only</td>
      <td><span class="cell-no">No</span></td>
    </tr>
    <tr>
      <th scope="row">11. Extension ecosystem</th>
      <td><span class="cell-no">No</span> Starlark checks and signed packs, no marketplace</td>
      <td><span class="cell-partial">Partial</span> BApp store, some entries Pro-only</td>
      <td><span class="cell-yes">Yes</span> 300+ BApps from 250+ authors</td>
      <td><span class="cell-yes">Yes</span> Add-on marketplace with alpha and beta tiers</td>
      <td><span class="cell-partial">Partial</span> Plugin store, growing</td>
      <td><span class="cell-partial">Partial</span> Python addons, no marketplace</td>
    </tr>
    <tr>
      <th scope="row">12. Years of hardening</th>
      <td><span class="cell-no">No</span> About 4 months</td>
      <td><span class="cell-yes">Yes</span> Lineage since 2003</td>
      <td><span class="cell-yes">Yes</span> Lineage since 2003</td>
      <td><span class="cell-yes">Yes</span> Since 2010</td>
      <td><span class="cell-partial">Partial</span> Since about 2022</td>
      <td><span class="cell-yes">Yes</span> Since 2010</td>
    </tr>
    <tr>
      <th scope="row">13. Out-of-band (OAST) infrastructure</th>
      <td><span class="cell-partial">Partial</span> Self-hosted catcher, off by default, needs your own tunnel to be reachable</td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-yes">Yes</span> Hosted Burp Collaborator</td>
      <td><span class="cell-partial">Partial</span> OAST add-on, needs your own callback host</td>
      <td><span class="cell-partial">Partial</span> Via plugin</td>
      <td><span class="cell-no">No</span></td>
    </tr>
    <tr>
      <th scope="row">14. HTTP/3</th>
      <td><span class="cell-no">No</span> HTTP/2 upstream, HTTP/1.1 on the MITM client leg</td>
      <td><span class="cell-unknown">Not checked</span></td>
      <td><span class="cell-unknown">Not checked</span></td>
      <td><span class="cell-unknown">Not checked</span></td>
      <td><span class="cell-unknown">Not checked</span></td>
      <td><span class="cell-yes">Yes</span> HTTP/1, 2 and 3</td>
    </tr>
    <tr>
      <th scope="row">15. Training material</th>
      <td><span class="cell-no">No</span> Documentation only</td>
      <td><span class="cell-yes">Yes</span> Web Security Academy</td>
      <td><span class="cell-yes">Yes</span> Web Security Academy</td>
      <td><span class="cell-partial">Partial</span> OWASP docs and community</td>
      <td><span class="cell-partial">Partial</span> Docs and community</td>
      <td><span class="cell-partial">Partial</span> Docs</td>
    </tr>
    <tr>
      <th scope="row">16. Defeats certificate pinning</th>
      <td><span class="cell-no">No</span> Detects it and documents the instrumentation path</td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span></td>
      <td><span class="cell-no">No</span> A tie. No proxy does this.</td>
    </tr>
  </tbody>
</table>
</div>

Interseptor loses or ties on rows 10 to 15. Row 14 reads "Not checked" for four competitors because we confirmed that mitmproxy supports HTTP/3 and that Interseptor does not, and did not confirm the other four. We would rather leave a cell open than fill it from absence of evidence.

Where the matrix says "133 tools" for MCP, count and transports are in the [API and MCP reference]({{ '/api-and-mcp/' | relative_url }}). Burp, ZAP and Caido all have MCP servers too. The difference in row 7 is where the server lives, not whether one exists.

## Burp Suite Professional

Burp Pro is the reference tool for web application testing, and for good reason. The active scanner works on JavaScript-heavy apps through a built-in browser and also scans APIs. Collaborator gives you hosted out-of-band callbacks without running anything yourself. Turbo Intruder handles high-volume attacks. The BApp store lists 300+ extensions from 250+ authors, and the Web Security Academy is the best free training in the field. If your team has years of project files and extensions, that is real value.

**Cost.** $499 per user per year, checked 2026-10-10 at [portswigger.net/burp/pro](https://portswigger.net/burp/pro). The licence is per individual person, not per concurrent user or installation, and it is an annual subscription ([licensing model](https://portswigger.net/support/burp-suite-licensing-model)). It runs on the JVM.

Where Interseptor has an edge:

- Price and licence: MIT, no tiers, no per-person administration.
- No runtime to install: one static binary.
- An MCP server built into the binary rather than added as a BApp, and a findings workspace that refuses a final export until the evidence checks pass. See [findings and reporting]({{ '/findings-and-reporting/' | relative_url }}).

Where it has a real gap:

- No active scanner, no Collaborator, no extension store.
- About four months of history against twenty years. Burp has had far longer to find its own bugs.

## Burp Suite Community

Community is free and it is a good way to learn the workflow. The official `MCP Server` BApp works on Community as well as Pro, so an agent can drive it. What Community withholds is what makes it awkward for paid client work: there is no scanner, and projects are temporary and held in memory, which is also why Intruder results cannot be saved ([PortSwigger on temporary projects](https://portswigger.net/burp/documentation/desktop/settings/suite/performance)). Intruder is rate-limited. We do not publish a specific rate, because we could not confirm one from PortSwigger.

**Cost.** Free (checked 2026-10-10).

Where Interseptor has an edge: this is its clearest comparison. Unthrottled Intruder, saved projects and reporting are all available under MIT. See [collections]({{ '/collections/' | relative_url }}) for imports, including Burp saved-items XML.

Where it has a real gap:

- Community still gives you Burp's interface conventions, its documentation, and the Web Security Academy.
- If you are learning, the Academy alone is a reason to stay.

## OWASP ZAP

ZAP is free, Apache 2.0, and a full tool. It has a real active scanner with tiered rule add-ons and scan policies. The Automation Framework describes a whole run in one YAML file with `spider`, `passiveScan` and `activeScan` jobs, which is a solid CI story. There are official Docker images and GitHub Actions, an add-on marketplace, and an official MCP Integration add-on announced 2026-04-02 ([announcement](https://www.zaproxy.org/blog/2026-04-02-zap-mcp-server/)). Checkmarx has employed the project leads since September 2024, and ZAP remains free with no paid tiers ([Checkmarx](https://checkmarx.com/product/zap/)).

**Cost.** Free, Apache 2.0, checked 2026-10-10. It needs Java 17+; the macOS package bundles a JRE, and Windows and Linux installs need Java ([getting started](https://www.zaproxy.org/getting-started/)).

Where Interseptor has an edge:

- No JVM and a smaller footprint.
- A findings and evidence workspace that ZAP's alert list and report do not have.
- A built-in rather than add-on MCP server.

Where it has a real gap:

- ZAP wins the automated scanning row outright, with sixteen years of hardening behind it.
- Its add-on ecosystem and CI tooling are larger and better established.

## Caido

Caido is the closest modern competitor and the one to take seriously. It is a Rust binary with no runtime, so Interseptor's "no JVM" point does not apply. It has HTTPQL filtering, workflows, remote hosting, a plugin store, and a strong following among bug bounty hunters. Its Automate feature is available on the free tier, and it publishes its own writeup on [agentic pentesting with MCP](https://www.caido.io/blog/2026-07-14-agentic-pentesting-with-mcp/).

**Cost.** We are not printing a paid price for Caido, because the figure on its pricing page is rendered by JavaScript and we could not verify it. Check [caido.io/pricing](https://caido.io/pricing) yourself. What we did confirm there on 2026-10-10: the free tier has no expiry and is capped at 2 projects, 7 workflows, 3 plugins, 5 filter presets, 1 pipeline session and 1 seat, with unlimited installations. The same page says: "Caido is not open source."

Where Interseptor has an edge:

- MIT source you can read and audit, against a closed product.
- No project, plugin or workflow caps.
- A first-party MCP server in the binary. Caido's MCP servers are community-built.
- The findings, evidence and reporting layer.

Where it has a real gap:

- Caido's interface polish, HTTPQL, plugin store and community recognition are ahead of Interseptor's.
- It has roughly four more years in the field than Interseptor (since about 2022).

## mitmproxy

mitmproxy is MIT licensed, widely used, and the best choice when you want to script traffic. It speaks HTTP/1, HTTP/2, HTTP/3 and WebSockets. It has three front-ends on one engine: `mitmproxy` for the terminal, `mitmweb` for the browser, and `mitmdump` as a scriptable CLI. The Python addon API is the point of the tool, and many testers already know Python. Current release v12.2.2, April 2026 ([mitmproxy.org](https://www.mitmproxy.org/), checked 2026-10-10).

**Cost.** Free, MIT.

Where Interseptor has an edge:

- A pentest workflow on top of the proxy: Intruder, authorization differentials, findings and reports.
- No Python environment to manage.

Where it has a real gap:

- HTTP/3. Interseptor uses HTTP/2 upstream and an HTTP/1.1 client leg on MITM connections; see [HTTP/2 and MITM]({{ '/http2/' | relative_url }}).
- Python addons are easier for most people than Starlark checks, and there are more of them.

## Postman, Insomnia and Bruno

These are not rivals. They are import sources. Keep authoring in Postman; import the collection here when you need to run it as several identities and keep the evidence. The importers are described in [collections]({{ '/collections/' | relative_url }}).

## When you should not use Interseptor

- **You need an automated active scan.** Use ZAP. It is free, the Automation Framework is good, and it can run next to Interseptor. The 31 passive checks here are a safety net on traffic you were already generating. They are not a scanner.
- **You need Collaborator-grade out-of-band testing.** Use Burp Pro. Interseptor ships a self-hosted catcher that is off by default and cannot be reached from a target on localhost without a tunnel you set up. See [architecture]({{ '/architecture/' | relative_url }}).
- **Your workflow depends on specific BApps.** Keep Burp. There is no equivalent store here. You can still import Burp saved-items XML next to your Burp work.
- **You need HTTP/3 interception.** Use mitmproxy.
- **Your client contract requires encryption at rest in the application.** Captured traffic, including credentials and tokens, is stored unencrypted under `~/.interseptor/`. The [architecture page]({{ '/architecture/' | relative_url }}) says so in bold. Full-disk encryption and an encrypted volume for `--data-dir` help, but if the contract wants application-layer encryption, this is a blocker today.
- **You need to bypass certificate pinning from the proxy.** Nothing does that. Interseptor detects pinning and tells you which instrumentation path to use; see [mobile testing]({{ '/mobile-testing/' | relative_url }}).
- **You are learning web security from scratch.** Start with the Web Security Academy, then come back.

## Objections

<div class="faq-list">
<details>
  <summary>It is four months old. I am not putting an engagement on it.</summary>
  <p>That is a fair position, and we cannot argue you out of it. What we can do is lower the cost of being wrong. The code is MIT, so you can read it. About 100k lines of source sit beside about 88k lines of tests. <code>go vet</code>, <code>go test</code> and <code>go test -race</code> gate every push. Projects export as portable bundles, so you can run it beside Burp on one engagement and walk away with your data. The <a href="{{ '/architecture/' | relative_url }}">architecture page</a> also lists the places where the shipped behaviour deliberately differs from the original design, including a feature left out because it could not be honest about what the server would do.</p>
</details>
<details>
  <summary>No active scanner, so it cannot replace Burp Pro.</summary>
  <p>Correct. It does not replace Burp Pro's scanner. What it replaces is paying $499 for manual tooling you already know how to use: Intruder without a throttle, saved projects, authorization differentials and reporting. If you need an automated active scan, run ZAP next to it.</p>
</details>
<details>
  <summary>Burp and ZAP already have MCP. Why switch for that?</summary>
  <p>You should not switch for that alone. Both have official MCP integrations, and Caido has a community one. There are three narrower differences. The server is in the binary, so there is no extension to install or keep in version step. The first-party tool set is broader: 133 tools from recon through report export. And findings an agent files are held to an evidence contract: a title-only finding is not accepted, only a real browser or device screenshot counts as visual proof, and the final export returns 409 until the gaps close. If you re-verify your agent's findings by hand today, that last point is the one that matters. See the <a href="{{ '/ai-agents/' | relative_url }}">agent guide</a>.</p>
</details>
<details>
  <summary>My muscle memory, extensions and team workflow are all Burp.</summary>
  <p>Then keep Burp. Interseptor imports Burp saved-items XML into History, imports Postman, OpenAPI, curl, Insomnia, Bruno, HAR and SOAP collections, and exports HAR and project bundles, so it can sit beside Burp rather than replace it. The realistic first use is the agent-assisted lane and the report-writing lane, not removing your proxy.</p>
</details>
<details>
  <summary>Unencrypted data at rest rules it out for client work.</summary>
  <p>For some contracts it does. Captured traffic is stored unencrypted under <code>~/.interseptor/</code>. The usual mitigations apply: full-disk encryption, an encrypted volume for <code>--data-dir</code>, one project per engagement, and the retention policy described in <a href="{{ '/engagement-closeout/' | relative_url }}">engagement close-out</a>. If your contract requires application-layer encryption, that does not meet it.</p>
</details>
</div>

## Try it

Download a release binary for Linux, macOS or Windows, or install with Go 1.25 or later. The steps are in [getting started]({{ '/getting-started/' | relative_url }}). The [feature list]({{ '/features/' | relative_url }}) and the [documentation home]({{ '/' | relative_url }}) cover the rest. Releases are on [GitHub](https://github.com/Veyal/interseptor/releases).
