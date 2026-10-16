---
name: sender-options-guard
description: Conventions for sender.SendOptions, RawHeaders and the dial-time IPGuard so collection/runner sends stay safe and Repeater defaults stay unchanged.
---

# Sender options and IP guard

- `Request.Options == nil` must remain byte-for-byte the old Repeater path (no redirects, no timeout, no TLS verify, global upstream). New behaviour lives in `internal/sender/options*.go`; hooks in `sender.go` are additive.
- Guard at dial time, on the resolved IP, and dial that IP (`guardedDial`). Checking a hostname beforehand permits DNS rebinding. Keep TLS ServerName from the original host, not the pinned IP.
- Own-listener refusal (`OwnPorts`/`OwnIPs`) wins over `AllowHosts`; allow-listing is by exact host only.
- Redirects are followed in `Send` (each hop a flow, guard re-applied at dial); strip Authorization/Cookie cross-host.
- Through an HTTP(S) upstream proxy the dial hop is the proxy, so the guard pre-resolves the target in `Transport.Proxy` (best effort, racy). RawHeaders is refused there.
- RawHeaders skips session headers, token macro and 401 re-auth: the caller owns the wire request. The stored flow keeps duplicates but a map cannot keep order.
- Test with `httptest` (loopback) plus `DNSOverride` to model rebinding; use example.com names only.
