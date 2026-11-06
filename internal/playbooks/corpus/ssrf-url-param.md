---
id: ssrf-url-params
title: Server-side request forgery through URL, webhook and image parameters
applies_to:
  - param:url
  - param:uri
  - param:callback
  - param:webhook
  - param:image
  - param:next
severity_hint: High
tools:
  - oob_new
  - send_request
  - get_flow
  - create_finding
evidence: >-
  A request whose parameter points at your OOB host, and the OOB interaction
  that proves the server fetched it.
references:
  - OWASP WSTG-INPV-19
  - CWE-918
---

# SSRF through URL parameters

Any parameter the server fetches from is a candidate: webhooks, avatars and
image imports, PDF renderers, link previews, feed imports, "fetch from URL"
imports, and URL-based redirects that server-side code follows.

## Do it

1. Generate a unique token with `oob_new` — a per-test host, so a hit is
   unambiguous.
2. Supply it where a URL is expected, in the shape the endpoint accepts
   (`https://<token>.oob…/probe`), and send.
3. Check `oob_state` for the interaction: a DNS lookup alone means the parser
   resolved the host; an HTTP request means the server fetched it.
4. Then test the boundary, in this order:
   - metadata: `http://169.254.169.254/latest/meta-data/` (cloud) — a fetch that
     returns cloud credentials is Critical;
   - loopback: `http://127.0.0.1:<control-port>/` — does the app fetch its own
     admin surface?
   - scheme: `file:///etc/passwd`, `gopher://`, `dict://` — many parsers allow
     more than http(s).
5. If redirects are followed, point at your OOB host which 302s to the metadata
   address: that defeats a scheme-and-host allowlist on the first hop.

## Evidence

Attach the triggering request and the OOB interaction record. For a metadata
read, attach the response body that contains the credentials path or token
(redacted). Never leave a fired callback unrecorded: an SSRF finding without the
interaction is a hypothesis.

## Do not

Do not probe internal ranges broadly. One loopback attempt and one metadata
attempt, on a host you are authorised to test, is the finding.
