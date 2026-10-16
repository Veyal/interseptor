# Finding image evidence

Findings support three body block types: `text`, `flow`, and `image`.

## Persist shape (in `findings.body`)

```json
{"type":"image","hash":"<64-char sha256>","mime":"image/png","caption":"...","role":"result","proof":"...","source":"browser_screenshot","sourceFlowId":123}
```

`role`, `proof`, `source`, and `sourceFlowId` are optional provenance metadata. Never store `data`,
`path`, or `url` in the body JSON; image availability is derived when the finding is read.

## Upload

- REST: `POST /api/findings/{id}/images` with
  `{data, mime?, caption?, role?, proof?, source?, sourceFlowId?, position?}`
- MCP: `add_finding_image` (same fields) — for real browser/device screenshots you already have;
  uploads default conservatively to `source=operator_upload`
- Bytes go through `PutAndAttachImage` → content-addressed `bodiesDir` (same as flow bodies), with
  upload and attachment protected from body GC as one operation
- Max 5 MiB; raster MIME and dimensions are validated before storage

## Flow PNG previews (tool-styled HTTP screenshots)

Use these when a labeled rendering of captured HTTP improves report readability. Keep the captured
flow attached as the inspectable raw evidence, and do not present a preview as a browser screenshot.

- REST: `GET /api/flows/{id}/preview.png?side=both|req|res&pretty=0|1&layout=vertical|horizontal&theme=dark|light`
- REST: `POST /api/findings/{id}/flow-preview` with
  `{flowId, side?, pretty?, layout?, theme?, caption?, role?, proof?, position?}` — render + attach
- MCP: `render_flow_preview` with `flowId` + optional `findingId` / `pretty` / `layout` / `theme` /
  `role` / `proof` (pass `findingId` to attach it)

Defaults: `side=both`, `pretty=true`, `layout=vertical` (request above response), `theme=light`. Pass `layout=horizontal` for request left / response right.

Generated PNGs use Interseptor chrome + monospace req/res panes (pure Go, no browser).

## Serve

`GET /api/findings/images/{hash}` — `nosniff`, sanitized Content-Type, long cache.

HTML report export (`?format=html`) rewrites image URLs to `data:` URIs so offline/client reports show
screenshots. Embedding is bounded to 5 MiB per image and 8 MiB across the report; evidence outside the
bound is marked unavailable rather than retaining a live API dependency.

## GC

`GCBodies` unions flow body hashes **and** hashes from finding image blocks (`FindingImageHashes`). Without that, `POST /api/flows/gc` deletes screenshots.

## UI

The canonical editor and evidence-first workflow live in `docs/findings-and-reporting.md`. Keep this
skill focused on image storage and rendering instead of maintaining a second finding template.

**＋ Screenshot** / flow attach / flow-preview PNG defaults: pretty, vertical (request above
response), light theme. Pass `layout=horizontal` for a side-by-side preview.

Click any screenshot (or markdown `.md-img`) → full-viewport lightbox: scroll / ± / double-click to zoom, drag to pan, Fit or Esc to close.

Prefer a real browser/device screenshot when visual state proves the issue. Use `render_flow_preview`
only for generated HTTP visuals. A caption identifies the artifact; `proof` states the exact
security-relevant claim it establishes.

For visual claims, use the readiness and image source rules in
[Findings and reporting](../../../docs/findings-and-reporting.md#evidence-rules).
Body edits and repeated uploads must preserve the generated origin of a known hash.


### Ingestion and classification

`source` is reviewer classification; server-owned `provenance` keeps original ingestion metadata.
Do not copy client-supplied provenance into canonical storage. Reclassification must preserve the
original ingestion and stamp the writing boundary/time. Known generated hashes cannot become
browser/device proof when reused. Historical revision image hashes are GC roots even after the
finding is deleted. Do not weaken those roots to reclaim space.
