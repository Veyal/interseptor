# Copy Abilities

**Copy Abilities** are installable extension bundles that let you add new features to Interseptor without forking or recompiling the binary. An ability is a self-contained folder (or `.tar.gz`/`.zip` archive) with an `ability.json` manifest, optional ES module UI files, and optional sandboxed Starlark scripts.

An approved ability can inject:

- **Sidebar menus and workspace panels** under existing navigation groups or an **Abilities** group
- **Flow inspector tabs** inside the History / Flow Drawer inspector
- **Context-menu actions** for flows (`"flow"` is mounted in v1; `"selection"` and `"workspace"` are reserved)
- **Command-palette commands** (`Ctrl/⌘+K`)
- **Background `flow.captured` hooks** in sandboxed Starlark that auto-tag or annotate flows
- **On-demand Starlark actions**, optionally exposed to connected AI agents via MCP

Newly installed or modified abilities start **quarantined** and stay inert until a human operator inspects and explicitly trusts them.

## Where abilities live

Abilities can be installed globally or per project:

| Scope | Directory | Trust record | Notes |
|---|---|---|---|
| `global` | `~/.interseptor/abilities/<id>/` | `~/.interseptor/abilities-state.json` | Available in every project (under `$INTERSEPTOR_DATA_DIR/` when `--data-dir` or `INTERSEPTOR_DATA_DIR` is set). |
| `project` | `<projectDir>/abilities/<id>/` | `~/.interseptor/ability-approvals/<sha256>/abilities-state.json` | Available only when that project is open; overrides a global ability with the same `id`. Approvals are stored machine-locally keyed by the canonical `projectDir` SHA-256 and never read from `<projectDir>/abilities-state.json`. The `default` project aliases `~/.interseptor`, so only `global` scope is available there. |

A typical ability folder looks like this:

```text
my-ability/
├── ability.json
├── ui/
│   ├── panel.js
│   └── panel.css
└── hooks/
    ├── on-flow.star
    └── inspect.star
```

## Manifest (`ability.json`)

Every ability requires an `ability.json` manifest at its root using schema `interseptor.ability/v1`.

### Top-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `schema` | string | yes | Must be `"interseptor.ability/v1"`. |
| `id` | string | yes | Unique identifier matching `^[a-z0-9][a-z0-9-]{0,63}$`. |
| `name` | string | yes | Human-readable display name shown in **Configure → Abilities**. |
| `version` | string | yes | Version string (for example `"0.1.0"`). |
| `description` | string | no | Short summary of what the ability does. |
| `author` | string | no | Author or maintainer name. |
| `icon` | string | no | Sprite icon name without the `i-` prefix (see [UI icon system](ui-icons.md)). |
| `capabilities` | array of strings | no | Informational review labels for declared features (`"ui.menu"`, `"ui.panel"`, `"ui.contextMenu"`, `"ui.inspectorTab"`, `"ui.command"`, `"flow.hook"`, `"ability.action"`, `"mcp.tool"`); these do not sandbox UI JavaScript. |
| `menus` | array | no | Sidebar navigation entries and their panel modules. |
| `contextMenus` | array | no | Right-click / context menu entries. |
| `inspectorTabs` | array | no | Extra tabs in the flow inspector. |
| `commands` | array | no | Entries registered in the `Ctrl/⌘+K` command palette. |
| `hooks` | array | no | Starlark lifecycle hooks (`flow.captured`). |
| `actions` | array | no | On-demand Starlark actions (UI, REST, and optional MCP). |

### `menus[]`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique menu slot ID within the ability. |
| `label` | string | Sidebar menu label. |
| `group` | string | Sidebar group: `"capture"`, `"test"`, `"recon"`, `"report"`, `"configure"`, or `"abilities"`. |
| `icon` | string | Sprite icon name (without `i-`). |
| `entry` | string | Relative path to the UI ES module (for example `"ui/panel.js"`). |
| `style` | string | Optional relative path to a stylesheet (for example `"ui/panel.css"`). |
| `description` | string | Short description shown in navigation/palette surfaces. |

### `contextMenus[]`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique context-menu entry ID. |
| `label` | string | Menu item label. |
| `target` | string | Where the item appears: `"flow"` (mounted on flow rows and inspector panes; `"selection"` and `"workspace"` are accepted in manifests as reserved values). |
| `icon` | string | Sprite icon name (without `i-`). |
| `panel` | string | Optional menu/panel ID to activate when invoked. |
| `action` | string | Optional action ID to invoke. |
| `entry` | string | Optional relative ES module path (`"ui/..."`). |

### `inspectorTabs[]`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique inspector tab ID within the ability. |
| `label` | string | Tab button label shown in the flow inspector. |
| `side` | string | Flow pane(s) where the tab appears: `"both"`, `"req"`, or `"res"`. |
| `entry` | string | Relative ES module path (`"ui/..."`). |
| `style` | string | Optional relative CSS path (`"ui/..."`). |

### `commands[]`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique command ID within the ability. |
| `title` | string | Title shown in the command palette (`Ctrl/⌘+K`). |
| `keywords` | array of strings or string | Search keywords for command-palette matching (whitespace-split when given as a string). |
| `panel` | string | Optional menu/panel ID to open. |
| `action` | string | Optional action ID to invoke. |

### `hooks[]`

| Field | Type | Description |
|---|---|---|
| `event` | string | Hook event name. Currently `"flow.captured"`. |
| `entry` | string | Relative Starlark script path (`"hooks/..."`). |

### `actions[]`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique action ID within the ability. |
| `name` | string | Display name for the action. |
| `description` | string | Summary of what the action accepts and returns. |
| `entry` | string | Relative Starlark script path (`"hooks/..."`). |
| `mcp` | boolean | When `true`, exposes the action through MCP `run_ability_action` (while trusted and enabled). |

### Example `ability.json`

```json
{
  "schema": "interseptor.ability/v1",
  "id": "example-header-audit",
  "name": "Example Header Audit",
  "version": "0.1.0",
  "description": "Flags missing security headers on api.example.com responses and summarizes them in a workspace panel.",
  "author": "Example Author",
  "icon": "checks",
  "capabilities": [
    "ui.menu",
    "ui.panel",
    "ui.inspectorTab",
    "ui.contextMenu",
    "ui.command",
    "flow.hook",
    "ability.action",
    "mcp.tool"
  ],
  "menus": [
    {
      "id": "audit-panel",
      "label": "Header Audit",
      "group": "abilities",
      "icon": "checks",
      "entry": "ui/panel.js",
      "style": "ui/panel.css",
      "description": "Summarize header checks across captured example.com flows."
    }
  ],
  "inspectorTabs": [
    {
      "id": "header-tab",
      "label": "Header Audit",
      "side": "res",
      "entry": "ui/tab.js",
      "style": "ui/panel.css"
    }
  ],
  "contextMenus": [
    {
      "id": "audit-selected-flow",
      "label": "Run Header Audit",
      "target": "flow",
      "icon": "checks",
      "action": "audit-flow"
    }
  ],
  "commands": [
    {
      "id": "open-header-audit",
      "title": "Open Header Audit",
      "keywords": ["headers", "audit", "example"],
      "panel": "audit-panel"
    }
  ],
  "hooks": [
    {
      "event": "flow.captured",
      "entry": "hooks/on-flow.star"
    }
  ],
  "actions": [
    {
      "id": "audit-flow",
      "name": "Audit flow headers",
      "description": "Inspect response security headers for a captured flow.",
      "entry": "hooks/audit.star",
      "mcp": true
    }
  ]
}
```

## Frontend JS UI SDK

UI slots (`menus[]` and `inspectorTabs[]`) load standard browser ES modules served from the revision-pinned path `/abilities/{id}/{digest}/{asset...}` (`Cache-Control: no-store`) once the ability is trusted and enabled. Injected flow inspector tabs appear in both the Proxy bottom inspector and the Flow Drawer (`req`, `res`, or `both` panes), and `flow` context-menu actions appear under an **Abilities** submenu on flow rows and inspector panes.

Each UI module exports `mount(container, ctx)` (required) and optionally `update(container, ctx)` and `unmount(container)`:

```javascript
export function mount(container, ctx) {
  // Render DOM inside `container`, wire listeners, subscribe to SSE if needed.
}

export function update(container, ctx) {
  // Optional: update an already-mounted panel or inspector tab when flow/action context changes.
}

export function unmount(container) {
  // Optional: clean up event listeners, timers, and SSE subscriptions.
}
```

A `contextMenus[]` entry that supplies `entry` (without `panel` or `action`) is imported on click and invoked via `run(ctx)` or `default(ctx)`. Module load, `mount`, and `update` errors are isolated inside a per-slot error boundary with **Retry** and **Open Abilities** actions so a broken ability never crashes the workspace.

### `ctx` reference

| Member | Kind | Description |
|---|---|---|
| `ctx.abilityId` | string | ID of the owning ability. |
| `ctx.menuId` | string | Active slot ID (menu ID, inspector tab ID, or context-menu ID). |
| `ctx.tabId` | string | Same slot ID (`menuId` alias, helpful in inspector tabs). |
| `ctx.side` | string (optional) | Inspector tab side (`"both"`, `"req"`, or `"res"`). |
| `ctx.api(path, opts?)` | async function | Calls Interseptor's control REST API with UI headers and parsed JSON handling. |
| `ctx.runAction(actionId, input?)` | async function | Executes one of this ability's declared `actions[]` (`POST /api/abilities/{id}/actions/{action}`) and returns `{ok: true, result}`. |
| `ctx.getSelectedFlow()` | function | Returns the flow passed from a context menu (consumed once) or the currently selected flow in Proxy/History, or `null` when none is selected. |
| `ctx.openFlow(flowId, opts?)` | function | Opens the Flow Drawer inspector on `flowId`. |
| `ctx.activateTab(tabId)` | function | Navigates to and focuses a workspace panel tab. |
| `ctx.onSse(type, fn)` | function | Subscribes to a live SSE event type (for example `"flow.new"` or `"ability.update"`); returns an unsubscribe function and is cleaned up automatically on `unmount`. |
| `ctx.offSse(type)` | function | Unsubscribes all listeners registered by this slot for `type` (or all types when omitted). |
| `ctx.toast(msg, severity?)` | function | Shows a themed toast notification (`"info"`, `"success"`, `"warn"`, or `"error"`). |
| `ctx.toastError(prefix, err)` | function | Formats and displays an error toast. |
| `ctx.esc(str)` | function | HTML-escapes untrusted strings (such as hosts, paths, or headers) before building HTML markup. |
| `ctx.icon(name, label?)` | function | Returns inline SVG sprite markup for a built-in Interseptor icon (`#i-<name>`). |
| `ctx.flow`, `ctx.actionResult`, `ctx.result` | optional | Populated when a panel or inspector tab is opened with flow context or immediately after a context-menu/command action run. |

### Example UI module (`ui/panel.js`)

```javascript
export function mount(container, ctx) {
  let off = null;

  async function render() {
    const flow = ctx.getSelectedFlow();
    if (!flow) {
      container.innerHTML = `<div class="ability-box"><p>Select a flow in Proxy to audit <code>api.example.com</code> headers.</p></div>`;
      return;
    }
    container.innerHTML = `
      <div class="ability-box">
        <h3>${ctx.icon('checks')} ${ctx.esc(flow.method)} ${ctx.esc(flow.host)}${ctx.esc(flow.path)}</h3>
        <button type="button" class="btn" id="runAuditBtn">Run Audit Action</button>
        <pre id="auditOut"></pre>
      </div>`;
    container.querySelector('#runAuditBtn')?.addEventListener('click', async () => {
      try {
        const res = await ctx.runAction('audit-flow', { flowId: flow.id });
        container.querySelector('#auditOut').textContent = JSON.stringify(res, null, 2);
      } catch (err) {
        ctx.toastError('Audit failed', err);
      }
    });
  }

  off = ctx.onSse('flow.new', () => render());
  render();

  container._abilityCleanup = () => {
    if (typeof off === 'function') off();
  };
}

export function unmount(container) {
  if (typeof container._abilityCleanup === 'function') {
    container._abilityCleanup();
    delete container._abilityCleanup;
  }
  container.innerHTML = '';
}
```

## Starlark hooks and actions

Ability scripts under `hooks/*.star` run in Interseptor's sandboxed Starlark engine (using the shared `internal/starx` standard library).

- **Sandbox guarantees:** No filesystem access, no raw sockets or network egress, no environment variable access, no `load()`, and no ability to mutate Interseptor settings or bypass scope. Symlinks escaping the ability directory and hidden dotfiles are rejected.
- **Execution and size budgets:** Each hook or action invocation is capped at **`100,000` (`100_000`) Starlark steps** (with a **5-second** timeout on `flow.captured` hooks and a **10-second** default timeout on actions; max nesting depth **128**). Request/response bodies passed to Starlark are capped at **64 KiB**, single ability files are capped at **2 MiB** (total uncompressed ability size **16 MiB**, max **512** files; archive uploads capped at **32 MiB**), and `flow.captured` hooks can apply at most **16** tags (64 bytes each) and a **4,096-byte** note per run.
- **Builtins:** Shared `internal/starx` helpers (`json_decode`, `json_encode`, `b64decode`, `b64encode`, `url_decode`, `url_encode`, `re_search`, `hash`, `hmac`, `sha256_hex`, `hmac_sha256_hex`, `aes_ecb_encrypt`, `aes_ecb_decrypt`, `aes_cbc_encrypt`, `aes_cbc_decrypt`, `aes_ctr`, `now_ms`, `random_hex`, `finding`) are available both as top-level globals and under the frozen `starx` module (for example `starx.b64encode(...)`).

### Flow hook: `on_flow(flow)`

Hooks registered for `"flow.captured"` run asynchronously via `plugin.OnFlowCaptured` after a flow reaches its final recorded state. They **never** run on the proxy hot path, so a slow or failing hook never delays or breaks HTTP/HTTPS forwarding.

`on_flow(flow)` receives a frozen `flow` dictionary with these keys:

| Key | Type | Description |
|---|---|---|
| `id` | int | Captured flow ID. |
| `method` | string | HTTP method (`GET`, `POST`, etc.). |
| `scheme` | string | `"http"` or `"https"`. |
| `host` | string | Target hostname. |
| `port` | int | Target port. |
| `path` | string | Request path and query string. |
| `status` | int | Response status code (`0` if unavailable). |
| `mime` | string | Response MIME type. |
| `reqHeaders` | dict | Request headers (`headerName -> list of strings`). |
| `resHeaders` | dict | Response headers (`headerName -> list of strings`). |
| `reqBody` | string | Request body text (capped at 64 KiB). |
| `resBody` | string | Response body text (capped at 64 KiB). |
| `note` | string | Current flow note. |

`on_flow(flow)` returns either `None` (no changes) or a dictionary with optional `tags` and/or `note`:

```python
# hooks/on-flow.star
def on_flow(flow):
    if flow["host"] != "api.example.com":
        return None
    if flow["status"] == 200 and not flow["resHeaders"].get("Content-Security-Policy"):
        return {
            "tags": ["missing-csp"],
            "note": "Response from api.example.com omitted Content-Security-Policy.",
        }
    return None
```

### Action: `run(ctx)`

Actions declared in `actions[]` are invoked on demand from an ability's UI (`ctx.runAction`), a context menu or command palette binding, or `POST /api/abilities/{id}/actions/{action}` (and MCP `run_ability_action` when `"mcp": true`).

`run(ctx)` receives a frozen `ctx` dictionary containing:

| Key | Type | Description |
|---|---|---|
| Top-level input fields + `input` | dict / any | Caller-supplied JSON fields are exposed at the top level of `ctx` and mirrored under `ctx["input"]` (unless the caller explicitly sent an `"input"` key). |
| `flow` | dict (optional) | Frozen flow dictionary (same fields as `on_flow(flow)`) loaded automatically when `flowId` (top-level or `input.flowId`) is a positive flow ID. |

`run(ctx)` returns any JSON-serializable value (dictionary, list, string, integer, float, boolean, or `None`):

```python
# hooks/audit.star
def run(ctx):
    flow = ctx.get("flow")
    if not flow:
        return {"ok": False, "error": "flowId is required"}
    headers = flow["resHeaders"]
    missing = [
        h for h in ["Strict-Transport-Security", "X-Content-Type-Options", "Content-Security-Policy"]
        if not headers.get(h)
    ]
    return {
        "ok": True,
        "flowId": flow["id"],
        "host": flow["host"],
        "missingHeaders": missing,
    }
```

## Security and quarantine trust model

> **Important:** Trusted UI ES modules run **same-origin in the Interseptor UI** with full operator UI privileges (access to `ctx.api`, DOM, storage, and session controls), **outside the Starlark sandbox**. Manifest `capabilities[]` are informative review labels, not a browser JS permission boundary. Only approve UI code from authors you trust, and reload the browser tab after revoking an ability to clear any already-evaluated ES module state from memory.

Interseptor treats every external or modified ability as **untrusted and inert until a human approves it**:

1. **Quarantined and inert on arrival.** Installing an ability from a directory or `.tar.gz`/`.zip` archive, copying files into `~/.interseptor/abilities/` or `<projectDir>/abilities/`, or scaffolding a new ability leaves it **quarantined** (untrusted and disabled). While quarantined:
   - No UI menus, inspector tabs, context menus, or command-palette entries are injected.
   - `/abilities/{id}/{digest}/*` returns `404` and refuses to serve any UI module or static asset. Only verified, immutable `ui/` snapshot bytes on trusted, enabled, non-drifted abilities matching `{digest}` are served (`Cache-Control: no-store`).
   - Starlark scripts are only parsed and statically resolved (`starlark.SourceProgramOptions`, `load()` disabled) — **no Starlark module top-level code executes** until the ability is explicitly trusted and enabled.
   - `flow.captured` hooks do not execute and actions cannot be invoked from the UI, REST, or MCP (`403`).
2. **Human-only, machine-owned, revision-bound approval.** Only an operator in the interactive web UI (**Configure → Abilities**) or via the local CLI (`interseptor ability trust <id>`, which enables by default unless `--enable=false` is passed) can approve an ability. UI approval (`POST /api/abilities/{id}/trust`) submits the reviewed `{digest, scope, enabled}` pair; the server re-snapshots the directory via `os.Root` before persisting trust and rejects stale or mismatched requests with `409 Conflict`. Project approvals live strictly in machine-owned `~/.interseptor/ability-approvals/<sha256>/abilities-state.json` — copying or opening an untrusted project directory never grants trust.
3. **SHA-256 drift detection.** Approving an ability records a canonical SHA-256 digest over all non-hidden regular files (`ability.json`, `ui/*`, `hooks/*`). Symlinked ability containers/directories and hidden manifest segments are rejected, and any post-approval file change on disk revokes trust on reload and automatically re-quarantines the ability (`409 Conflict` if toggled or invoked while drifted).
4. **AI / MCP / API-key isolation.** Control endpoints that install, scaffold, reload, trust, revoke, toggle, or delete abilities require an interactive UI session (`X-Interseptor-CSRF` + UI session cookie, with `X-Interseptor-Source` unset or `ui`); AI, MCP, and API-key callers receive `403`. MCP exposes only `list_abilities` (optional `enabledOnly` and `trustedOnly` booleans) and `run_ability_action` (required `abilityId` and `action`; optional `flowId` and `input`; restricted to actions on already-trusted, enabled abilities that explicitly declare `"mcp": true`). All non-UI callers (API keys, MCP, or `X-Interseptor-Source: ai`) are atomically refused `403` on actions lacking `"mcp": true`. Abilities cannot bypass target scope or weaken proxy security rules.

## Managing abilities

### Interactive UI (`Configure → Abilities`)

Open **Configure → Abilities** (or press `g b`) to:

- Inspect installed global and project abilities, their declared capabilities, trust status, and SHA-256 digest state.
- **Install** an ability from a `.tar.gz`/`.tgz`/`.zip` archive (use `interseptor ability install <dir>` for local folders), or **Scaffold** a starter ability.
- **Approve & Enable** a quarantined ability after reviewing its manifest and SHA-256 digest, **Enable / Disable** it, **Export** it as a portable `.tar.gz` archive, **Reload** abilities from disk, or **Remove** it.

### CLI

Use `interseptor ability` subcommands for local terminal management:

```text
interseptor ability list
interseptor ability install <archive-or-dir>
interseptor ability trust <id>
interseptor ability enable <id>
interseptor ability disable <id>
interseptor ability scaffold <id>
interseptor ability export <id>
interseptor ability remove <id>
```

See [CLI reference](cli-reference.md) for full command usage and [API and MCP](api-and-mcp.md) for the `/api/abilities*`, `/abilities/{id}/{digest}/*`, SSE (`ability.update`), and MCP tool contracts.

## Examples

The repository ships two complete example abilities you can inspect, install, or copy:

- [`examples/abilities/hello-ability/`](../examples/abilities/hello-ability/) — minimal starter showing a sidebar menu panel, flow context menu, command-palette item, `flow.captured` hook, and MCP-ready action.
- [`examples/abilities/jwt-xray/`](../examples/abilities/jwt-xray/) — practical JWT inspection ability with a Recon sidebar panel, Flow Inspector tab, `flow.captured` JWT/`alg=none` tagger, and deep JWT analyzer action (`mcp: true`).
