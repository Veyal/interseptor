# HTTP/2 and MITM

## Current behavior

| Leg | Protocol |
|-----|----------|
| Client → proxy (CONNECT + MITM TLS) | **HTTP/1.1** (request line / response framing) |
| Proxy → origin (via `http.Transport`) | **HTTP/2 when ALPN negotiates `h2`**, else HTTP/1.1 |

Upstream HTTP/2 is enabled with `ForceAttemptHTTP2` and TLS `NextProtos: h2, http/1.1`.
If the origin responds over h2, History’s `HTTPVersion` records that upstream proto
(e.g. `HTTP/2.0`). Before writing back to the client, the proxy **downgrades framing to
HTTP/1.1 + chunked** so browsers never hang on an `HTTP/2.0` status line over an h1
MITM socket (see `writeResponseConn`).

Forwarding never depends on h2 being available — ALPN/h1 fallback is automatic.

## Not yet (issue #19 remainder)

True **client↔proxy HTTP/2 MITM** (ALPN `h2` on the forged leaf + `http2.Server` on the
MITM conn) is not shipped. Many clients still work over h1 after CONNECT; APIs that
require h2 end-to-end on the client leg need the remaining work in #19.

## Ops notes

- TLS-bypass (pinning) tunnels CONNECT raw — origin may speak h2 end-to-end; Interseptor
  does not decrypt those sessions.
- WebSocket upgrades remain a separate path (HTTP/1.1 Upgrade).
