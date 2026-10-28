// flow-focus-model.js — pure helpers for focused flow images ("image around
// text"): turning a selection or a typed value into a focus request, checking
// line ranges before the server does, and reading the focus outcome headers
// the preview endpoint returns. No DOM, no imports; tested under node --test.

export const FOCUS_MAX = 200;
export const CONTEXT_DEFAULT = 6;
export const CONTEXT_MAX = 40;

// selectionToFocus: the first non-empty line of a selection, trimmed and capped
// at 200 characters (the server matches one line at a time).
export function selectionToFocus(text) {
  const line = String(text == null ? '' : text).split(/\r?\n|\r/).map((l) => l.trim()).find(Boolean) || '';
  return Array.from(line).slice(0, FOCUS_MAX).join('');
}

// parseFocusInput reads the Focus field: "/pattern/" is a regex, anything else
// a literal; an explicit regex toggle wins for unslashed text. literal (text
// that came from a selection, not typed) skips the /pattern/ form, so a
// selected path like "/files/a.b+c/" is matched as written.
export function parseFocusInput(value, regexToggle, literal) {
  const v = String(value == null ? '' : value).trim();
  if (!v) return { focus: '', regex: false };
  const m = literal ? null : v.match(/^\/(.+)\/$/s);
  if (m) return { focus: m[1].slice(0, FOCUS_MAX), regex: true };
  return { focus: Array.from(v).slice(0, FOCUS_MAX).join(''), regex: !!regexToggle };
}

// checkRegex returns '' when the pattern compiles in JS (close to RE2 for the
// common subset), else a short reason. The server stays authoritative.
export function checkRegex(pattern) {
  try { new RegExp(pattern, 'i'); return ''; } catch (e) { return 'invalid regex: ' + ((e && e.message) || 'syntax error'); }
}

// normalizeLines validates "res:400-440, req:1-5, 12" like the server and
// returns {value, error}; value is the cleaned comma-separated form.
export function normalizeLines(input) {
  const raw = String(input == null ? '' : input).trim();
  if (!raw) return { value: '', error: '' };
  const out = [];
  for (const part of raw.split(',').map((p) => p.trim()).filter(Boolean)) {
    const m = part.match(/^(?:(req|request|res|resp|response|both)\s*:\s*)?(\d+)(?:\s*-\s*(\d+))?$/i);
    if (!m) return { value: '', error: 'use ranges like res:400-440' };
    const from = Number(m[2]);
    const to = m[3] ? Number(m[3]) : from;
    if (from < 1 || to < from) return { value: '', error: 'ranges are 1-based with from ≤ to' };
    const side = !m[1] ? '' : /^req/i.test(m[1]) ? 'req:' : /^both$/i.test(m[1]) ? '' : 'res:';
    out.push(side + from + (to !== from ? '-' + to : ''));
  }
  if (out.length > 8) return { value: '', error: 'at most 8 ranges' };
  return { value: out.join(','), error: '' };
}

// clampContext keeps context lines in 0..40, falling back to the default.
export function clampContext(v) {
  const n = Number(v);
  if (v === '' || v == null || !Number.isFinite(n)) return CONTEXT_DEFAULT;
  return Math.min(CONTEXT_MAX, Math.max(0, Math.round(n)));
}

const decode = (s) => { try { return decodeURIComponent(s); } catch (e) { return s; } };

// readFocusHeaders turns X-Interseptor-Focus-* response headers into
// {mode, matches: {req, res}, notFound: [], masked, notShown, bodyTruncated}
// (null when absent). notShown / bodyTruncated are '', 'req', 'res' or 'both'.
export function readFocusHeaders(get) {
  const matches = get('X-Interseptor-Focus-Matches');
  if (!matches) return null;
  const counts = {};
  for (const part of matches.split(',')) {
    const [k, v] = part.split('=');
    if (k === 'req' || k === 'res') counts[k] = Number(v) || 0;
  }
  const nf = get('X-Interseptor-Focus-Notfound');
  return {
    mode: get('X-Interseptor-Focus-Mode') || '',
    matches: counts,
    notFound: nf ? nf.split(',').map(decode) : [],
    masked: get('X-Interseptor-Focus-Masked') === '1',
    notShown: get('X-Interseptor-Focus-Notshown') || '',
    bodyTruncated: get('X-Interseptor-Body-Truncated') || '',
  };
}

// focusResultText is the one-line outcome shown after a render.
export function focusResultText(r) {
  if (!r) return '';
  const sides = Object.entries(r.matches).map(([k, n]) => (k === 'req' ? 'request ' : 'response ') + n);
  const total = Object.values(r.matches).reduce((a, b) => a + b, 0);
  const add = (s) => { text += (text ? ' ' : '') + s; };
  let text = '';
  if (total && r.notShown) add('Found ' + total + (total === 1 ? ' match' : ' matches') + ' (' + sides.join(', ') + '), but they did not fit in the image: narrow it with lines.');
  else if (total) add('Highlighted ' + total + (total === 1 ? ' match' : ' matches') + ' (' + sides.join(', ') + ').');
  // A masked-only focus text is never listed: the server leaves it out.
  if (r.masked) add('A focus text matched only a masked header value (hidden).');
  if (r.notFound.length) add('Not found: ' + r.notFound.map((s) => '“' + s + '”').join(', ') + '.');
  if ((r.notFound.length || r.masked) && r.mode === 'top') add('Showing the first lines instead.');
  if (r.bodyTruncated && r.notFound.length) add('The body was cut at 64 KiB for the image; the text may lie beyond it.');
  return text;
}

// decodedBodyText explains the X-Interseptor-Decoded-Body header ("req=form,
// res=base64"); empty when nothing was decoded.
export function decodedBodyText(header) {
  const parts = String(header || '').split(',').map((p) => p.trim()).filter(Boolean).map((p) => {
    const [k, v] = p.split('=');
    return (k === 'req' ? 'request ' : 'response ') + (v || '');
  });
  return parts.length ? 'Decoded body: ' + parts.join(', ') + '. Choose Raw to see the original bytes.' : '';
}

// attachErrorText is the toast for a failed attach. A 422 means the focus
// matched nothing drawable and nothing was stored.
export function attachErrorText(e) {
  const msg = (e && e.message) || 'request failed';
  if (e && e.status === 422) return 'Not attached: ' + msg + '. Change the Focus text or lines, or clear them to attach the first lines.';
  return msg;
}

// focusOptions assembles the options flowPreviewPngURL and the attach body take.
export function focusOptions({ text, regexToggle, literal, side, context, lines, caption, decode } = {}) {
  const f = parseFocusInput(text, regexToggle, literal);
  const l = normalizeLines(lines);
  const err = f.regex && f.focus ? checkRegex(f.focus) : '';
  return {
    error: err || l.error,
    opts: {
      focus: f.focus ? [f.focus] : [],
      regex: f.regex,
      focusSide: side === 'req' || side === 'res' ? side : 'both',
      lines: l.value,
      context: clampContext(context),
      caption: String(caption || '').trim().slice(0, 120),
      decode: String(decode || '').trim().toLowerCase() === 'raw' ? 'raw' : 'auto',
    },
  };
}

// focusFormValues carries the literal flag of a selection-seeded Focus field
// forward only while its text is unchanged; once edited, "/x/" means regex.
export function focusFormValues(values, seed) {
  const s = seed || {};
  const v = values || {};
  return { ...v, literal: !!(s.literal && String(v.text == null ? '' : v.text) === String(s.text == null ? '' : s.text)) };
}

// focusAttachBody is the POST /api/findings/{id}/flow-preview body.
export function focusAttachBody(flowId, o, extra = {}) {
  const body = { flowId: Number(flowId), side: o.side || 'both', theme: 'light', role: 'result', ...extra };
  if (o.focus && o.focus.length) body.focus = o.focus;
  if (o.regex && o.focus && o.focus.length) body.regex = true;
  if (o.focusSide && o.focusSide !== 'both') body.focusSide = o.focusSide;
  if (o.lines) body.lines = o.lines;
  if (o.context !== undefined) body.context = o.context;
  if (o.caption) body.caption = o.caption;
  if (o.decode === 'raw') body.decode = 'raw';
  return body;
}
