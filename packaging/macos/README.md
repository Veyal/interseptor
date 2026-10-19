# macOS app bundle

Builds `Interseptor.app`, a native macOS app (Swift + AppKit + WKWebView) around
the existing static Go binary. The proxy and the web UI are unchanged. The bundle
supplies a real Mac window, menu bar, Dock tile, menu-bar status item and
notifications.

## Build

```bash
make macos-app                   # → dist/macos/Interseptor.app (ad-hoc signed)
MAKE_DMG=1 make macos-app        # also produce a DMG
```

The build must run on macOS with the Xcode command-line tools (`xcrun swiftc`, `lipo`,
`codesign`, `iconutil`). Every executable is universal (x86_64 + arm64), and the
shell targets macOS 12+. The shell compiles with plain `swiftc`, so it needs no
Xcode project and no third-party dependencies. The server binary is stamped with
the same numeric version written to `Info.plist`. Tagged releases verify that
`interseptor version` reports the exact tag before they publish the bundle.

An explicit numeric `VERSION` override takes precedence. Otherwise the build
uses `git describe --tags --dirty`, so normal tagged and dirty describes remain
supported. When no tag is reachable, it falls back to the compiled-in
`internal/version.Version` value. This keeps a decimal-only abbreviated commit
SHA from being mistaken for `CFBundleShortVersionString`.

## What's inside

```
Interseptor.app/Contents/
  Info.plist
  MacOS/InterseptorApp       ← CFBundleExecutable: the native shell (packaging/macos/shell)
  MacOS/interseptor-macapp   ← Go supervisor (cmd/interseptor-macapp), run as `--shell`
  MacOS/interseptor          ← the real server binary
  Resources/AppIcon.icns     ← from packaging/macos/AppIcon.png
```

The shell executable is named `InterseptorApp`, not `Interseptor`, because macOS volumes
are normally case-insensitive and that name would overwrite the `interseptor`
server binary. The menu bar, the Dock and Activity Monitor all show `CFBundleName`
("Interseptor").

**Division of labour.** The shell owns the UI and the supervisor owns the server.
The shell runs `interseptor-macapp --shell` with pipes. The supervisor resolves
the sibling `interseptor`, starts it with `INTERSEPTOR_NO_BROWSER=1`, reads the
URL the server actually bound, waits until it answers, and prints exactly one
line to stdout: `INTERSEPTOR_READY <url>`. If anything fails it prints
`INTERSEPTOR_ERROR <message>`. It then supervises until SIGTERM/SIGINT or until
its stdin reaches EOF, which means the shell died, and stops the server
gracefully (SIGTERM, then SIGKILL after 6s). If an Interseptor server is already
running on the control address, the supervisor reports that URL. It never stops
that server, because it did not start it. Run without `--shell`,
`interseptor-macapp` keeps its old behaviour of opening an app window in a
browser.

**Behaviour.**

- One window, opened at once with a dark native loading state ("Starting proxy…"),
  so there is no white flash. The title bar is unified with the UI's title toolbar
  (one row, Xcode style): the traffic lights sit in the toolbar, the toolbar's empty
  areas drag the window, and a double-click there zooms or minimises it as set in
  System Settings. The window title stays current for Mission Control and the
  Window menu, and its colour follows the UI theme. The window frame persists between launches, and full screen is
  supported.
- Only the control origin loads inside the web view. External links,
  `target=_blank` and `window.open` go to the default browser. Attachments,
  non-displayable responses and `blob:`/`data:` links save through a native save
  panel. `<input type=file>` uses a native open panel, and JS
  `alert`/`confirm`/`prompt` appear as sheets.
- Closing the window leaves the proxy running. A Dock click or the status item
  reopens the window. ⌘Q asks for confirmation when requests are held in
  Intercept, stops the server, and then quits. If the server dies, the window
  shows an error with **Show Log** and **Restart** buttons.
- The menu-bar status item shows the held-request count, the intercept toggles
  and the project name with its live state. A notification is posted when a
  request is held while the app is in the background.
- **File → Connect to Server…** loads a remote control URL (for example a
  Tailscale `http://100.x.y.z:9966`) instead of starting a local proxy.
  **Use Local Proxy** switches back.
- The page receives `desktop-app desktop-macos` classes and a
  `window.interseptorDesktop` bridge (`post`, plus `run(command)` provided by
  the UI). Its user agent ends in `InterseptorDesktop/<version>`. Menu commands
  are sent to the UI as `panel:<id>`, `intercept:toggle-request`, `palette:open`
  and similar commands. The UI reports `ready`, `theme` and `status` messages back,
  plus `drag-regions` (where a press drags the window) and `file-accept` (the
  accept list of the file input about to open, which the open panel then honours).

**Per-machine settings.** A Finder-launched app inherits launchd's
environment, not a shell's, so the shell reads overrides from its defaults domain
and passes them to the supervisor as environment variables:

| `defaults write com.veyal.interseptor …` | Effect |
|---|---|
| `ControlAddr 127.0.0.1:19996` | `INTERSEPTOR_CONTROL_ADDR` |
| `ProxyAddr 127.0.0.1:18116` | `INTERSEPTOR_PROXY_ADDR` |
| `DataDir /path/to/dir` | `INTERSEPTOR_DATA_DIR` |
| `Project name` | `INTERSEPTOR_PROJECT` |
| `UIPath /desktop/` | UI path loaded on the control server (default `/`) |
| `RemoteURL http://host:9966` | set by Connect to Server… |

Use these settings to run a test copy beside a live instance. For example, set
`ControlAddr`, `ProxyAddr` and a throwaway `DataDir`, then remove them afterwards
with `defaults delete com.veyal.interseptor`.

All output goes to `~/Library/Logs/Interseptor.log` (**Help → Show Log**).

## Shell tests

`packaging/macos/shell-tests/run.sh` compiles `shell/Supervisor.swift` with plain `swiftc` and runs
its state-machine tests (clean stop, crash report, SIGTERM-ignoring child forced down by SIGKILL,
restart after a forced stop, stale exits). `go test ./cmd/interseptor-macapp` runs it on macOS.
The View menu panel order and shortcuts are checked against the web rail by the same package.

## Signing and notarization

Unsigned bundles work fine locally but Gatekeeper quarantines them once they have
been downloaded. For distribution you need an Apple Developer Program membership
($99/year) and a Developer ID Application certificate.

One-time notarytool credential setup:

```bash
xcrun notarytool store-credentials interseptor-notary \
  --apple-id "you@example.com" --team-id TEAMID --password "app-specific-password"
```

Then:

```bash
export CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)"
export NOTARY_PROFILE=interseptor-notary
make macos-app
```

The script signs the inner Mach-O files first and the bundle last, with
`--options runtime` (hardened runtime, required for notarization), then submits,
waits, and staples the ticket so Gatekeeper can verify offline. When
`CODESIGN_IDENTITY` is unset, the script ad-hoc signs the bundle (`codesign -s -`)
instead. That gives it the stable code identity that notifications and WebKit
expect, but Gatekeeper still quarantines a downloaded copy.

## Icon

`packaging/macos/AppIcon.png` (1024×1024) is rendered from the UI's favicon
mark: the green octagon with arrows, centred on a dark rounded square that
follows Apple's icon grid. Regenerate it with:

```bash
xcrun swift packaging/macos/render-icon.swift packaging/macos/AppIcon.png
```

The build expands it into every required size with `sips` and `iconutil`. A
hand-built `packaging/macos/AppIcon.iconset` takes precedence when present. The
menu-bar glyph is drawn in code (`shell/StatusGlyph.swift`) as a template image.

## Known trade-offs

**WebKit, not Chromium.** The UI runs in the system WKWebView (Safari's engine)
rather than a Chromium app window, so behaviour follows the installed macOS
Safari version. Clipboard writes rely on the page's own copy code. Loopback is a
secure context, so `navigator.clipboard` works locally. A plain-`http` remote
server is not a secure context and falls back to whatever the UI does without
that API.

**Bridge features need the UI side.** Menu commands, intercept checkmarks, the
status-item count and held-request notifications depend on the web UI
implementing the `interseptorDesktop` bridge. Until it does, menu commands
are queued and do nothing. Standard editing, reload, window, help and quit work
regardless.

**Self-update is blocked inside the bundle.** `interseptor update` overwrites its
own executable, which would invalidate the bundle's code signature and leave the
launcher and `Info.plist` on the old version. The bundled binary detects that it
is the main executable of a `.app` and refuses, pointing at whole-bundle
replacement instead. `interseptor update --check` still works, since it only
reads release metadata. `INTERSEPTOR_ALLOW_BUNDLE_UPDATE=1` overrides the refusal
and is safe only for an unsigned local build. Sparkle-style in-place bundle
updates are still unimplemented; updating means dropping in a new `.app`.

**Local access is unauthenticated by default.** The control UI auto-trusts
loopback connections carrying no API key. That is reasonable for a CLI you
started yourself, but weaker for an app that sits running in the background.
Set `INTERSEPTOR_REQUIRE_LOCAL_AUTH=1` to require a key from local clients too:

```bash
INTERSEPTOR_REQUIRE_LOCAL_AUTH=1 /Applications/Interseptor.app/Contents/MacOS/interseptor
```

Enforcement only arms itself once at least one API key exists, so turning it on
with a fresh profile cannot lock you out of the UI you need in order to mint the
first key. The HTTP `/mcp` front end carries a per-process internal token for its control-plane calls; that token
is never persisted and is rejected on any non-loopback connection.

The cost is that **external** local clients must then authenticate: `curl` needs
`Authorization: Bearer <key>`, and the separate `interseptor mcp` stdio process
does not currently pass a key, so it will be refused. Leave the flag off if you
depend on that subcommand.

While the flag is on, the **IP allowlist** (Settings → API → Allowlist) stops
granting access. It is a keyless trust grant, which is the very thing this mode
removes — and an entry covering loopback would otherwise disable the hardening
completely and silently. Allowlisted remote clients need an API key too.

The bundle does not set this flag for you — silently breaking MCP for every
desktop user would be worse than the default. To make it stick for the app, add
it to the bundle's `Info.plist` under `LSEnvironment`, or launch from a shell.

**CI builds the bundle but cannot sign it.** `ci.yml` has a `macos-app` job on
`macos-latest` that runs the darwin test suite, builds the bundle, validates the
plist and universal binaries, and uploads it as an artifact. Tagged builds use
the same validation in `release.yml`, then publish an unsigned app ZIP and DMG
only after the cross-platform GoReleaser candidate also passes. The assets stay
unsigned because signing needs a Developer ID certificate and notary
credentials that CI does not have; publishing signed assets requires adding
those credentials to repository secrets and the release workflow. Tagged
releases also publish `macos-checksums.txt` for the ZIP and DMG; the separate
`checksums.txt` covers the GoReleaser CLI archives.

**Homebrew cask needs notarization.** Homebrew is phasing out unsigned casks, so
cask distribution is not viable without the signing steps above.
