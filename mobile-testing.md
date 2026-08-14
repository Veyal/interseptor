---
layout: default
title: Mobile testing
classification: current
source: docs/mobile-testing.md
---
<p class="eyebrow">CURRENT</p>
# Mobile testing

## Choose a connection mode

- **Android over USB:** Interseptor uses `adb reverse`; the device proxy points to loopback and no LAN
  listener is required.
- **Android or iOS over Wi-Fi:** bind the proxy to `0.0.0.0:8080` (or a specific LAN interface), put
  both devices on a reachable network, and configure the phone with the workstation's LAN address.
- **iOS Simulator:** use the built-in Simulator setup on macOS with Xcode tools installed.

Wi-Fi clients use a non-loopback listener and therefore need [proxy authentication]({{ "/proxy-and-tls/" | relative_url }}#proxy-authentication):
any username plus a full-scope Interseptor API key as the password.

## Android

Install `adb`, enable USB debugging, connect exactly one authorized device, then open **Settings →
Mobile devices**. **Setup all** selects a system CA for an emulator/rooted target and a user CA for a
normal phone, then configures the chosen proxy mode. Use **Clear proxy** during close-out.

Android 7+ applications do not automatically trust user-installed CAs. A user CA may work in the
browser while an app still rejects it. For an authorized test, use a debuggable build with a network
security configuration, an emulator/rooted device with a system CA, or an approved instrumentation
method. Interseptor does not defeat pinning itself.

## iOS

For Simulator, select the target and use **Setup all** or install the CA and open the profile
separately. For a physical device, download the `.mobileconfig` profile over a trusted path and install
it. Then enable full trust under **Settings → General → About → Certificate Trust Settings**.

The profile can configure the global HTTP proxy but cannot bypass application pinning. Jailbroken SSH
automation sends credentials only for the setup request and does not persist them in project settings.

## Verification

1. Open an HTTP test URL and confirm a History row.
2. Open an HTTPS URL and confirm a decrypted path rather than only a `CONNECT` row.
3. Check the device proxy address, CA trust, and client time if HTTPS fails.
4. If browsers work but one app fails, treat pinning or app-specific trust as the leading hypothesis.
5. If no traffic arrives, verify Wi-Fi reachability, host firewall, listener bind, and whether the app
   ignores the operating-system proxy.

Use the TLS diagnostic in **Settings → TLS / CA** to distinguish no traffic, no HTTPS, and a failed
client TLS handshake. See [Troubleshooting]({{ "/troubleshooting/" | relative_url }}) for the complete decision tree.

## Cleanup

Clear device proxies, remove profiles and CA trust, remove any temporary system CA or instrumentation,
revoke the device API key, and restore the proxy listener to loopback.

