# Proxy, TLS, and networking

This guide explains the listener, optional proxy authentication, HTTPS interception, origin
certificate verification, TLS passthrough, and chained upstream proxies.

## Listener model

Interseptor starts an HTTP/HTTPS forward proxy on `127.0.0.1:8080` and a control UI/API on
`127.0.0.1:9966`. Configure one client to use the proxy for both HTTP and HTTPS. For a phone or
another machine, add a non-loopback listener such as `0.0.0.0:8080` in **Settings → Network → Proxy
listener**, then use the workstation's LAN address as the device proxy.

`INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` disables all non-loopback listener changes. This is useful on a
workstation that must never accept LAN traffic.

## System proxy automation

**Settings → Proxy & network** and the first-run **Setup** wizard can point the operating system's
HTTP/HTTPS proxy at Interseptor and turn it back off again. It only ever acts on an explicit request
— Interseptor never changes your system proxy on its own.

| OS | Mechanism | Notes |
| --- | --- | --- |
| **macOS** | `networksetup` web + secure-web proxy on every active network service | Unchanged behaviour. |
| **Windows** | The per-user WinINet registry values `ProxyServer` and `ProxyEnable` under `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, then a WinINet refresh so running apps pick up the change | The operator's previous proxy is saved and restored when you turn it off. |
| **Linux (GNOME)** | `gsettings org.gnome.system.proxy` manual HTTP/HTTPS mode | The previous proxy mode is restored on disable. |

On a non-GNOME Linux desktop (KDE, a headless host, or anything without `gsettings`), automatic
configuration reports as unsupported and the control is hidden; set the proxy manually, or export
`http_proxy` and `https_proxy=http://127.0.0.1:8080` for CLI tools. Automatic configuration changes
only the HTTP/HTTPS proxy, never the bypass list. The Windows and Linux paths are built and
unit-tested on every platform but are exercised end to end only on their own OS.

## Test browser

**Launch test browser** in the first-run **Setup** wizard starts your installed Chrome/Chromium in an
isolated profile already pointed at the proxy — the same idea as Burp's embedded browser. Personal
history, cookies, and extensions stay in your real profile and never pollute captured traffic. The
browser is discovered exactly as the PDF report export discovers it (`INTERSEPTOR_CHROME`, then
`PATH`, then the usual install locations); with none installed the action reports an honest error
naming what it looked for. The test browser runs with `--ignore-certificate-errors` so HTTPS targets
load through the MITM without the throwaway profile having to trust the Interseptor CA — the proxy
itself can still verify the real origin (Origin certificate verification, below).

## Proxy listener exposure

Proxy listeners do not require authentication by default — not on loopback, and not on a LAN or
tailnet address. API keys protect History and the control API; they are not a proxy password.

Optional listener basic auth lives in **Settings → Proxy & network → Proxy authentication**. Turn
it on and set a username and password of your own, then point the client at the proxy with those
credentials (`Proxy-Authorization: Basic`). Leave it off, the default, and any client that can
reach the listener can use it. `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` still refuses non-loopback binds.

A non-loopback listener with authentication off is an open proxy for everyone who can reach it:
bind only the interfaces the engagement needs, prefer a tailnet or LAN you control, and close the
listener after capture. Interseptor removes `Proxy-Authorization` before forwarding, so the
listener password is never sent to the target origin.

## Browser background traffic suppression

**Settings → Proxy & network → Browser background traffic** is enabled by default. It forwards
known Firefox and Chromium background requests unchanged while excluding them from History, live
flow and frame events, request and response interception, match-and-replace processing,
content-addressed body storage, and WebSocket frame capture. It is capture-noise suppression, not a
firewall: the browser can still reach those services.

The curated list covers dedicated telemetry, crash, update, Remote Settings, Safe Browsing,
connectivity, Firefox Suggest, sponsored new-tab, and related browser-managed endpoints. Some of
these services are functional browser services rather than telemetry; the UI deliberately calls the
category **background traffic**. The API setting retains its existing
`suppressBrowserTelemetry` name for compatibility.

Suppression applies when a new request enters the proxy. Existing History rows are not deleted or
hidden when the setting is enabled. Disable the setting when those browser services are themselves
part of the authorized target.

Matching stays exact and conservative. Interseptor does not suppress broad domains such as
`*.mozilla.org`, `*.googleapis.com`, Mozilla Accounts, Firefox Sync, add-on services, downloads, or
ordinary website/application hosts. Current Firefox endpoints are reviewed against Mozilla's
[enterprise domain inventory](https://support.mozilla.org/kb/domains-allow-firefox) and
[source defaults](https://searchfox.org/mozilla-central/source/modules/libpref/init/all.js).

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
origin certificates** by choosing **Strict** in **Settings → TLS / CA → Origin connection security**
when authenticity matters. With verification on,
an expired, mismatched, self-signed, or untrusted origin certificate produces an upstream TLS error
instead of a captured response.

Verification exceptions keep traffic intercepted but accept any origin certificate for matching
hosts. Use them narrowly for known test systems. This is different from passthrough and weakens server
authentication for those hosts.

If a target is opened by IP and the certificate contains only DNS names, strict mode reports an error
such as `x509: cannot validate certificate for 192.0.2.25 because it doesn't contain any IP SANs`.
Prefer the hostname named by the certificate and map it to the IP with test DNS or a hosts file. When
that is impossible for an authorized test target:

1. Select the failed request in **History**.
2. Open **Settings → TLS / CA → Origin TLS verification exceptions**.
3. Choose **Add selected History host**. You can also enter the IP manually and choose **Add
   exception**.

Installing another CA does not repair a hostname/IP mismatch. An exception deliberately disables
origin identity verification only for the listed host or IP.

## TLS passthrough and pinning

TLS passthrough tunnels selected hosts without decryption. Use it when a pinned client must continue
working and capture is not required for that host. Passthrough rows contain connection metadata, not
HTTP paths, headers, or bodies.

Interseptor does not bypass certificate pinning. A `CONNECT` followed by a failed client handshake is
usually an untrusted CA or pinning. Options include installing the CA correctly, using a test build,
an emulator/rooted device with a system CA, or an authorized runtime instrumentation workflow. Do not
add a host to passthrough if you still need HTTP evidence from it.

## Upstream proxies

Configure a second-hop proxy in **Settings → Proxy & network → Upstream proxy**. Select the connection
type, then enter its host and port. The UI builds and validates the proxy URL; credentials stay in
separate fields so percent-encoding and special characters are handled safely.

The saved route applies consistently to captured browser/device traffic, Repeater, Intruder, login
macros, and custom-check requests. A Repeater `502` should therefore be diagnosed against
the same upstream endpoint, credentials, DNS mode, and HTTPS-proxy CA shown in Settings.

| Type | Choose it when | DNS behavior | Typical port |
| --- | --- | --- | --- |
| **Direct** | Interseptor should connect to targets itself | Local | — |
| **HTTP** | Chaining through a corporate proxy, Burp, or another HTTP CONNECT proxy | Proxy protocol carries target hostnames | `80` or `8080` |
| **HTTPS** | The connection from Interseptor to the upstream proxy must itself use TLS | Proxy protocol carries target hostnames | `443` |
| **SOCKS5** | A SOCKS service is available and target DNS should resolve on the Interseptor workstation | Local DNS; the resolved IP is sent to SOCKS | `1080` |
| **SOCKS5H** | Target names resolve only from the proxy network, or local DNS leakage must be avoided | Remote DNS at the SOCKS proxy | `1080` |

### HTTP or HTTPS upstream

1. Select **HTTP** or **HTTPS**.
2. Enter the proxy host and port, for example `proxy.example.com` and `8080`.
3. Add username and password only if the proxy requires authentication.
4. For an HTTPS proxy signed by a private CA, open **Advanced trust settings** and paste the CA PEM.
5. Choose **Save upstream proxy** and confirm the summary shows the expected endpoint.

HTTPS upstream-proxy certificates are always verified. The origin compatibility mode and origin
exceptions never weaken this separate TLS connection.

### SOCKS5 or SOCKS5H upstream

For an SSH dynamic forward, start a local SOCKS listener:

```bash
ssh -N -D 127.0.0.1:1080 operator@example.com
```

Then select **SOCKS5H**, use host `127.0.0.1`, port `1080`, and leave credentials blank unless the SOCKS
server requires them. Choose plain **SOCKS5** only when DNS should be resolved locally before the
connection is sent through SOCKS.

To disable chaining, select **Direct — no upstream proxy** and save. Stored proxy credentials are not
sent to target origins; Interseptor uses them only on the upstream-proxy hop.

If an upstream returns `407`, Interseptor reports an upstream authentication failure without passing
the upstream's authentication challenge to the browser. This avoids a misleading second credential
prompt.

## Upstream TLS fingerprint (ClientHello)

Interseptor dials origins with Go's standard `crypto/tls`, so its TLS ClientHello carries Go's
fingerprint. Some anti-bot WAFs (Cloudflare, Akamai) fingerprint the ClientHello (JA3/JA4) and may
challenge or block a connection that does not look like a mainstream browser.

Mimicking a Chrome or Firefox ClientHello needs a third-party TLS stack — such as
[uTLS](https://github.com/refraction-networking/utls) — which would be a **new production dependency**
and is **not bundled**. It is deferred deliberately: adding a dependency is a project decision, and
uTLS does not drop cleanly into the current upstream transport, which negotiates HTTP/2 by ALPN
through Go's `http.Transport`/`DialTLSContext`. A uTLS `UConn` shapes the handshake but hands back a
connection `http.Transport` will not speak HTTP/2 over, so the HTTP/2 leg would have to be wired
explicitly (an `http2.Transport` with a custom dial) rather than inherited — a real change to the
forwarding path, not a drop-in. If you need ClientHello mimicry today, chain Interseptor through an
upstream proxy that performs it (see [Upstream proxies](#upstream-proxies)). The full assessment is in
[issue #108](https://github.com/Veyal/interseptor/issues/108).

## Safe network checklist

- Bind only the interfaces needed for the engagement. With proxy authentication off, a non-loopback
  listener is an open proxy for everyone who can reach it.
- Keep the control UI loopback-only unless remote access is intentional and key-protected.
- Enable origin verification unless the target environment requires an explicit exception.
- Remove CA trust, listener exposure, saved credentials, and tunnels during close-out.

See [Mobile testing](mobile-testing.md), [Troubleshooting](troubleshooting.md), and the
[security model](architecture.md#security-model).
