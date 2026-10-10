---
layout: home
title: Interseptor — an intercepting proxy that makes you prove it
description: An HTTP/HTTPS intercepting proxy and pentest workbench in one static Go binary. Intercept, replay and fuzz, then export a report that will not go final until the evidence is real. MIT licensed, with a 133-tool MCP server for agent-assisted testing.
image_alt: The Interseptor history inspector, with a captured request and its response open side by side.
sections:
  - title: What it does
    id: capabilities
  - title: Workflow
    id: workflow
  - title: For agents
    id: agents
  - title: Honestly
    id: honestly
  - title: Questions
    id: questions
---
<section class="hero" id="hero">
  <div class="landing-wrap hero-inner">
    <div class="hero-copy">
      <p class="mono-label">One static binary · v{{ site.data.release.version }} · MIT</p>
      <h1 class="hero-title">Finding the bug is the <span class="squiggle">easy half<svg viewBox="0 0 200 12" preserveAspectRatio="none" aria-hidden="true"><path d="M3 9C40 3 80 3 197 8"/></svg></span>.</h1>
      <p class="hero-sub">Interseptor intercepts, replays and fuzzes like you would expect. Then it refuses to call a report final until the evidence is actually attached. One Go binary, no JVM, no licence tier.</p>
      <div class="hero-actions">
        <a class="btn-primary btn-large" href="{{ '/getting-started/' | relative_url }}">Get started <span aria-hidden="true">→</span></a>
        <a class="btn-ghost btn-large" href="{{ '/compare/' | relative_url }}">Compared to Burp &amp; ZAP <span aria-hidden="true">↗</span></a>
      </div>
      <div class="command hero-install">
        <span class="command-prompt" aria-hidden="true">$</span>
        <code class="command-text" id="install-go">go install github.com/Veyal/interseptor/cmd/interseptor@latest</code>
        <button class="chip copy-button" type="button" data-copy="#install-go" aria-label="Copy the install command">Copy</button>
      </div>
      <p class="hero-meta"><span>Needs Go 1.25+ to build</span><span>Or a prebuilt binary: macOS, Linux, Windows</span><span>amd64 &amp; arm64</span></p>
    </div>
    <div class="hero-visual">
      <figure class="shot">
        <div class="shot-frame">
          <div class="shot-chrome" aria-hidden="true">
            <span class="shot-dots"><i></i><i></i><i></i></span>
            <span class="shot-chrome-label">Interseptor · History</span>
          </div>
          <div class="shot-media" style="--shot-ratio: 1440 / 900">
            <picture>
              <source type="image/webp" srcset="{{ '/website/assets/img/history-inspector-light.webp' | relative_url }}">
              <img class="shot-img shot-img-light" src="{{ '/website/assets/img/history-inspector-light.png' | relative_url }}" width="1440" height="900" decoding="async" alt="The history list of captured requests, with one request and its response opened side by side in the inspector.">
            </picture>
            <picture>
              <source type="image/webp" srcset="{{ '/website/assets/img/history-inspector-dark.webp' | relative_url }}">
              <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/history-inspector-dark.png' | relative_url }}" width="1440" height="900" decoding="async" alt="" aria-hidden="true">
            </picture>
          </div>
        </div>
      </figure>
    </div>
  </div>
</section>

<section class="landing-section landing-band" aria-label="Project facts">
  <div class="landing-wrap">
    <div class="stat-row">
      <div class="stat card-surface"><p class="stat-value">31 MB</p><p class="stat-label">One static binary, no cgo</p></div>
      <div class="stat card-surface"><p class="stat-value">133</p><p class="stat-label">MCP tools, built in</p></div>
      <div class="stat card-surface"><p class="stat-value">2,977</p><p class="stat-label">Go test functions</p></div>
      <div class="stat card-surface"><p class="stat-value">MIT</p><p class="stat-label">No seats, no account, no tier</p></div>
    </div>
  </div>
</section>

<section class="landing-section" id="capabilities">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">What it does</p>
      <h2 class="section-heading">One process, from first request to final report.</h2>
      <p class="section-lede">Nothing to install alongside it. No JVM, no Python, no Node at runtime, and nothing phoning home except a release check you can switch off.</p>
    </div>
    <div class="capability-grid">
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-pink" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 3v5M15 3v5M6 8h12v3a6 6 0 0 1-12 0V8ZM12 17v4"/></svg></span>
        <h3 class="capability-title">Intercepting proxy</h3>
        <p class="capability-body">Hold, edit, forward or drop requests <em>and</em> responses through a local CA, with ordered match-and-replace.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-sky" aria-hidden="true"><svg viewBox="0 0 24 24"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></svg></span>
        <h3 class="capability-title">History you can search</h3>
        <p class="capability-body">Metadata in SQLite, bodies content-addressed and streamed to disk. Search headers, tags and bodies, or save a Starlark predicate.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-yellow" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 20 20 4M14 4h6v6"/></svg></span>
        <h3 class="capability-title">Repeater &amp; Intruder</h3>
        <p class="capability-body">Hand-replay anything. Then Sniper, Pitchfork and Race, with thread and delay controls, payload processing and anomaly flagging.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-mint" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M5 21V4M5 4h13l-2.5 4.5L18 13H5"/></svg></span>
        <h3 class="capability-title">Findings &amp; reports</h3>
        <p class="capability-body">Evidence required and graded by provenance. CVSS v4, immutable revisions, and Markdown, self-contained HTML or JSON out.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-grape" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h10"/></svg></span>
        <h3 class="capability-title">Collections</h3>
        <p class="capability-body">Import Postman, OpenAPI, curl, Insomnia, Bruno, HAR, Burp, GraphQL, <code>.http</code> and WSDL. Run them as several identities.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-pink" aria-hidden="true"><svg viewBox="0 0 24 24"><rect x="7" y="2.5" width="10" height="19" rx="2"/><path d="M11 18.5h2"/></svg></span>
        <h3 class="capability-title">Mobile devices</h3>
        <p class="capability-body">Android CA and proxy over <code>adb</code>, iOS Simulator via <code>simctl</code>, physical devices by profile. It detects pinning; it does not defeat it.</p>
      </article>
    </div>
  </div>
</section>

<section class="landing-section" id="workflow">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Workflow</p>
      <h2 class="section-heading">The part other tools leave to you at 2am.</h2>
      <p class="section-lede">Most proxies help you find the bug and then hand you a blank document. This one tracks what each artefact actually proves.</p>
    </div>
    <div class="feature-rows">
      <article class="feature-row">
        <div class="feature-copy">
          <h3 class="feature-heading">A report that will not go final on a bare claim.</h3>
          <p class="feature-body">Every finding needs a captured flow or an image attached in the same run that files it. Ask for a final export with a gap still open and you get a 409 back with the failing checks named, not a polished document with a hole in it.</p>
          <ul class="feature-points">
            <li>Only a real browser or device screenshot counts as visual proof</li>
            <li>Generated previews carry a marker in the PNG, and the store refuses them as screenshots</li>
            <li>Revisions are immutable; restoring an old one appends rather than rewrites</li>
          </ul>
          <a class="feature-link" href="{{ '/findings-and-reporting/' | relative_url }}">How findings work <span aria-hidden="true">→</span></a>
        </div>
        <figure class="shot shot-tight">
          <div class="shot-frame">
            <div class="shot-chrome" aria-hidden="true"><span class="shot-dots"><i></i><i></i><i></i></span><span class="shot-chrome-label">Interseptor · Findings</span></div>
            <div class="shot-media" style="--shot-ratio: 1440 / 900">
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/findings-document-light.webp' | relative_url }}">
                <img class="shot-img shot-img-light" src="{{ '/website/assets/img/findings-document-light.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="A finding open as a single document, with its attached evidence listed beneath the reproduction steps.">
              </picture>
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/findings-document-dark.webp' | relative_url }}">
                <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/findings-document-dark.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="" aria-hidden="true">
              </picture>
            </div>
          </div>
        </figure>
      </article>
      <article class="feature-row">
        <div class="feature-copy">
          <h3 class="feature-heading">Collections become engagement artefacts, not just requests.</h3>
          <p class="feature-body">Import the collection the developers already wrote, then run it as every identity you have saved and diff what comes back. The identity matrix flags the cells that answered the wrong caller — as candidates for you to confirm, which is exactly what the code calls them.</p>
          <ul class="feature-points">
            <li>Imported scripts stay quarantined until you trust the exact hash</li>
            <li>Exports scrub credentials on every path, with no opt-out</li>
            <li>A bulk run tells you how many live requests it will send, and how many change state, before anything leaves</li>
          </ul>
          <a class="feature-link" href="{{ '/collections/' | relative_url }}">Collections guide <span aria-hidden="true">→</span></a>
        </div>
        <figure class="shot shot-tight">
          <div class="shot-frame">
            <div class="shot-chrome" aria-hidden="true"><span class="shot-dots"><i></i><i></i><i></i></span><span class="shot-chrome-label">Interseptor · Collections</span></div>
            <div class="shot-media" style="--shot-ratio: 1440 / 900">
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/collections-light.webp' | relative_url }}">
                <img class="shot-img shot-img-light" src="{{ '/website/assets/img/collections-light.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="A collection tree beside an open request and its response, with the environment selector in the toolbar.">
              </picture>
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/collections-dark.webp' | relative_url }}">
                <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/collections-dark.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="" aria-hidden="true">
              </picture>
            </div>
          </div>
        </figure>
      </article>
      <article class="feature-row">
        <div class="feature-copy">
          <h3 class="feature-heading">Hold a response mid-flight, then fuzz it properly.</h3>
          <p class="feature-body">Response interception is the part people assume is missing. It is not. And the attack runner is not rate-limited, because there is no tier to upsell you to.</p>
          <ul class="feature-points">
            <li>Sniper, Pitchfork and Race modes, with grep match and extract</li>
            <li>Runs are recorded and render as report-ready PNGs</li>
            <li>A render reports counts; it never claims a race or a rate-limit bypass is confirmed</li>
          </ul>
          <a class="feature-link" href="{{ '/features/' | relative_url }}">All 32 features <span aria-hidden="true">→</span></a>
        </div>
        <figure class="shot shot-tight">
          <div class="shot-frame">
            <div class="shot-chrome" aria-hidden="true"><span class="shot-dots"><i></i><i></i><i></i></span><span class="shot-chrome-label">Interseptor · Intruder</span></div>
            <div class="shot-media" style="--shot-ratio: 1440 / 900">
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/intruder-light.webp' | relative_url }}">
                <img class="shot-img shot-img-light" src="{{ '/website/assets/img/intruder-light.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="An Intruder run in progress, listing per-request results with anomalies flagged.">
              </picture>
              <picture>
                <source type="image/webp" srcset="{{ '/website/assets/img/intruder-dark.webp' | relative_url }}">
                <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/intruder-dark.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="" aria-hidden="true">
              </picture>
            </div>
          </div>
        </figure>
      </article>
    </div>
  </div>
</section>

<section class="landing-section landing-band" id="agents">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">For AI agents</p>
      <h2 class="section-heading">Your agent drives the same engine you do.</h2>
      <p class="section-lede">133 MCP tools over stdio and Streamable-HTTP, in the binary. Nothing to install, nothing to keep in version lockstep.</p>
    </div>
    <div class="feature-rows">
      <article class="feature-row">
        <div class="feature-copy">
          <h3 class="feature-heading">Point an agent at one URL and it knows the whole tool.</h3>
          <p class="feature-body">Paste <code>Read https://veyal.github.io/interseptor/llms-full.txt and follow it.</code> into any MCP client. It covers install, the REST and MCP surfaces, the hard rules, and how to write a finding so every agent produces the same report. That file is generated from these docs and CI fails if it drifts.</p>
          <ul class="feature-points">
            <li>The agent can recon, replay, fuzz, run an authz matrix and file the finding — through the same control plane the UI uses</li>
            <li>Its calls are stamped, so you can tell its traffic from yours in History</li>
            <li>It can pause and ask you before anything with real blast radius</li>
            <li>Scope policy and script trust are refused to AI callers outright — an agent cannot loosen either</li>
          </ul>
          <a class="feature-link" href="{{ '/ai-agents/' | relative_url }}">Agent guide <span aria-hidden="true">→</span></a>
        </div>
        <div class="terminal" aria-label="Example agent session">
          <div class="terminal-bar" aria-hidden="true"><span class="terminal-dots"><i></i><i></i><i></i></span><span class="terminal-title">claude</span></div>
          <div class="terminal-body">
            <p class="terminal-line"><span class="terminal-prompt">&gt;</span> Read https://veyal.github.io/interseptor/llms-full.txt and follow it.</p>
            <p class="terminal-line terminal-out">Connected over MCP. 133 tools available.</p>
            <p class="terminal-line"><span class="terminal-prompt">&gt;</span> Run the Orders collection as anonymous, user and admin. Diff them.</p>
            <p class="terminal-line terminal-out">collection_identity_matrix → 3 identities × 24 requests</p>
            <p class="terminal-line terminal-out">2 cells answered the wrong caller. Candidates, not confirmed.</p>
            <p class="terminal-line terminal-out">create_finding → #41, evidence: flow 1042 attached</p>
          </div>
        </div>
        <p class="shot-caption">Illustration of the shape of a session. <code>collection_identity_matrix</code> and <code>create_finding</code> are real tools; the numbers are made up.</p>
      </article>
    </div>
    <p class="section-lede">The core is model-free: no provider keys, no built-in chat, no autonomous loop. You bring the agent; Interseptor supplies deterministic tools and keeps the receipts.</p>
  </div>
</section>

<section class="landing-section" id="honestly">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Honestly</p>
      <h2 class="section-heading">What it does not do.</h2>
      <p class="section-lede">Worth knowing before you download it rather than after.</p>
    </div>
    <div class="capability-grid">
      <article class="capability-card card-surface">
        <h3 class="capability-title">No active scanner</h3>
        <p class="capability-body">31 passive checks over traffic you already captured, extensible in sandboxed Starlark. If you want an automated active scan, ZAP is free and good at it.</p>
      </article>
      <article class="capability-card card-surface">
        <h3 class="capability-title">It is new</h3>
        <p class="capability-body">First commit June 2026. Burp's lineage goes back to 2003, ZAP's to 2010. It is MIT, so read the code: roughly 100k lines of source against 88k of tests.</p>
      </article>
      <article class="capability-card card-surface">
        <h3 class="capability-title">No extension marketplace</h3>
        <p class="capability-body">Starlark checks and signed rule packs, and that is all. Burp has 300+ BApps from 250+ authors; that gap is real.</p>
      </article>
      <article class="capability-card card-surface">
        <h3 class="capability-title">No hosted out-of-band service</h3>
        <p class="capability-body">There is a self-hosted catcher, off by default, and it needs your own tunnel to be reachable. It is not Burp Collaborator.</p>
      </article>
      <article class="capability-card card-surface">
        <h3 class="capability-title">No HTTP/3</h3>
        <p class="capability-body">HTTP/2 upstream with an HTTP/1.1 client leg on the MITM path. mitmproxy does HTTP/3 today.</p>
      </article>
      <article class="capability-card card-surface">
        <h3 class="capability-title">Data at rest is not encrypted</h3>
        <p class="capability-body">Captured traffic sits unencrypted under your data directory, by design. Secure the machine it runs on.</p>
      </article>
    </div>
    <div class="section-head section-head-center">
      <p class="section-lede">The architecture doc lists where the build deviates from its own spec, &ldquo;recorded so they are not mistaken for gaps&rdquo; — including a control left out of the UI because it &ldquo;could not be honest about what the server will do&rdquo;.</p>
      <a class="btn-ghost" href="{{ '/compare/' | relative_url }}">The full 16-row comparison <span aria-hidden="true">→</span></a>
    </div>
  </div>
</section>

<section class="landing-section" id="questions">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Questions</p>
      <h2 class="section-heading">The ones worth asking first.</h2>
    </div>
    <div class="faq-list">
      <details>
        <summary>Does this replace Burp Professional?</summary>
        <p>Not its scanner, and not Collaborator. What it replaces is paying $499 a year for manual tooling you already know how to drive — unthrottled fuzzing, projects that persist, authz differentials and reporting. Run it next to whatever you already use; projects export as JSON or a full archive, so trying it is reversible.</p>
      </details>
      <details>
        <summary>Burp and ZAP have MCP too. Why does yours matter?</summary>
        <p>They do, and anyone saying otherwise is selling you something. Three honest differences: ours is in the binary rather than an extension you install and version-match; it is 133 first-party tools covering the workflow end to end including findings and reporting; and <code>create_finding</code> makes the agent attach evidence before the finding counts. That last one has no equivalent anywhere.</p>
      </details>
      <details>
        <summary>Is my captured traffic going anywhere?</summary>
        <p>No analytics, no telemetry endpoints, no provider keys in the binary. The only host it contacts on its own is GitHub, to check whether a newer release exists, and you can turn that off. Everything else leaves only if you deliberately open a tunnel. Note that data at rest is unencrypted.</p>
      </details>
      <details>
        <summary>Can I use it headless, over SSH?</summary>
        <p>Yes. It is one binary with a loopback control API, and <code>interseptor run</code> drives a collection from CI and emits JUnit. The UI is served over HTTP, so forward a port and use a browser wherever you are.</p>
      </details>
      <details>
        <summary>Will it defeat certificate pinning?</summary>
        <p>No, and neither will any other proxy. It detects pinning and tells you which instrumentation path you need.</p>
      </details>
    </div>
  </div>
</section>

<section class="cta-band">
  <div class="landing-wrap cta-inner">
    <h2 class="cta-heading">Install it and point a browser at it.</h2>
    <p class="cta-body">Two commands and a certificate. If it is not useful inside ten minutes, nothing was lost but a download.</p>
    <div class="command">
      <span class="command-prompt" aria-hidden="true">$</span>
      <code class="command-text" id="install-go-footer">go install github.com/Veyal/interseptor/cmd/interseptor@latest</code>
      <button class="chip copy-button" type="button" data-copy="#install-go-footer" aria-label="Copy the install command">Copy</button>
    </div>
    <div class="cta-actions">
      <a class="btn-primary" href="{{ '/getting-started/' | relative_url }}">Getting started <span aria-hidden="true">→</span></a>
      <a class="btn-ghost" href="{{ '/ai-agents/' | relative_url }}">For AI agents <span aria-hidden="true">↗</span></a>
      <a class="btn-ghost" href="https://github.com/Veyal/interseptor/releases">Prebuilt binaries <span aria-hidden="true">↗</span></a>
    </div>
  </div>
</section>
