# Proxy, TLS, and networking

This guide explains the listener, the browser authentication prompt, HTTPS interception, origin
certificate verification, TLS passthrough, and chained upstream proxies.

## Listener model

Interseptor starts an HTTP/HTTPS forward proxy on `127.0.0.1:8080` and a control UI/API on
`127.0.0.1:9966`. Configure one client to use the proxy for both HTTP and HTTPS. For a phone or
another machine, add a non-loopback listener such as `0.0.0.0:8080` in **Settings → Network → Proxy
listener**, then use the workstation's LAN address as the device proxy.

`INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` disables all non-loopback listener changes. This is useful on a
workstation that must never accept LAN traffic.

## Proxy authentication

A loopback proxy listener does not request credentials. Every non-loopback proxy listener requires
proxy authentication, even when the client happens to connect from the same workstation. This
protects captured traffic and prevents an accidentally exposed listener from becoming an open proxy.

Create a **full-scope** API key in **Settings → API & MCP → API keys**. Configure the client with:

- username: any non-empty label, such as `interseptor`;
- password: the full-scope API key.

A read-only key is rejected because proxying creates captured flows and can send traffic. Interseptor
removes `Proxy-Authorization` before forwarding, so the listener credential is never sent to the
target origin.

## Browser prompt

The dialog saying that `moz-proxy://…` requests a username and password is Firefox's normal response
to HTTP `407 Proxy Authentication Required`. The realm text is **interseptor**. It is the proxy asking,
not the destination website and not a compromised page.

If you do not need LAN/device capture, bind the proxy to `127.0.0.1`; the prompt disappears. If you do
need it, enter any username and a full-scope Interseptor API key as the password. Repeated prompts mean
the key is absent, expired, revoked, or read-only. Do not enter the target application's credentials.

Command-line clients can authenticate explicitly:

```bash
curl --proxy http://192.0.2.10:8080 \
  --proxy-user 'interseptor:FULL_SCOPE_API_KEY' \
  https://example.com/
```

Avoid putting a real key in shell history on an engagement; use your client's protected credential
store or an environment-specific secret mechanism.

## HTTPS interception

For HTTPS, the client first creates a `CONNECT` tunnel. Interseptor presents a per-host certificate
signed by its local CA, decrypts the request, forwards it to the origin, and records the exchange.

1. Download the CA from **Settings → TLS / CA**, or `http://127.0.0.1:9966/api/ca.crt`.
2. Install it in the trust store used by the test client.
3. Confirm an HTTPS flow appears in History with a visible path and response status.

Installing the CA authorizes Interseptor to impersonate HTTPS sites on that client. Remove it after
the engagement when it is no longer required.

## Origin certificate verification

Client trust and origin trust are separate TLS legs. The client trusts Interseptor's CA; Interseptor
may also verify the real origin certificate.

Origin verification is off by default for compatibility with test environments. Enable **Verify
origin TLS certificates** in **Settings → TLS / CA** when authenticity matters. With verification on,
an expired, mismatched, self-signed, or untrusted origin certificate produces an upstream TLS error
instead of a captured response.

Verification exceptions keep traffic intercepted but accept any origin certificate for matching
hosts. Use them narrowly for known test systems. This is different from passthrough and weakens server
authentication for those hosts.

## TLS passthrough and pinning

TLS passthrough tunnels selected hosts without decryption. Use it when a pinned client must continue
working and capture is not required for that host. Passthrough rows contain connection metadata, not
HTTP paths, headers, or bodies.

Interseptor does not bypass certificate pinning. A `CONNECT` followed by a failed client handshake is
usually an untrusted CA or pinning. Options include installing the CA correctly, using a test build,
an emulator/rooted device with a system CA, or an authorized runtime instrumentation workflow. Do not
add a host to passthrough if you still need HTTP evidence from it.

## Upstream proxies

Configure an HTTP, HTTPS, `socks5`, or `socks5h` upstream in **Settings → Network → Control UI and
upstream proxy**. Credentials are optional. For a private HTTPS upstream proxy, add its CA PEM in the
dedicated upstream CA field; upstream-proxy TLS verification stays strict and is independent from
origin verification.

If an upstream returns `407`, Interseptor reports an upstream authentication failure without passing
the upstream's authentication challenge to the browser. This avoids a misleading second credential
prompt.

## Safe network checklist

- Bind only the interfaces needed for the engagement.
- Use a scoped, expiring full key for non-loopback proxy clients.
- Keep the control UI loopback-only unless remote access is intentional and key-protected.
- Enable origin verification unless the target environment requires an explicit exception.
- Remove CA trust, listener exposure, saved credentials, and tunnels during close-out.

See [Mobile testing](mobile-testing.md), [Troubleshooting](troubleshooting.md), and the
[security model](architecture.md#security-model).
