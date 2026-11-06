---
id: security-headers
title: Security headers, CORS and cookie policy on authenticated responses
applies_to:
  - mime:text/html
  - mime:application/json
severity_hint: Low
tools:
  - get_flow
  - list_flows
  - send_request
  - create_finding
evidence: >-
  The response headers of an authenticated, sensitive endpoint and of the
  browser-facing page, with what is missing said explicitly.
references:
  - OWASP Secure Headers Project
  - CWE-693
---

# Security headers and cookie policy

A missing header is an observation, not a finding, unless you say what it lets
an attacker do on *this* application. Rate it Low and describe the concrete
consequence; do not file seven separate findings for seven headers.

## Check, on the response that matters

- `Content-Security-Policy` — absent or `unsafe-inline`: reflected XSS in the
  same app becomes easier to exploit. Tie it to an XSS finding when you have one.
- `Strict-Transport-Security` on the apex and every host that sets cookies.
- `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `frame-ancestors` (or
  `X-Frame-Options`) for pages that perform state changes: clickjacking.
- Cookies: `Secure`, `HttpOnly`, `SameSite`. A session cookie without `HttpOnly`
  is only interesting when an XSS sink exists; say which.
- CORS: `Access-Control-Allow-Origin` reflecting the request `Origin` **with**
  `Access-Control-Allow-Credentials: true` is a real finding — demonstrate it
  with a request carrying an attacker origin and a cookie, and show the
  `Access-Control-Allow-Origin` echoed back. A wildcard without credentials is
  usually not.

## Evidence

Attach the response flow and quote the header block. For CORS, attach the
cross-origin request whose response echoes the attacker origin with credentials,
and state what the attacker reads.

## Note

Bundled browsers enforce most of this for you; the finding is about the users
whose client or version does not, or about the one directive the app relies on.
Keep it one finding per root cause with the endpoints as targets.
