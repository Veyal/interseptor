---
layout: default
title: Interseptor documentation
description: Set up Interseptor, find your way around the workspace, and turn captured traffic into reviewed findings.
---
<section class="home-intro">
  <p class="eyebrow">Documentation · v{{ site.data.release.version }}</p>
  <h1>Work with<br>Interseptor.</h1>
  <p class="lede">Set up your proxy, inspect captured traffic, and keep findings tied to their evidence.</p>
  <div class="actions"><a class="button primary" href="{{ '/getting-started/' | relative_url }}">Get started <span aria-hidden="true">→</span></a><a class="text-link" href="{{ '/workspace/' | relative_url }}">Find your way around <span aria-hidden="true">↗</span></a></div>
</section>

<section class="start-paths" aria-label="Common tasks">
  <a class="path-link" href="{{ '/proxy-and-tls/' | relative_url }}"><span class="path-number">01 / SET UP</span><h2>Connect your browser</h2><p>Proxy listeners, certificates, and TLS settings.</p><span class="path-arrow" aria-hidden="true">↗</span></a>
  <a class="path-link" href="{{ '/workspace/#inspect-captured-traffic' | relative_url }}"><span class="path-number">02 / INSPECT</span><h2>Read a captured response</h2><p>History, response views, and the Session inspector.</p><span class="path-arrow" aria-hidden="true">↗</span></a>
  <a class="path-link" href="{{ '/findings-and-reporting/#read-and-edit-a-finding' | relative_url }}"><span class="path-number">03 / REVIEW</span><h2>Prepare a finding</h2><p>Evidence, revisions, readiness, and report export.</p><span class="path-arrow" aria-hidden="true">↗</span></a>
</section>

<section class="release-note" aria-labelledby="release-heading">
  <div class="release-note-label"><span class="release-dot" aria-hidden="true"></span><span>Current release</span><a href="https://github.com/Veyal/interseptor/releases/tag/v{{ site.data.release.version }}">v{{ site.data.release.version }}</a></div>
  <div><h2 id="release-heading">A closer look at your evidence</h2><p>Review linked findings, compare revisions, recover deleted records, and see what is missing before a final report.</p><div class="inline-links"><a href="{{ '/findings-and-reporting/' | relative_url }}">Findings guide <span aria-hidden="true">→</span></a><a href="https://github.com/Veyal/interseptor/releases/tag/v{{ site.data.release.version }}">Release notes <span aria-hidden="true">↗</span></a></div></div>
</section>

<section class="guide-directory" aria-labelledby="guides-heading">
  <div class="section-title"><h2 id="guides-heading">Pick up where you are</h2><a href="{{ '/features/' | relative_url }}">All features <span aria-hidden="true">→</span></a></div>
  <div class="guide-grid">
    <a href="{{ '/projects-and-data/' | relative_url }}"><h3>Projects &amp; data</h3><p>Workspace boundaries, imports, backups, and retention.</p><span aria-hidden="true">↗</span></a>
    <a href="{{ '/settings/' | relative_url }}"><h3>Settings</h3><p>Find the right section without searching every panel.</p><span aria-hidden="true">↗</span></a>
    <a href="{{ '/mobile-testing/' | relative_url }}"><h3>Mobile devices</h3><p>Device connections, proxy setup, and certificate trust.</p><span aria-hidden="true">↗</span></a>
    <a href="{{ '/api-and-mcp/' | relative_url }}"><h3>API &amp; MCP</h3><p>Connect clients and understand the exposed contracts.</p><span aria-hidden="true">↗</span></a>
    <a href="{{ '/message-codecs/' | relative_url }}"><h3>Message codecs</h3><p>Understand encoded bodies and their original captures.</p><span aria-hidden="true">↗</span></a>
    <a href="{{ '/troubleshooting/' | relative_url }}"><h3>Troubleshooting</h3><p>Connection failures, saved drafts, rendering, and updates.</p><span aria-hidden="true">↗</span></a>
  </div>
</section>
