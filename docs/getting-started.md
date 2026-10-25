# Getting started

## Install

Interseptor is a single static binary — **no cgo, no Node, no runtime dependencies**.

### Recommended — `go install` (uses the release tags)

Requires **Go 1.25+**:

```bash
# latest release:
go install github.com/Veyal/interseptor/cmd/interseptor@latest
# …or pin a specific release:
go install github.com/Veyal/interseptor/cmd/interseptor@v2.1.0

interseptor        # if $(go env GOPATH)/bin is on your PATH
```

**Or update in place** (no `go install` to remember):

```bash
interseptor update              # latest release
interseptor update --check      # is a newer version out?
interseptor update --version 2.1.0
```

`interseptor update` downloads a prebuilt binary from [GitHub Releases](https://github.com/Veyal/interseptor/releases) when one is attached for your OS/arch (and verifies `checksums.txt` when present). If the release has no binary yet, it falls back to `go install` automatically.

**Or update from the UI.** When a newer release is out, the version badge at the bottom of the navigation rail reads `↑ vX.Y.Z available`. Click it to see the release notes, then **Update**: Interseptor downloads the release, verifies it against `checksums.txt` (a mismatch stops the update and leaves your binary untouched), and installs it while you keep working. **Restart to Update** then relaunches the new version on the same project and ports, and the page reconnects by itself. Copies installed with Homebrew or Scoop show the command to run instead (`brew upgrade interseptor`, `scoop update interseptor`); local development builds and binaries in a folder you cannot write to explain why they cannot update themselves. The macOS app runs its own updater when you click the badge. AI agents cannot start an update.

Every tagged version is listed on the [**Releases**](https://github.com/Veyal/interseptor/releases)
page with its changelog; `@latest` resolves to the newest tag, `@vX.Y.Z` pins one.

### From source

```bash
git clone https://github.com/Veyal/interseptor.git
cd interseptor
make build
./interseptor
```

`make build` and `make run` stamp local CLI builds with `<version>-local`
(for example, `2.1.0-local`). The version comes from the latest reachable Git
tag, or the declared fallback version in a source archive. Published release
binaries keep their normal version. Plain `go build` does not apply this stamp.

### Prebuilt binaries

Each tagged release attaches static binaries for **linux / macOS / windows** (amd64 & arm64) plus a
`checksums.txt` on the [Releases](https://github.com/Veyal/interseptor/releases) page (built by the
release workflow when a `v*` tag is pushed) —
download, verify the checksum, `chmod +x`, and run. (`go install` above is equivalent and always
tracks the latest release.)

### macOS app

Each release also attaches `Interseptor_<version>.dmg` and `Interseptor-v<version>-unsigned.zip`, a
native macOS app (macOS 12 or later, Apple silicon and Intel) that runs the same proxy and UI in its
own window with a menu bar, Dock tile and notifications.

**Easiest: install it from the CLI.** If you already have the `interseptor` binary, run:

```bash
interseptor install-desktop          # latest release into /Applications (or ~/Applications)
interseptor install-desktop --open   # same, then launch it
```

It downloads the release zip, checks its SHA-256 against `macos-checksums.txt`, the bundle identifier,
the version and the code signature, then swaps the app in atomically. A copy downloaded this way has
no quarantine flag, so the first launch needs no **Open Anyway** step. Run it again to update. Quit the
app first, because the command refuses while the app is running. See
[the CLI reference](cli-reference.md#install-the-macos-app).

**Or install the DMG by hand:**

1. Download the DMG from the [Releases](https://github.com/Veyal/interseptor/releases) page, open it,
   and drag **Interseptor** to **Applications**.
2. The app is not notarized, so the first launch is blocked. Open **System Settings → Privacy &
   Security**, find the message about Interseptor, and click **Open Anyway**. Or clear the quarantine
   flag once from a terminal:

   ```bash
   xattr -dr com.apple.quarantine /Applications/Interseptor.app
   ```

3. Launch **Interseptor**. The app starts the proxy on `127.0.0.1:8080` and the UI on
   `127.0.0.1:9966`, and stops them when you quit. If an Interseptor server is already running, the
   app opens that one and leaves it running when you quit.

## Quick start

1. **Run it.** `interseptor` starts the proxy on `127.0.0.1:8080` and the UI on `127.0.0.1:9966`.
   The UI opens in its own app window (an installed Chrome, Edge, Chromium or Brave in app mode,
   with no tabs or address bar). Without one of those browsers it opens in your default browser;
   with `--headless`, over SSH, or with no desktop, the log says which URL to open instead.
2. **Send traffic through it.** Point your browser/HTTP client at `127.0.0.1:8080`, or let
   Interseptor set the OS proxy for you (macOS, Windows, and GNOME Linux) — the first-run **Setup**
   wizard also offers **Launch test browser** to open an isolated Chrome/Chromium already pointed at
   the proxy. See [System proxy automation](proxy-and-tls.md#system-proxy-automation).
3. **For HTTPS, trust the CA** (see below) — then HTTPS flows are decrypted and editable.
4. **Work the loop.** Watch flows land in **Proxy**, send one to **Repeater** or **Intruder**, run
   the **Scanner**, set **Scope**, or flip on **Intercept** to hold/edit requests and responses.

For a LAN or mobile listener, read [Proxy listener exposure](proxy-and-tls.md#proxy-listener-exposure)
before rebinding. Authentication is off by default, so a non-loopback listener is an open proxy
until you set a username and password under **Settings → Proxy & network → Proxy authentication**.

Runtime data lives under `~/.interseptor/` (`interseptor.db`, `bodies/`, `ca/`). Full-project archives
intentionally use the compatibility filename `interceptor.db`. To start with an empty workspace,
create a new project; keep an archive before deleting existing project data. See [Projects and data](projects-and-data.md).

Continue with the [workspace guide](workspace.md) for the main menus and response views, or
[Settings](settings.md) to find a specific configuration section.

## Intercepting HTTPS

1. Point your client at the proxy (`127.0.0.1:8080`).
2. Download the CA from the **Settings** tab (or `http://127.0.0.1:9966/api/ca.crt`) and
   install/trust it in your OS/browser trust store.
3. HTTPS flows are now decrypted, captured, and editable. Per-host leaf certs are minted on demand
   and cached.

## Configuration

| Environment variable | Effect |
|---|---|
| `INTERSEPTOR_HEADLESS` | Don't open the UI window on start (same as `--headless`); the UI URL is logged instead. |
| `INTERSEPTOR_OPEN_BROWSER` | Open the UI window even when started over SSH (same as `--open`). |
| `INTERSEPTOR_NO_BROWSER` | Never open the UI window, overriding `--open`/`INTERSEPTOR_OPEN_BROWSER`. |
| `INTERSEPTOR_CHROME` | Exact Chrome/Chromium binary for the UI window and PDF export. |
| `INTERSEPTOR_ALLOW_EXTERNAL_BIND` | Lock down to **loopback-only** binds when set to `0`/`false`. External bind (e.g. `0.0.0.0` for LAN capture) is allowed by default — see [Security model](architecture.md#security-model). |
| `INTERSEPTOR_CONTROL_URL` | For `interseptor mcp`: the control API to drive (default `http://127.0.0.1:9966`). External MCP clients connect through this server; model providers are configured outside Interseptor. |
| `INTERSEPTOR_CONTROL_ADDR` | Env equivalent of `--control-addr`: full control UI/API listen address (`host:port`). |
| `INTERSEPTOR_PROJECT` | Env equivalent of `--project`: open a specific project by name/path. |
| `INTERSEPTOR_PROXY_ADDR` | Override the proxy listen address(es) (also how the launcher gives each spawned instance its own port). |
| `INTERSEPTOR_NO_UPDATE_CHECK` | Disable the background update check Interseptor runs on every startup. |
| `GITHUB_TOKEN` / `INTERSEPTOR_GITHUB_TOKEN` / `GH_TOKEN` | Authenticates update checks and downloads (first non-empty wins). Required when this repository's GitHub releases are private; the token needs repository access. |

The proxy bind address is also runtime-configurable in **Settings** (and persisted).

For the complete command surface, see the [CLI reference](cli-reference.md). For CA trust, origin
verification, passthrough, pinning, and upstream chaining, see [Proxy, TLS, and networking](proxy-and-tls.md).

## Running multiple projects

For one-off multi-instance use, `interseptor` takes root flags: `--project <name|path>` (or
`INTERSEPTOR_PROJECT`) opens a specific project; `--control-port <port>`
picks the control UI/API port on loopback (default `9966`); `--control-addr host:port` sets the full
control listen address and overrides `--control-port` (or `INTERSEPTOR_CONTROL_ADDR` — see
[Configuration](#configuration)). Pair `--control-port`/`--control-addr` with `INTERSEPTOR_PROXY_ADDR`
to give a second manually-launched instance its own proxy port too.

For running several projects at once, **`interseptor launcher`** is a small dashboard process
(default `http://127.0.0.1:9965`, no separate auth setup — it auto-generates a local token on start)
that starts/stops per-project Interseptor instances, each its own OS process with its own
auto-allocated control+proxy ports, sharing only the global CA and Starlark checks. Closing the
launcher does not stop the instances it spawned.

**`interseptor stop`** gracefully stops all running Interseptor instances (SIGINT/SIGTERM, waiting
up to a `--timeout`, default 6s); add `--force`/`-f` to force-kill immediately instead. It leaves
AI agents' `interseptor mcp` bridges, the launcher and the vault running and says so; add `--all`
to stop those too.
