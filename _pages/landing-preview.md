---
layout: home
permalink: /landing-preview/
title: Landing component preview
description: Scratch page that renders every landing-page component in both themes. Not linked from the site.
noindex: true
docs_url: /getting-started/
sections:
  - title: Hero
    id: hero
  - title: Capabilities
    id: capabilities
  - title: Workflow
    id: workflow
  - title: Compare
    id: compare
  - title: Questions
    id: questions
---
<!-- Scratch page. Copy is placeholder unless noted; the numbers in the trust row were
     measured in this worktree (31 MB stripped CGO_ENABLED=0 binary, 2,977 Go test
     functions, 74 packages, MIT). Screenshot filenames are provisional: swap in whatever
     the image pass actually writes to website/assets/img/. -->
<section class="hero" id="hero">
  <div class="landing-wrap hero-inner">
    <div class="hero-copy">
      <p class="mono-label">One static binary · v{{ site.data.release.version }}</p>
      <h1 class="hero-title">Intercept it, read it, <span class="squiggle">prove it<svg viewBox="0 0 200 12" preserveAspectRatio="none" aria-hidden="true"><path d="M3 9C40 3 80 3 197 8"/></svg></span>.</h1>
      <p class="hero-sub">An intercepting proxy, a repeater, an attack runner, and a findings workflow that keeps every claim attached to the request that proves it. No JVM, no agent, no account.</p>
      <div class="hero-actions">
        <a class="btn-primary btn-large" href="{{ '/getting-started/' | relative_url }}">Install in one command <span aria-hidden="true">→</span></a>
        <a class="btn-ghost btn-large" href="{{ '/features/' | relative_url }}">See what it does <span aria-hidden="true">↗</span></a>
      </div>
      <div class="command hero-install">
        <span class="command-prompt" aria-hidden="true">$</span>
        <code class="command-text" id="install-brew">brew install Veyal/tap/interseptor</code>
        <button class="chip copy-button" type="button" data-copy="#install-brew" aria-label="Copy the Homebrew install command">Copy</button>
      </div>
      <p class="hero-meta"><span>macOS, Linux, Windows</span><span>MIT licensed</span><span>Loopback by default</span></p>
    </div>
    <div class="hero-visual">
      <figure class="shot">
        <div class="shot-frame">
          <div class="shot-chrome" aria-hidden="true">
            <span class="shot-dots"><i></i><i></i><i></i></span>
            <span class="shot-chrome-label">Interseptor · History</span>
          </div>
          <div class="shot-media" style="--shot-ratio: 1440 / 900">
            <img class="shot-img shot-img-light" src="{{ '/website/assets/img/history-inspector-light.png' | relative_url }}" width="1440" height="900" decoding="async" alt="The history inspector listing captured requests, with one request and its response opened side by side.">
            <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/history-inspector-dark.png' | relative_url }}" width="1440" height="900" decoding="async" alt="" aria-hidden="true">
          </div>
        </div>
        <figcaption class="shot-caption">Placeholder: history inspector, light and dark artwork swapped by theme.</figcaption>
      </figure>
    </div>
  </div>
</section>

<section class="landing-section landing-band" aria-label="Project facts">
  <div class="landing-wrap">
    <div class="stat-row">
      <div class="stat card-surface"><p class="stat-value">31 MB</p><p class="stat-label">One static binary, no cgo</p></div>
      <div class="stat card-surface"><p class="stat-value">0</p><p class="stat-label">Runtime dependencies to install</p></div>
      <div class="stat card-surface"><p class="stat-value">2,977</p><p class="stat-label">Go test functions across 74 packages</p></div>
      <div class="stat card-surface"><p class="stat-value">MIT</p><p class="stat-label">Licence, no seats, no telemetry</p></div>
    </div>
  </div>
</section>

<section class="landing-section" id="capabilities">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Capabilities</p>
      <h2 class="section-heading">Everything an engagement needs in one process.</h2>
      <p class="section-lede">Placeholder body copy. Each card is one capability and one line, nothing more.</p>
    </div>
    <div class="capability-grid">
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-pink" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3 8h16l-4-4M21 16H5l4 4"/></svg></span>
        <h3 class="capability-title">MITM proxy</h3>
        <p class="capability-body">Intercept, edit, and replay HTTP/1.1, HTTP/2, and WebSocket traffic.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-sky" aria-hidden="true"><svg viewBox="0 0 24 24"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></svg></span>
        <h3 class="capability-title">History search</h3>
        <p class="capability-body">Query every captured body by header, status, host, or regex.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-mint" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M5 21V4M5 4h13l-2.5 4.5L18 13H5"/></svg></span>
        <h3 class="capability-title">Findings</h3>
        <p class="capability-body">Draft, revise, and export findings that cite their own evidence.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-yellow" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3v18M3 12h18"/></svg></span>
        <h3 class="capability-title">Attack runner</h3>
        <p class="capability-body">Payload sets, markers, and diffing without leaving the capture.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-grape" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h10"/></svg></span>
        <h3 class="capability-title">Scope and rules</h3>
        <p class="capability-body">Keep capture, scanning, and automation inside the agreed targets.</p>
      </article>
      <article class="capability-card card-surface card-hover">
        <span class="icon-badge badge-sky" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 8h10v9H7zM10 8V5h4v3"/></svg></span>
        <h3 class="capability-title">REST and MCP</h3>
        <p class="capability-body">Drive the same workspace from scripts or an AI agent.</p>
      </article>
    </div>
  </div>
</section>

<section class="landing-section" id="workflow">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Workflow</p>
      <h2 class="section-heading">From first request to a finished report.</h2>
    </div>
    <div class="feature-rows">
      <article class="feature-row">
        <div class="feature-copy">
          <p class="mono-label">Step one</p>
          <h3 class="feature-heading">Point a browser at it and start capturing.</h3>
          <p class="feature-body">Placeholder body copy for the alternating feature row. The screenshot sits opposite the text on wide screens and below it on narrow ones.</p>
          <ul class="feature-points">
            <li>Generated CA, per-host certificates, no manual OpenSSL</li>
            <li>Bodies stream to disk, so large responses stay readable</li>
            <li>Forwarding never waits on capture</li>
          </ul>
          <a class="feature-link" href="{{ '/proxy-and-tls/' | relative_url }}">Proxy and TLS guide <span aria-hidden="true">→</span></a>
        </div>
        <figure class="shot">
          <div class="shot-frame">
            <div class="shot-chrome" aria-hidden="true">
              <span class="shot-dots"><i></i><i></i><i></i></span>
              <span class="shot-chrome-label">Interseptor · Proxy</span>
            </div>
            <div class="shot-media" style="--shot-ratio: 1440 / 900">
              <img class="shot-img shot-img-light" src="{{ '/website/assets/img/proxy-listeners-light.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="Proxy settings showing two listeners and the generated certificate authority.">
              <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/proxy-listeners-dark.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="" aria-hidden="true">
            </div>
          </div>
          <figcaption class="shot-caption">Placeholder: proxy listeners and certificate status.</figcaption>
        </figure>
      </article>
      <article class="feature-row">
        <div class="feature-copy">
          <p class="mono-label">Step two</p>
          <h3 class="feature-heading">Write the finding while the evidence is open.</h3>
          <p class="feature-body">Placeholder body copy. This row is the second child, so its text column moves to the right automatically.</p>
          <ul class="feature-points">
            <li>Every finding links the requests that prove it</li>
            <li>Revisions are kept, so a report can be re-checked</li>
            <li>Export to Markdown or HTML with the evidence attached</li>
          </ul>
          <a class="feature-link" href="{{ '/findings-and-reporting/' | relative_url }}">Findings guide <span aria-hidden="true">→</span></a>
        </div>
        <figure class="shot">
          <div class="shot-frame">
            <div class="shot-chrome" aria-hidden="true">
              <span class="shot-dots"><i></i><i></i><i></i></span>
              <span class="shot-chrome-label">Interseptor · Findings</span>
            </div>
            <div class="shot-media" style="--shot-ratio: 1440 / 900">
              <img class="shot-img shot-img-light" src="{{ '/website/assets/img/findings-report-light.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="A finding editor with its linked evidence list and readiness checklist.">
              <img class="shot-img shot-img-dark" src="{{ '/website/assets/img/findings-report-dark.png' | relative_url }}" width="1440" height="900" loading="lazy" decoding="async" alt="" aria-hidden="true">
            </div>
          </div>
          <figcaption class="shot-caption">Placeholder: finding editor with linked evidence.</figcaption>
        </figure>
      </article>
    </div>
  </div>
</section>

<section class="landing-section landing-band" aria-labelledby="terminal-heading">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Quick start</p>
      <h2 class="section-heading" id="terminal-heading">Two commands and you are capturing.</h2>
      <p class="section-lede">Prompts and sample output carry <code>data-copy-skip</code>, so the copy button hands over the commands only.</p>
    </div>
    <div class="terminal">
      <div class="terminal-bar">
        <span class="terminal-dots" aria-hidden="true"><i></i><i></i><i></i></span>
        <span class="terminal-title">zsh</span>
        <button class="chip copy-button" type="button" data-copy="#quickstart-commands" aria-label="Copy the quick start commands">Copy</button>
      </div>
      <pre class="terminal-body" id="quickstart-commands"><code><span class="terminal-line"><span class="terminal-prompt" data-copy-skip aria-hidden="true">$ </span>go install github.com/Veyal/interseptor/cmd/interseptor@latest
</span><span class="terminal-line"><span class="terminal-prompt" data-copy-skip aria-hidden="true">$ </span>interseptor --project acme-web --open
</span><span class="terminal-line terminal-out" data-copy-skip>proxy    127.0.0.1:8080
</span><span class="terminal-line terminal-out" data-copy-skip>control  http://127.0.0.1:9966
</span><span class="terminal-line terminal-out" data-copy-skip>project  acme-web — 0 findings, 0 captures
</span></code></pre>
    </div>
    <p class="shot-caption">Placeholder output. The single-line variant of the same component is in the hero and the closing band; see the <a href="{{ '/cli-reference/' | relative_url }}">CLI reference</a> for the real flags.</p>
  </div>
</section>

<section class="landing-section" id="compare">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Compare</p>
      <h2 class="section-heading">What you get without a licence server.</h2>
      <p class="section-lede">Placeholder matrix. Every cell states its answer in words and in shape, so it reads the same without colour.</p>
    </div>
    <div class="table-wrap matrix" tabindex="0" role="region" aria-label="Capability comparison (scrollable table)">
      <table>
        <caption class="sr-only">Capabilities compared across Interseptor and two placeholder alternatives</caption>
        <thead>
          <tr>
            <th scope="col">Capability</th>
            <th scope="col" class="matrix-self">Interseptor</th>
            <th scope="col">Tool B</th>
            <th scope="col">Tool C</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">Single static binary</th>
            <td class="matrix-self"><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>Yes</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>No</span></td>
            <td><span class="mark mark-partial"><span class="mark-glyph" aria-hidden="true">~</span>Partial</span></td>
          </tr>
          <tr>
            <th scope="row">Runs without a JVM</th>
            <td class="matrix-self"><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>Yes</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>No</span></td>
            <td><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>Yes</span></td>
          </tr>
          <tr>
            <th scope="row">Findings tied to evidence</th>
            <td class="matrix-self"><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>Yes</span></td>
            <td><span class="mark mark-partial"><span class="mark-glyph" aria-hidden="true">~</span>Partial</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>No</span></td>
          </tr>
          <tr>
            <th scope="row">MCP server for agents</th>
            <td class="matrix-self"><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>Yes</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>No</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>No</span></td>
          </tr>
          <tr>
            <th scope="row">Price for the full toolkit</th>
            <td class="matrix-self"><span class="mark mark-yes"><span class="mark-glyph" aria-hidden="true">✓</span>MIT</span></td>
            <td><span class="mark mark-no"><span class="mark-glyph" aria-hidden="true">✕</span>Per seat</span></td>
            <td><span class="mark mark-partial"><span class="mark-glyph" aria-hidden="true">~</span>Free tier</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="matrix-hint">Scroll the table sideways to compare <span aria-hidden="true">→</span></p>
    <p class="section-lede">Alias spelling used by <code>compare.md</code>: <code>.table-scroll</code> wrapper, <code>.comparison-table</code>, and self-drawing <code>.cell-*</code> spans.</p>
    <div class="table-scroll" tabindex="0" role="region" aria-label="Alias comparison table (scrollable)">
      <table class="comparison-table">
        <thead><tr><th scope="col">Capability</th><th scope="col" class="matrix-self">Interseptor</th><th scope="col">Tool B</th><th scope="col">Tool C</th></tr></thead>
        <tbody>
          <tr><th scope="row">Evidence-linked findings</th><td class="matrix-self"><span class="cell-yes">Yes</span></td><td><span class="cell-partial">Partial</span></td><td><span class="cell-no">No</span></td></tr>
          <tr><th scope="row">Published benchmark numbers</th><td class="matrix-self"><span class="cell-yes">Yes</span></td><td><span class="cell-unknown">Unknown</span></td><td><span class="cell-unknown">Unknown</span></td></tr>
        </tbody>
      </table>
    </div>
  </div>
</section>

<section class="landing-section" id="questions">
  <div class="landing-wrap">
    <div class="section-head">
      <p class="mono-label">Objections</p>
      <h2 class="section-heading">The questions a tester actually asks.</h2>
    </div>
    <div class="faq">
      <details class="faq-item card-surface">
        <summary class="faq-question">Why not just use the proxy I already have?</summary>
        <div class="faq-answer"><p>Placeholder answer. Keep it to two sentences and name the trade-off honestly rather than claiming a win.</p></div>
      </details>
      <details class="faq-item card-surface">
        <summary class="faq-question">Does it break TLS pinning on mobile?</summary>
        <div class="faq-answer"><p>Placeholder answer with a link out to the <a href="{{ '/proxy-and-tls/' | relative_url }}">proxy and TLS guide</a>.</p></div>
      </details>
      <details class="faq-item card-surface">
        <summary class="faq-question">Can an agent drive it?</summary>
        <div class="faq-answer"><p>Placeholder answer pointing at <a href="{{ '/ai-agents/' | relative_url }}">the agent guide</a> and <a href="{{ '/api-and-mcp/' | relative_url }}">the API and MCP reference</a>.</p></div>
      </details>
      <details class="faq-item card-surface">
        <summary class="faq-question">What happens to my capture data?</summary>
        <div class="faq-answer"><p>Placeholder answer. It stays on the machine; see <a href="{{ '/troubleshooting/' | relative_url }}">troubleshooting</a> if a capture goes missing.</p></div>
      </details>
    </div>
    <p class="section-lede">Alias spelling used by <code>compare.md</code>: a <code>.faq-list</code> wrapper around bare <code>&lt;details&gt;</code> elements, no per-item classes.</p>
    <div class="faq-list">
      <details>
        <summary>Does the alias wrapper need extra classes?</summary>
        <p>No. A bare <code>&lt;details&gt;</code> inside <code>.faq-list</code> picks up the card frame, the marker and the answer padding.</p>
      </details>
      <details>
        <summary>Is the keyboard behaviour the same?</summary>
        <p>Yes, both spellings are native disclosures with the same focus ring.</p>
      </details>
    </div>
  </div>
</section>

<section class="cta-band" aria-labelledby="cta-heading">
  <div class="landing-wrap cta-inner">
    <p class="mono-label">Get started</p>
    <h2 class="cta-heading" id="cta-heading">Install it, proxy one request, see for yourself.</h2>
    <p class="cta-body">Placeholder closing copy. One command, one browser setting, and the first capture lands in the history view.</p>
    <div class="command">
      <span class="command-prompt" aria-hidden="true">$</span>
      <code class="command-text" id="install-go">go install github.com/Veyal/interseptor/cmd/interseptor@latest</code>
      <button class="chip copy-button" type="button" data-copy="#install-go" aria-label="Copy the go install command">Copy</button>
    </div>
    <div class="cta-actions">
      <a class="btn-primary btn-large" href="{{ '/getting-started/' | relative_url }}">Read getting started <span aria-hidden="true">→</span></a>
      <a class="btn-ghost btn-large" href="https://github.com/Veyal/interseptor">Browse the source <span aria-hidden="true">↗</span></a>
    </div>
  </div>
</section>
