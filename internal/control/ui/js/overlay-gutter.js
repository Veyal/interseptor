// overlay-gutter.js — keeps a highlight overlay (<pre>) wrapping at the same
// width as the transparent textarea over it. WebKit with classic scrollbars does
// not reserve a scrollbar gutter on the overflow:hidden <pre> (scrollbar-gutter
// is not honoured there), so once the textarea shows a scrollbar its text wraps
// narrower and the colors drift from the caret. The overlay gets the
// difference as extra right padding. Pure parts run under `node --test`.

export function overlayPaddingRight(taPad, taGutter, hlGutter) {
  return Math.max(0, taPad + taGutter - hlGutter);
}

const px = (v) => parseFloat(v) || 0;
const gutter = (el, cs) => Math.max(0, el.offsetWidth - el.clientWidth - px(cs.borderLeftWidth) - px(cs.borderRightWidth));

// syncOverlayGutter measures both boxes (computed styles may be passed in for
// tests) and sets the overlay's right padding.
export function syncOverlayGutter(ta, hl, taStyle, hlStyle) {
  if (!ta || !hl) return;
  const tcs = taStyle || getComputedStyle(ta);
  const hcs = hlStyle || getComputedStyle(hl);
  hl.style.paddingRight = '';
  const base = px(tcs.paddingRight);
  const next = overlayPaddingRight(base, gutter(ta, tcs), gutter(hl, hcs));
  hl.style.paddingRight = next === base ? '' : next + 'px';
}

// wireOverlayGutter keeps the pair in sync as the textarea is resized or its
// scrollbar comes and goes (content-box changes), and after each input.
export function wireOverlayGutter(ta, hl) {
  if (!ta || !hl || ta._overlayGutter) return;
  ta._overlayGutter = true;
  const sync = () => syncOverlayGutter(ta, hl);
  if (typeof ResizeObserver === 'function') new ResizeObserver(sync).observe(ta);
  ta.addEventListener('input', sync);
  sync();
}
