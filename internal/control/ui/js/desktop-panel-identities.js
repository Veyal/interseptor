// desktop-panel-identities.js — desktop skin of the Identities panel
// (docs/desktop-design.md, Panels). The toolbar keeps the filter and New;
// From selected flow and Run access test move into the overflow menu (the
// ellipsis "More actions" button). Every control is the original element,
// re-parented.
import { whenReady, observe, createOverflow } from './desktop-panel-kit.js';

const $ = (sel) => document.querySelector(sel);

function wireToolbar() {
  const bar = $('#panel-identities .ident-bar');
  if (!bar || $('#identMoreTools')) return;
  const more = createOverflow({
    id: 'identMoreTools',
    tooltip: 'From selected flow, Run access test',
    items: [$('#identFromFlow'), $('#identRunTest')],
  });
  bar.appendChild(more.button);
}

// Detail buttons are rebuilt on every repaint: keep a tooltip on each so a
// narrow pane that drops labels still names them on hover.
function wireDetail() {
  const detail = $('#identDetail');
  if (!detail) return;
  observe(detail, () => {
    detail.querySelectorAll('.ident-actions .btn:not([title])').forEach((b) => { const t = b.textContent.trim(); if (t) b.title = t; });
  });
}

export function initIdentitiesDesktop() {
  whenReady(() => $('#identFromFlow') && $('#identRunTest'), wireToolbar);
  whenReady(() => $('#identDetail'), wireDetail);
}
