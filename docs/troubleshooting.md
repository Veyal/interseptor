# Troubleshooting

Start with the first symptom that matches. Keep a terminal open for application logs and use a generic
test target before debugging a complex application.

## Browser asks for a proxy username and password

This is expected on a non-loopback proxy listener. The realm is `interseptor`. Enter any username and
a full-scope API key as the password. A read key, expired key, or target-site password produces another
prompt. Bind to `127.0.0.1` if the listener is local-only and authentication is unnecessary. See
[Proxy authentication](proxy-and-tls.md#proxy-authentication).

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

A browser-visible `407` from Interseptor means listener credentials are missing or invalid. An
Interseptor toast/log saying “upstream proxy authentication required” means the configured chained
proxy rejected its credentials. These are different authentication layers.

For upstream setup, select a mode instead of typing a URL: **HTTP/HTTPS** for an HTTP CONNECT proxy,
**SOCKS5** for local DNS, or **SOCKS5H** for proxy-side DNS. Verify host and port in the status summary.
Private CA PEM applies only to HTTPS upstream proxies.

## Send to Repeater does not look right

Use the action on the attached PoC flow, wait for “loaded #… into Repeater,” and verify method, complete
URL, headers, and body before sending. Interseptor reuses a tab for the same host and path; query values
remain in the loaded request. A deleted/missing evidence flow cannot be sent and must be recaptured.

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
Go version documented in [Getting started](getting-started.md).

## Collecting a useful bug report

Include Interseptor version, OS/architecture, exact command flags with secrets removed, the smallest
generic reproduction, relevant log lines, and whether the problem occurs on loopback. Never attach
real captured traffic, API keys, session tokens, or target/customer data to a public issue.
