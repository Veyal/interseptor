// map-model.js — pure helpers for the Map panel (no imports, no DOM) so they
// run under `node --test`: graph zoom math, endpoint URLs and count wording.

export const MAP_ZOOM_MIN = 0.1;
export const MAP_ZOOM_MAX = 4;

const clampScale = (k) => Math.max(MAP_ZOOM_MIN, Math.min(MAP_ZOOM_MAX, k));

// wheelZoomFactor turns one ctrl/cmd wheel event into a scale factor. A mouse
// wheel notch reports a large delta (100 px, or 3 lines) and steps ~10%; a
// trackpad pinch streams many small deltas, which map continuously.
export function wheelZoomFactor(deltaY, deltaMode = 0) {
  const dy = deltaMode === 1 ? deltaY * 33 : deltaY; // 3 lines ~ one 100 px notch
  if (!dy) return 1;
  if (Math.abs(dy) >= 50) return Math.exp(-Math.sign(dy) * 0.1 * Math.min(3, Math.abs(dy) / 100));
  return Math.exp(-dy * 0.01);
}

// zoomAround re-scales the graph keeping the point (mx,my) fixed on screen.
export function zoomAround(zoom, mx, my, nextK) {
  const nk = clampScale(nextK);
  return { k: nk, x: mx - (mx - zoom.x) * (nk / zoom.k), y: my - (my - zoom.y) * (nk / zoom.k) };
}

// gestureZoom is the trackpad pinch in Safari/WKWebView, which arrives as
// gesturestart/gesturechange (not ctrl+wheel). `scale` is cumulative since
// gesturestart, so it multiplies the scale captured at the start.
export function gestureZoom(startZoom, scale, mx, my) {
  const s = Number(scale);
  if (!Number.isFinite(s) || s <= 0) return { ...startZoom };
  const nk = clampScale(startZoom.k * s);
  return { k: nk, x: mx - (mx - startZoom.x) * (nk / startZoom.k), y: my - (my - startZoom.y) * (nk / startZoom.k) };
}

// endpointUrl is the endpoint's address as the origin sees it: the port shows
// unless it is the scheme's default.
export function endpointUrl(e) {
  const scheme = e.scheme || 'http';
  const port = Number(e.port) || 0;
  const def = (scheme === 'https' && port === 443) || (scheme === 'http' && port === 80);
  return scheme + '://' + e.host + (port && !def && !/:\d+$/.test(e.host) ? ':' + port : '') + (e.path || '/');
}

// paramsCountText is the Params view's status line; it says when the miner saw
// only the newest sample so hit counts are not read as totals.
export function paramsCountText(d) {
  const n = (d && d.flowsScanned) || 0;
  return n.toLocaleString('en-US') + ' flows · param miner' + (d && d.capped ? ' · newest sample, hit counts are partial' : '');
}

// mapCountSuffix explains a header count that differs from the raw endpoint
// count other controls (Domain filter, Expand all) report.
export function mapCountSuffix(shown, raw) {
  return raw > shown ? ` (${raw.toLocaleString('en-US')} before collapsing)` : '';
}
