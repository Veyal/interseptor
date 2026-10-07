#!/usr/bin/env node
// Icon contact sheet: renders every <symbol id="i-*"> from the control UI's
// inline sprite at 16, 20, 24 and 48px, on a dark and a light ground, with
// labels, so the icon family can be reviewed (and screenshotted) as a whole.
//
//   node scripts/ui_icon_sheet.mjs [out.html]
//
// Writes a self-contained static page (default: ./icon-sheet.html); open it in
// a browser or screenshot it with headless Chrome. Needs Node >= 18, no deps.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const indexPath = resolve(here, '../internal/control/ui/index.html');
const out = resolve(process.argv[2] || 'icon-sheet.html');
const html = readFileSync(indexPath, 'utf8');

const sprite = html.match(/<svg id="iconSprite"[\s\S]*?<\/svg>/);
if (!sprite) {
  console.error('iconSprite not found in index.html');
  process.exit(1);
}
const ids = [...sprite[0].matchAll(/<symbol id="(i-[a-z0-9-]+)"/g)].map(m => m[1]);

const sizes = [16, 20, 24, 48];
const cell = id =>
  `<figure><div class="row">${sizes
    .map(s => `<svg class="ic" width="${s}" height="${s}" aria-hidden="true"><use href="#${id}"/></svg>`)
    .join('')}</div><figcaption>${id.slice(2)}</figcaption></figure>`;
const grid = ids.map(cell).join('\n');

const page = `<!doctype html><meta charset="utf-8"><title>Interseptor icon sheet</title>
<style>
  body{margin:0;font:12px/1.3 system-ui,-apple-system,sans-serif}
  .ground{padding:16px;display:grid;grid-template-columns:repeat(auto-fill,minmax(152px,1fr));gap:8px 10px}
  .dark{background:#0b0f14;color:#d7e0ea}
  .light{background:#f6f8fa;color:#1f2933}
  h2{grid-column:1/-1;margin:0;font-size:13px;font-weight:600;opacity:.7}
  figure{margin:0;padding:8px;border:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:8px}
  .row{display:flex;gap:10px;align-items:center;min-height:52px}
  figcaption{margin-top:6px;opacity:.75;font-size:11px;overflow-wrap:anywhere}
  .ic{fill:none;stroke:currentColor;stroke-width:1.75;stroke-linecap:round;stroke-linejoin:round;flex:none}
  .ic.accent{color:#00e08a}
</style>
${sprite[0].replace('<svg id="iconSprite"', '<svg id="iconSprite" style="position:absolute;width:0;height:0"')}
<section class="ground dark"><h2>Dark - ${ids.length} icons at 16 / 20 / 24 / 48</h2>${grid}</section>
<section class="ground light"><h2>Light</h2>${grid}</section>
`;
writeFileSync(out, page);
console.log(`wrote ${out} (${ids.length} icons)`);
