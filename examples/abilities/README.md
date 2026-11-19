# Example Copy Abilities

Copy Abilities extend Interseptor with custom UI panels, Flow Inspector tabs, command palette entries, flow context-menu actions, passive `flow.captured` Starlark hooks, and MCP-exposed actions — all packaged in a self-contained directory with no Go compiler or rebuild required.

## Directory Layout

Every Copy Ability lives in its own directory (`<ability-id>/`) containing an `ability.json` manifest and optional `ui/` and `hooks/` assets:

```text
<ability-id>/
├── ability.json          # Required manifest (schema: "interseptor.ability/v1")
├── ui/
│   ├── panel.js          # ES module exporting mount(container, ctx) and unmount(container)
│   ├── inspector.js      # Optional Flow Inspector tab ES module
│   └── style.css         # Scoped CSS using Interseptor theme tokens (--bg2, --fg, --line, ...)
└── hooks/
    ├── on_flow.star      # Starlark hook for "flow.captured" (def on_flow(flow): ...)
    └── action.star       # Starlark action handler (def run(ctx): ...)
```

### Manifest (`interseptor.ability/v1`)

| Field | Purpose |
| --- | --- |
| `schema` | Must be `"interseptor.ability/v1"` |
| `id`, `name`, `version`, `description`, `author`, `icon` | Identity and display metadata |
| `capabilities` | Declared capabilities: `ui.menu`, `ui.inspectorTab`, `ui.contextMenu`, `ui.command`, `flow.hook`, `ability.action`, `mcp.tool` |
| `menus` | Sidebar navigation panels (`entry`, optional `style`, `group`) |
| `inspectorTabs` | Tabs injected into the Flow Inspector (`side`: `"req"`, `"res"`, or `"both"`) |
| `contextMenus` | Flow row right-click actions (`target`: `"flow"`, `panel`, `action`) |
| `commands` | Entries registered in the `Cmd+K` / `Ctrl+K` command palette (`keywords` is an array of strings) |
| `hooks` | Sandboxed Starlark lifecycle hooks (e.g. `event: "flow.captured"`) |
| `actions` | Sandboxed Starlark actions (`mcp: true` exposes the action via MCP `run_ability_action` when trusted and enabled) |

## Installing an Ability

### Option 1: Install archive from the UI

1. Package the ability directory into a `.zip` or `.tar.gz` archive (containing `<ability-id>/ability.json` or `ability.json` at the root):
   ```bash
   zip -r hello-ability.zip hello-ability/
   ```
2. In Interseptor, open **Configure -> Abilities**.
3. Click **Install archive** and select the `.zip` or `.tar.gz` file.

### Option 2: Copy directly into `~/.interseptor/abilities/`

Copy an example directory into your user abilities folder:

```bash
mkdir -p ~/.interseptor/abilities
cp -r examples/abilities/hello-ability ~/.interseptor/abilities/
cp -r examples/abilities/jwt-xray ~/.interseptor/abilities/
```

Then open **Configure -> Abilities** and click **Reload** (or restart Interseptor).

## Quarantine and Approval Model

Interseptor follows a default-deny trust model for extensions:

1. **Quarantine on arrival**: Newly installed or imported abilities start **quarantined** (untrusted and disabled). Their UI modules, Starlark hooks, and MCP actions do not load or execute automatically.
2. **Human-only approval**: Open **Configure -> Abilities** in the browser UI (or run `interseptor ability trust <id>` and `interseptor ability enable <id>` locally), inspect the manifest, declared `capabilities`, UI scripts, and Starlark code, and explicitly trust and enable the ability. AI/MCP callers and API-key clients can never approve or un-quarantine an ability.
3. **SHA-256 integrity pinning**: Trust records are stored per scope in `abilities-state.json`. If an ability's files are modified on disk after approval, its SHA-256 digest changes and Interseptor detects the drift and returns the ability to quarantine until a human reviews and re-trusts it.

## Included Examples

### 1. [`hello-ability/`](hello-ability/) — Minimal Starter Ability

A clean template demonstrating every `interseptor.ability/v1` surface:

- **Sidebar menu** (`Hello Ability`) and **Command Palette** entry (`Hello Ability: Open panel`)
- **Flow context menu** (`Greet with Hello Ability`) wired to `summarize_flow`
- **Starlark `flow.captured` hook** ([`hooks/on_flow.star`](hello-ability/hooks/on_flow.star)) tagging `example.com` or `/api/` traffic with `hello-api`
- **Starlark action & MCP tool** ([`hooks/summarize.star`](hello-ability/hooks/summarize.star)) returning a structured flow summary (`method`, `host`, `path`, `status`, `summary`)
- **Frontend UI** ([`ui/panel.js`](hello-ability/ui/panel.js) + [`ui/style.css`](hello-ability/ui/style.css)) showing `ctx.getSelectedFlow()`, invoking `ctx.runAction(...)`, subscribing to `ctx.onSse('flow.new', ...)`, and cleaning up cleanly in `unmount(container)`

### 2. [`jwt-xray/`](jwt-xray/) — Pentester JWT Inspector

A practical engagement ability for inspecting JSON Web Tokens across captured traffic:

- **Automatic detection hook** ([`hooks/on_flow.star`](jwt-xray/hooks/on_flow.star)) scanning `Authorization`, `Cookie`, and `Set-Cookie` headers for three-segment `eyJ...` JWTs and tagging flows with `jwt` (plus `jwt-alg-none` when `"alg":"none"` is detected)
- **Deep analyzer action & MCP tool** ([`hooks/analyze.star`](jwt-xray/hooks/analyze.star)) extracting JWTs from request/response headers, path, and bodies (or a manually supplied `token`), decoding header and payload JSON, and flagging `alg=none`, empty signatures, `jku`/`x5u`/`jwk` headers, suspicious `kid` values, missing `exp`, expired tokens, and sensitive payload claim names
- **Recon sidebar panel** ([`ui/panel.js`](jwt-xray/ui/panel.js)) for scanning the latest 50 flows (`/api/flows?limit=50`), inspecting the selected flow, or decoding pasted tokens
- **Flow Inspector tab** ([`ui/inspector.js`](jwt-xray/ui/inspector.js)) mounted directly inside the Flow Inspector (`JWT` tab for both request and response sides)
