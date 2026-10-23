# Mobile testing

## Choose a connection mode

- **Android over USB:** Interseptor uses `adb reverse`; the device proxy points to loopback and no LAN
  listener is required.
- **Android or iOS over Wi-Fi:** bind the proxy to `0.0.0.0:8080` (or a specific LAN interface), put
  both devices on a reachable network, and configure the phone with the workstation's LAN address.
- **iOS Simulator:** use the built-in Simulator setup on macOS with Xcode tools installed.

Wi-Fi clients use a non-loopback listener. Proxy authentication is off unless you set a username
and password in Settings; see [Proxy listener exposure](proxy-and-tls.md#proxy-listener-exposure).

## Android

Install `adb`, enable USB debugging, connect exactly one authorized device, then open **Settings →
Mobile devices**. **Setup all** selects a system CA for an emulator/rooted target and a user CA for a
normal phone, then configures the chosen proxy mode. Use **Clear proxy** during close-out.

Android 7+ applications do not automatically trust user-installed CAs. A user CA may work in the
browser while an app still rejects it. For an authorized test, use a debuggable build with a network
security configuration, an emulator/rooted device with a system CA, or an approved instrumentation
method. Interseptor does not defeat pinning itself.

### Android 14+ and the read-only system trust store

`adb remount` can no longer write `/system/etc/security/cacerts/` on Android 14+ and many Google APIs
emulator images, because the Conscrypt trust store moved into a read-only APEX
(`/apex/com.android.conscrypt/cacerts`). Two rooted options install a system CA without a writable
`/system`:

- **Rebootless tmpfs overlay** (CA mode `system-tmpfs`): Interseptor roots `adbd`, mirrors the
  existing system certs plus its own onto a `tmpfs` mounted over the system trust store, then repeats
  the overlay inside each zygote mount namespace so the APEX copy is covered too. It takes effect
  immediately and lasts until the next reboot.
- **Magisk / KernelSU module** (`GET /api/android/magisk-module.zip`): a flashable module that
  reinstalls the CA as a system CA and reapplies the APEX overlay on every boot, for a persistent
  rooted trust. Flash it in Magisk/KernelSU Manager and reboot.

Both require root — a rooted phone or an emulator image with root. The classic `adb remount`
system-CA path still works on older or userdebug images and is unchanged. The tmpfs command
construction and the generated module are unit-tested inside Interseptor, but the on-device steps
must be verified on your real Android 14+ hardware; neither path is a pinning bypass — an app that
pins a specific certificate still rejects the CA.

## iOS

For Simulator, select the target and use **Setup all** or install the CA and open the profile
separately. For a physical device, download the `.mobileconfig` profile over a trusted path and install
it. Fetch the profile or CA before pointing the device at the proxy (or with the device proxy off):
the proxy refuses to forward to Interseptor's own ports, including this computer's LAN address, so
a proxied device cannot reach the control API through it. Then enable full trust under **Settings → General → About → Certificate Trust Settings**.

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
client TLS handshake. See [Troubleshooting](troubleshooting.md) for the complete decision tree.

## Cleanup

Clear device proxies, remove profiles and CA trust, remove any temporary system CA or instrumentation,
revoke the device API key, and restore the proxy listener to loopback.
