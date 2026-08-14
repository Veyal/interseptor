---
name: structured-upstream-settings
description: Preserve upstream proxy URL compatibility while presenting safe, readable structured Settings controls.
---

# Structured upstream proxy settings

When the API persists one proxy URL but the UI exposes separate controls:

1. Keep the stored/API representation as a standard URL for backward compatibility.
2. Parse it into scheme, host, port, username, and password on load.
3. Use a scheme selector with explicit Direct, HTTP, HTTPS, SOCKS5, and SOCKS5H choices.
4. Explain DNS ownership: SOCKS5 resolves locally; SOCKS5H resolves at the proxy.
5. Build the URL with the browser `URL` API so credentials are encoded safely; never concatenate userinfo.
6. Validate required host and the numeric port before saving.
7. Keep HTTPS-proxy CA trust visibly separate from origin TLS verification.
8. Show a readable connection summary and stack every field/action at narrow widths.
9. Cover the structured controls, URL parse/build path, protocol choices, and responsive CSS in journey tests.
