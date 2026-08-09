# macOS app bundle

Builds `Interseptor.app` — a native macOS bundle wrapping the existing static Go
binary. Nothing about the proxy or the web UI changes; the bundle only adds a
double-clickable way to start them.

## Build

```bash
make macos-app                   # → dist/macos/Interseptor.app (unsigned)
MAKE_DMG=1 make macos-app        # also produce a DMG
```

Must run on macOS: the script needs `lipo`, and `iconutil`/`codesign` when those
steps are enabled. Both binaries are built universal (x86_64 + arm64).

## What's inside

```
Interseptor.app/Contents/
  Info.plist
  MacOS/interseptor-macapp   ← CFBundleExecutable, the launcher
  MacOS/interseptor          ← the real server binary, unchanged
  Resources/AppIcon.icns     ← only if packaging/macos/AppIcon.iconset exists
```

`interseptor-macapp` (source: `cmd/interseptor-macapp`) is a thin supervisor. It
resolves the sibling `interseptor` binary, starts it with `INTERSEPTOR_NO_BROWSER=1`,
polls the control URL until it accepts connections, then opens that URL in the
default browser. Launching the app a second time while it is already running just
reopens the UI instead of failing on a port conflict. Quitting forwards SIGTERM to
the child so it takes its normal graceful shutdown path, escalating to SIGKILL
after 6s — the same window `interseptor stop` uses.

Because a Finder-launched app has nowhere to print, startup failures surface as a
native alert and the server's stdout/stderr are appended to
`~/Library/Logs/Interseptor.log`.

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
waits, and staples the ticket so Gatekeeper can verify offline.

## Icon

The repository ships no application icon, because it contains no brand mark to
derive one from. Supply either:

- `packaging/macos/AppIcon.png` — a single square PNG (1024×1024 recommended).
  The build expands it into every required size with `sips` and converts it with
  `iconutil`. This is the easy path.
- `packaging/macos/AppIcon.iconset` — a hand-built Apple iconset
  (`icon_16x16.png` … `icon_512x512@2x.png`). Takes precedence over the PNG when
  both exist, so you can hand-tune small sizes where automatic downscaling of a
  detailed mark goes mushy.

Without either, the bundle uses the generic app icon and everything else still
works.

## Known trade-offs

**No Dock tile.** `Info.plist` sets `LSUIElement`, so the app runs as a background
agent. The browser is the UI, and a launcher process that owns a Dock tile it
cannot draw into would show as "not responding" and offer only Force Quit —
which skips graceful shutdown. Stop it with `interseptor stop`, or from the UI.

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
plist and universal binaries, and uploads it as an artifact — unsigned, because
signing needs a Developer ID certificate and notary credentials that CI does not
have. `release.yml` is untouched and still publishes only tarballs from
`ubuntu-latest`; attaching a signed `.app` to releases means adding a
`macos-latest` job with those credentials in repository secrets.

**Homebrew cask needs notarization.** Homebrew is phasing out unsigned casks, so
cask distribution is not viable without the signing steps above.
