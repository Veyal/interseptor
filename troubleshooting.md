---
layout: default
title: Troubleshooting
classification: reference
source: docs/troubleshooting.md
---
# Troubleshooting

Start with the first symptom that matches. Keep a terminal open for application logs and use a generic
test target before debugging a complex application.

## No traffic appears

1. Confirm Interseptor reports the expected proxy address and the port is not used by another process.
2. Send a simple HTTP request through that exact proxy.
3. Check client bypass lists (`localhost`, private ranges, or VPN-managed exclusions).
4. For a phone, verify LAN reachability and workstation firewall rules.
5. For an app, check whether it ignores the operating-system proxy or uses QUIC/HTTP3 directly.
6. Disable History filters and confirm capture policy is not limited to a mismatched scope.

## HTTP works but HTTPS fails

The client does not trust Interseptor's CA, is using a different trust store, has an incorrect clock,
or pins the server certificate. Install and explicitly trust the CA in the client actually making the
request. A browser working while one app fails strongly suggests app-specific trust or pinning.

TLS passthrough restores connectivity but removes HTTP visibility. Use it only when that tradeoff is
intentional.

## HTTPS returns an upstream TLS or 502 error

If origin verification is enabled, inspect the origin certificate for hostname, expiry, chain, and
private-CA trust. Add a narrow verification exception only for a known test host. For an HTTPS chained
proxy, install its CA in the upstream proxy CA field; origin exceptions do not weaken upstream-proxy
verification.

For `x509: cannot validate certificate for <IP> because it doesn't contain any IP SANs`, the URL uses
an IP that the certificate does not identify. Prefer the certificate's DNS hostname. For an authorized
test target, select the failed History row and use **Settings → TLS / CA → Origin TLS verification
exceptions → Add selected History host**. Installing a CA alone cannot fix a name mismatch.

## Repeated 407 or upstream authentication errors

A browser-visible `407` now always means the configured chained upstream proxy rejected its
credentials — Interseptor's own proxy listeners no longer require authentication. Check the
upstream settings and the toast/log saying “upstream proxy authentication required”.

For upstream setup, select a mode instead of typing a URL: **HTTP/HTTPS** for an HTTP CONNECT proxy,
**SOCKS5** for local DNS, or **SOCKS5H** for proxy-side DNS. Verify host and port in the status summary.
Private CA PEM applies only to HTTPS upstream proxies.

If captured traffic works through the upstream but Repeater returns `502`, confirm the saved summary,
then reopen Settings and save once. Current releases apply one shared upstream route to capture,
Repeater, Intruder, login macros, and custom-check sends. The Repeater flow's error field
contains the upstream dial, DNS, TLS, or authentication failure.

## Send to Repeater does not look right

Use the action on the attached PoC flow, wait for “loaded #… into Repeater,” and verify method, complete
URL, headers, and body before sending. Interseptor reuses a tab for the same scheme, host, port, and
queryless path; query values remain in the loaded request. A deleted/missing evidence flow cannot be
sent and must be recaptured.

## Repeater response shows HTML source

Choose **Render** above the response to preview HTML. The tab appears for HTML responses,
including responses selected from Repeater's History. Raw, Pretty, and Decoded remain available.
Render displays the captured body in a sandbox with scripts disabled; JavaScript-driven pages
may therefore look incomplete. Large HTML responses offer **Download body** or **Show anyway**.

## Saved workspace does not finish loading

Wait for the workspace status to offer **Reload** rather than clearing browser data. Reload retries
module startup, project identification, and Repeater/Intruder hydration without deleting drafts. If
the status says project-scoped tools are locked, confirm the active project is still available and
then reload; Interseptor deliberately does not guess a project after an identity failure. A recovery
warning means an older, malformed, or oversized browser draft was kept intact and the next explicit
valid edit will create its replacement and resume project synchronization. See [Project boundaries]({{ "/projects-and-data/" | relative_url }}#project-boundaries)
for storage and size limits.

If the warning says legacy migration is deferred until project ownership is known, keep working or
reload after the project list is available. Interseptor leaves the older legacy draft untouched,
while new edits continue saving to the canonical project-directory browser key and project storage.
The same guard applies to older unscoped drafts, which cannot be assigned when several projects,
duplicate project names, or malformed project-list entries make ownership uncertain.

## Finding layout or evidence is incomplete

Switch to Edit to reveal block controls and add actions. Read mode intentionally hides edit-only
controls. Use one step note per action, annotate each flow, and keep screenshots inside the article
width. If a finding says evidence was deleted, restore from a full archive or recapture it; the marker
does not contain the original body.

## UI cannot connect or keeps returning to login

On loopback, open the exact control address printed at startup. For remote access, use `/login` and a
valid key; read keys cannot perform mutations. Confirm the browser origin matches the exposed control
URL and that a tunnel or reverse proxy preserves same-origin behavior. Recreate expired keys rather
than weakening control-plane guards.

## Port already in use

Stop the existing instance with `interseptor stop`, choose `--proxy-port` and `--control-port`, or use
the launcher for multiple projects. Give every manual instance unique proxy and control ports.

## High memory or disk use

Bodies stream to content-addressed files, but a long engagement can still retain many flows. Configure
age/count retention, purge noisy hosts, then run body-file GC. Large responses are deliberately capped
in browser rendering and machine-tool output; download or inspect source evidence with an appropriate
bounded workflow.

## Update or installation fails

Check network access to GitHub Releases and the installed Go version. Use `interseptor update --check`
to separate update metadata problems from installation. A source build must use `CGO_ENABLED=0` and the
Go version documented in [Getting started]({{ "/getting-started/" | relative_url }}).

## Collecting a useful bug report

Include Interseptor version, OS/architecture, exact command flags with secrets removed, the smallest
generic reproduction, relevant log lines, and whether the problem occurs on loopback. Never attach
real captured traffic, API keys, session tokens, or target/customer data to a public issue.

