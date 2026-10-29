// render-base.js — pure helper for the sandboxed HTML "Render" views. A captured
// body is shown via iframe srcdoc, whose relative URLs would otherwise resolve
// against the control UI origin. Injecting <base href="<flow URL>"> makes the
// page's CSS and images resolve against the flow's own origin. No DOM access.

const httpURL = (value, base) => {
  try {
    const u = new URL(value, base);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u : null;
  } catch (e) { return null; }
};

const attrEscape = (s) => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/'/g, '&#39;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// withBaseHref(body, baseURL) returns body with a <base href> for baseURL. The
// first <base href> in a document wins, so an absolute http(s) base the page
// already declares is left alone; a relative one is resolved against baseURL and
// our resolved copy goes in front of it. A non-http(s) or invalid baseURL
// returns the body unchanged.
export function withBaseHref(body, baseURL) {
  const text = String(body == null ? '' : body);
  const flow = httpURL(String(baseURL || ''));
  if (!flow || !/^https?:\/\//i.test(String(baseURL).trim())) return text;
  // Mask comments (same length) so tags inside them are never matched.
  const masked = text.replace(/<!--[\s\S]*?(?:-->|$)/g, (m) => ' '.repeat(m.length));
  let href = flow.href;
  const pageBase = /<base\b[^>]*>/i.exec(masked);
  const pageHref = pageBase && /\shref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))/i.exec(pageBase[0]);
  if (pageHref) {
    const value = (pageHref[1] ?? pageHref[2] ?? pageHref[3] ?? '').trim();
    if (/^https?:\/\//i.test(value) && httpURL(value)) return text;
    const resolved = httpURL(value, flow.href);
    if (resolved) href = resolved.href;
  }
  const tag = `<base href="${attrEscape(href)}">`;
  for (const re of [/<head(?:\s[^>]*)?>/i, /<html(?:\s[^>]*)?>/i, /<!doctype[^>]*>/i]) {
    const m = re.exec(masked);
    if (m) return text.slice(0, m.index + m[0].length) + tag + text.slice(m.index + m[0].length);
  }
  return tag + text;
}
