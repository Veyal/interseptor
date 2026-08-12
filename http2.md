---
layout: default
title: HTTP/2
classification: current
source: docs/http2.md
---
<p class="eyebrow">CURRENT</p>
# HTTP/2 and MITM

## Current behavior

| Leg | Protocol |
|-----|----------|
| Client → proxy (CONNECT + MITM TLS) | **HTTP/2 when ALPN negotiates `h2`**, else HTTP/1.1 |
| Proxy → origin (via `http.Transport`) | **HTTP/2 when ALPN negotiates `h2`**, else HTTP/1.1 |

### Client → proxy leg

The forged leaf offers ALPN `h2, http/1.1` (`handleConnect`). When the client
selects `h2`, the MITM connection is handed to an `http2.Server`
(`serveH2MITM`, `internal/proxy/http2.go`) whose handler bridges every decoded
stream into the **same** forwarding pipeline the HTTP/1.1 loop uses —
`gateAndForward` → `writeResponseHTTP`. Intercept holds, match-&-replace,
scope, capture, and history all apply identically; only the client-facing
framing differs. When the client stays on HTTP/1.1, the request falls through
to the `http.ReadRequest` loop unchanged.

History records the **client** leg proto for h2 sessions (`HTTPVersion:
HTTP/2.0`), captured by `buildFlow` before the forwarded request is normalized
to HTTP/1.1 for `http.Transport` (which does its own upstream ALPN and rejects
`ProtoMajor == 2` requests).

### Proxy → origin leg

Upstream HTTP/2 is enabled with `ForceAttemptHTTP2` and TLS
`NextProtos: h2, http/1.1`. Forwarding never depends on h2 being available —
ALPN/h1 fallback is automatic on both legs.

### HTTP/1.1 client, HTTP/2 origin

A client that stays on HTTP/1.1 while the origin speaks h2 still works: the
proxy **downgrades framing to HTTP/1.1 + chunked** before writing back over the
h1 MITM socket (`writeResponseConn`), so browsers never hang on an `HTTP/2.0`
status line. `HTTPVersion` in that case records the upstream proto.

## Ops notes

- TLS-bypass (pinning) tunnels CONNECT raw — origin may speak h2 end-to-end;
  Interseptor does not decrypt those sessions.
- WebSocket upgrades remain a separate path (HTTP/1.1 `Upgrade`). h2 has no
  HTTP/1.1-style Upgrade, so the WebSocket relay only runs on the h1 leg.
- h2-only APIs that refused to work through the old h1-only MITM leg now
  capture correctly end-to-end.

