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
  const raw = String(e.host || '');
  // Captured IPv6 hosts are stored unbracketed (net/url Hostname()). More than
  // one colon means an IPv6 literal, not host:port; bracket it and never mistake
  // "::1" for a host that already carries a port.
  const ipv6 = raw.includes(':') && !raw.startsWith('[') && raw.split(':').length > 2;
  const host = ipv6 ? '[' + raw + ']' : raw;
  const hasPort = !ipv6 && (/:\d+$/.test(raw) || /^\[[^\]]+\]:\d+$/.test(raw));
  return scheme + '://' + host + (port && !def && !hasPort ? ':' + port : '') + (e.path || '/');
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

// MAP_FILTER_DEFAULTS is the rest state of the controls that live inside the
// Map Filters popover: 403/404-only hiding and identical collapsing are on,
// "Without evidence" is off. Domain and search are NOT counted: they stay in
// the toolbar row where their state is already visible.
export const MAP_FILTER_DEFAULTS = { hideNoise: true, collapseIdentical: true, onlyUnlinked: false };

// mapFilterCount is the badge on the Filters button: how many popover filters
// are away from their default. Purely derived, so the button label, the badge
// and the tests all read the same number. `filterState` is a mapState-shaped
// object (method, statusClass, tag, hideNoise, collapseIdentical, onlyUnlinked).
export function mapFilterCount(filterState) {
  const f = filterState || {};
  let n = 0;
  if (String(f.method || '').trim()) n++;
  if (Number(f.statusClass) > 0) n++;
  if (String(f.tag || '').trim()) n++;
  if (f.hideNoise === false) n++;
  if (f.collapseIdentical === false) n++;
  if (f.onlyUnlinked === true) n++;
  return n;
}

// mapFiltersLabel is the button's visible text: plain "Filters" at rest, and
// "Filters · N" once something inside the popover is active.
export function mapFiltersLabel(n) {
  const count = Number(n) > 0 ? Math.floor(Number(n)) : 0;
  return count ? `Filters · ${count}` : 'Filters';
}
