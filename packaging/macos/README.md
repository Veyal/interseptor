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
gracefully (SIGTERM, then SIGKILL after 6s). The server gets the same kind of
lifeline: its stdin is a pipe the supervisor holds open, and
`INTERSEPTOR_EXIT_ON_STDIN_EOF=1` makes it shut down gracefully at EOF, so a
SIGKILLed supervisor cannot leave it orphaned. If an Interseptor server is already
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
- Only the control origin loads inside the web view. External `http`, `https`
  and `mailto` links, `target=_blank` and `window.open` go to the default
  browser or mail app; any other scheme is dropped rather than launching
  whatever app claims it. Attachments,
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
  Tailscale `http://100.x.y.z:9966` or a MagicDNS name) instead of starting a
  local proxy. Plain `http` is allowed for web content and the local network
  (`NSAllowsArbitraryLoadsInWebContent`, `NSAllowsLocalNetworking` in
  `Info.plist`). **Use Local Proxy** switches back.
- The page receives `desktop-app desktop-macos` classes and a
  `window.interseptorDesktop` bridge (`post`, plus `run(command)` provided by
  the UI). Its user agent ends in `InterseptorDesktop/<version>`. Menu commands
  are sent to the UI as `panel:<id>`, `intercept:toggle-request`, `palette:open`
  and similar commands. The UI reports `ready`, `theme` and `status` messages back,
  plus `drag-regions` (where a press drags the window), `file-accept` (the
  accept list of the file input about to open, which the open panel then honours)
  and `check-updates` (run a user-initiated update check).

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

## Updates

The shell updates itself from GitHub releases, natively and without Sparkle
(`shell/Updater.swift`, `UpdateWindowController.swift`, `UpdateVerifier.swift`,
`UpdateHelperScript.swift`, `Versioning.swift`).

- **Checks.** Ten seconds after launch and then every 24 hours (checked
  hourly, so a sleeping Mac catches up), the shell reads
  `api.github.com/repos/Veyal/interseptor/releases/latest`. Drafts,
  prereleases and tags with a prerelease suffix are ignored, and versions
  compare numerically (`2.10.0` > `2.9.0`). **Interseptor → Check for
  Updates…** and a click on the UI's version badge (bridge message
  `{type:'check-updates'}`) always report a result. **Automatically Check for
  Updates** turns the background checks off; `INTERSEPTOR_NO_UPDATE_CHECK=1`
  also disables them. **Skip This Version** silences only automatic checks.
  State lives in the defaults keys `UpdateLastCheck`, `UpdateSkippedVersion`,
  `UpdateAutomaticChecks` and `UpdateStagedVersion`.
- **Install.** The update window shows the release notes, then download
  progress, **Verifying…** and **Preparing…**. Files go to
  `~/Library/Caches/<bundle id>/Updates/<version>/`. A verified update is
  staged: **Later** keeps it, and the next check or launch offers **Install and
  Relaunch** again without a new download, as long as the staged bundle still
  validates. **Install and Relaunch** uses the normal quit path (the held-request
  prompt, then a graceful server stop). As the app exits, it spawns a detached
  `/bin/sh` helper in its own session. The helper waits up to 60s for the old
  process, renames the old bundle aside, moves the new one into place (falling
  back to `ditto` across volumes), restores the old bundle if that fails, and
  reopens the app. It logs to `~/Library/Logs/Interseptor-update.log`. Paths
  reach the helper only as arguments, never as script text.
- **What is verified before install.** Every URL (assets, redirects, the final
  response) must be HTTPS on `github.com`, `objects.githubusercontent.com` or
  `release-assets.githubusercontent.com`. `macos-checksums.txt` from the same
  release must list the exact ZIP name `Interseptor-<tag>-unsigned.zip`, and the
  ZIP's streamed SHA-256 must match it. `ditto -x -k` must unpack an
  `Interseptor.app` whose `CFBundleIdentifier` equals the running app's, whose
  `CFBundleShortVersionString` equals the release version, and which passes
  `codesign --verify --deep --strict`. Any failure refuses the install and
  offers **Download Manually…**.
- **Trust model.** Trust rests on HTTPS to GitHub plus the checksum file the
  release publishes. The bundle is ad-hoc signed, so there is no developer
  identity to pin. The signature check catches corruption and tampering after
  signing, not a malicious release. Anyone who can publish a release, or write
  to the user's Caches or Applications folder, can already replace the app.
  URLSession downloads carry no quarantine attribute, so the swapped app
  relaunches without a Gatekeeper prompt.
- **When it cannot update in place.** This is detected before any download.
  The window explains the cause and offers **Download Manually…** instead of
  **Install Update** when the app runs from App Translocation (a quarantined
  copy opened outside Applications), from a read-only volume such as the
  mounted DMG, or from a folder the user cannot write to. Moving Interseptor
  into `/Applications` fixes the first two.

## Shell tests

`packaging/macos/shell-tests/run.sh` compiles the Foundation-only shell files
(`Supervisor.swift`, `ExternalLinks.swift`, `DownloadStaging.swift`,
`ReloadBudget.swift`, `Versioning.swift`, `UpdateVerifier.swift`,
`UpdateHelperScript.swift`) with plain `swiftc` and runs their tests: the supervisor
state machine (clean stop, crash report, SIGTERM-ignoring child forced down by
SIGKILL, restart after a forced stop, stale exits, an error line that arrives
after the exit, no leaked file descriptors, appending log writes), the
external-link scheme allowlist, download staging over an existing file, and
the crash-reload budget. The updater tests cover version comparison, release
JSON to asset selection, the host allowlist, exact-name checksum parsing, the
in-place install decision, verification of an ad-hoc signed fixture (good,
wrong checksum, identifier, version, tampered, corrupt), and the install helper
run against paths with spaces, quotes and `$(…)`, including a restore after a
failed swap. `UPDATE_E2E_APP=<path>/Interseptor.app` additionally verifies a
real built bundle. `go test ./cmd/interseptor-macapp` runs it on macOS.
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
and is safe only for an unsigned local build. The shell's own updater
replaces the whole bundle instead (see [Updates](#updates)).

**Local access is unauthenticated.** The control UI trusts loopback
connections that carry no API key, with a loopback `Host` and `Origin` (which
defeats DNS rebinding and cross-site requests). That is reasonable for a CLI you
started yourself, but weaker for an app that sits running in the background:
any local process running as you can drive the control API. There is currently
no setting that requires a key from local clients, so treat every local process
as trusted while the app runs, and quit it when you do not need it. Keys are
still required for every non-loopback client.

Do not change the app's environment by editing the bundle's `Info.plist`
(`LSEnvironment`): any edit inside the bundle breaks its code signature. Use
the `defaults` keys above, or, for another `INTERSEPTOR_*` variable, set it in
launchd's user environment before launching the app (it lasts until logout):

```bash
launchctl setenv INTERSEPTOR_NO_UPDATE_CHECK 1
```

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
