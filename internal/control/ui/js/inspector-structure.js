// inspector-structure.js — pure parsers behind the flow inspector's structured
// sub-tabs (#104): a header grid with cookie parsing, a collapsible JSON tree with
// JSONPath, query/form parameter tables, and GraphQL detection. No DOM and no
// imports, so it runs under node and every parser is unit-tested directly.

// splitHeadBody(raw) splits a reconstructed HTTP message into {head, body} at the
// first blank line (CRLF or LF). A message with no blank line is treated as all head.
export function splitHeadBody(raw) {
  const s = String(raw == null ? '' : raw);
  let i = s.indexOf('\r\n\r\n');
  let sep = 4;
  if (i < 0) { i = s.indexOf('\n\n'); sep = 2; }
  if (i < 0) return { head: s, body: '' };
  return { head: s.slice(0, i), body: s.slice(i + sep) };
}

// headerRows(headersObj) turns the detail DTO's {name:[values]} map into ordered
// {name, value} rows, one row per value, preserving duplicates and order.
export function headerRows(headersObj) {
  const out = [];
  if (!headersObj || typeof headersObj !== 'object') return out;
  for (const name of Object.keys(headersObj)) {
    const vals = headersObj[name];
    if (Array.isArray(vals)) { for (const v of vals) out.push({ name, value: String(v) }); }
    else if (vals != null) out.push({ name, value: String(vals) });
  }
  return out;
}

// filterHeaderRows(rows, term) keeps rows whose name or value contains term
// (case-insensitive). An empty term keeps everything.
export function filterHeaderRows(rows, term) {
  const t = String(term == null ? '' : term).trim().toLowerCase();
  if (!t) return rows.slice();
  return rows.filter((r) => r.name.toLowerCase().includes(t) || r.value.toLowerCase().includes(t));
}

// parseCookies(value) parses a request Cookie header ("a=1; b=2") into ordered
// {name, value} pairs. Bare tokens (no '=') keep an empty value.
export function parseCookies(value) {
  const out = [];
  for (const part of String(value == null ? '' : value).split(';')) {
    const p = part.trim();
    if (!p) continue;
    const eq = p.indexOf('=');
    if (eq < 0) out.push({ name: p, value: '' });
    else out.push({ name: p.slice(0, eq).trim(), value: p.slice(eq + 1).trim() });
  }
  return out;
}

// parseSetCookie(value) parses one Set-Cookie value into {name, value, attrs} where
// attrs is the ordered list of {k, v} directives (Path, HttpOnly, SameSite, …).
export function parseSetCookie(value) {
  const parts = String(value == null ? '' : value).split(';');
  const first = (parts.shift() || '').trim();
  const eq = first.indexOf('=');
  const out = { name: eq < 0 ? first : first.slice(0, eq).trim(), value: eq < 0 ? '' : first.slice(eq + 1).trim(), attrs: [] };
  for (const part of parts) {
    const p = part.trim();
    if (!p) continue;
    const e = p.indexOf('=');
    out.attrs.push(e < 0 ? { k: p, v: '' } : { k: p.slice(0, e).trim(), v: p.slice(e + 1).trim() });
  }
  return out;
}

// queryOf(path) returns the query string (after the first '?') of a URL or path.
export function queryOf(path) {
  const s = String(path == null ? '' : path);
  const q = s.indexOf('?');
  return q >= 0 ? s.slice(q + 1) : '';
}

function urlDecode(s) { try { return decodeURIComponent(String(s).replace(/\+/g, ' ')); } catch (e) { return String(s); } }

// parseFormEncoded(str) parses an application/x-www-form-urlencoded string (also a
// query string) into ordered url-decoded {key, value} pairs, duplicates kept.
export function parseFormEncoded(str) {
  const out = [];
  const s = String(str == null ? '' : str);
  if (!s) return out;
  for (const pair of s.split('&')) {
    if (!pair) continue;
    const eq = pair.indexOf('=');
    out.push({ key: urlDecode(eq < 0 ? pair : pair.slice(0, eq)), value: eq < 0 ? '' : urlDecode(pair.slice(eq + 1)) });
  }
  return out;
}

// isFormUrlEncoded / isMultipart classify a request body by its content type.
export function isFormUrlEncoded(contentType) { return /application\/x-www-form-urlencoded/i.test(String(contentType || '')); }
export function multipartBoundary(contentType) {
  const m = /boundary=("?)([^";]+)\1/i.exec(String(contentType || ''));
  return m ? m[2] : '';
}
// parseMultipart(body, boundary) returns ordered {name, filename, contentType, value}
// parts from a multipart/form-data body. Best-effort over the reconstructed text.
export function parseMultipart(body, boundary) {
  const out = [];
  if (!boundary) return out;
  const marker = '--' + boundary;
  const chunks = String(body == null ? '' : body).split(marker);
  for (const chunk of chunks) {
    const c = chunk.replace(/^\r\n/, '');
    if (!c || c === '--' || c === '--\r\n') continue;
    const sep = c.indexOf('\r\n\r\n') >= 0 ? c.indexOf('\r\n\r\n') : c.indexOf('\n\n');
    if (sep < 0) continue;
    const head = c.slice(0, sep);
    const value = c.slice(sep + (c.indexOf('\r\n\r\n') >= 0 ? 4 : 2)).replace(/\r?\n--\s*$/, '').replace(/\r?\n$/, '');
    const disp = /content-disposition:[^\n]*/i.exec(head);
    if (!disp) continue;
    const name = /name="([^"]*)"/i.exec(disp[0]);
    const filename = /filename="([^"]*)"/i.exec(disp[0]);
    const ctype = /content-type:\s*([^\r\n]+)/i.exec(head);
    out.push({ name: name ? name[1] : '', filename: filename ? filename[1] : '', contentType: ctype ? ctype[1].trim() : '', value });
  }
  return out;
}

function jsonType(v) {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v;
}
// jsonPathAppend(base, key) extends a JSONPath, using dot notation for simple
// identifier keys and bracket+quote notation otherwise.
export function jsonPathAppend(base, key) {
  return /^[A-Za-z_$][\w$]*$/.test(String(key)) ? base + '.' + key : base + '[' + JSON.stringify(String(key)) + ']';
}

// flattenJson(value) walks a parsed JSON value in document order and returns
// {rows, truncated}. Each row has: depth, key (label), path (JSONPath rooted at
// '$'), type ('object'|'array'|'string'|'number'|'boolean'|'null'), container
// (bool), and for containers a size + preview, for leaves a value. The caller
// renders these rows and folds subtrees by depth.
export function flattenJson(value, { maxNodes = 5000 } = {}) {
  const rows = [];
  let truncated = false;
  function walk(val, depth, keyLabel, path) {
    if (rows.length >= maxNodes) { truncated = true; return; }
    const type = jsonType(val);
    const container = type === 'object' || type === 'array';
    const row = { depth, key: keyLabel, path, type, container };
    if (container) {
      const keys = type === 'array' ? val.map((_, i) => i) : Object.keys(val);
      row.size = keys.length;
      row.preview = type === 'array' ? '[' + keys.length + ']' : '{' + keys.length + '}';
      rows.push(row);
      for (const k of keys) {
        const childPath = type === 'array' ? path + '[' + k + ']' : jsonPathAppend(path, k);
        walk(val[k], depth + 1, String(k), childPath);
        if (truncated) return;
      }
    } else {
      row.value = val;
      rows.push(row);
    }
  }
  walk(value, 0, '$', '$');
  return { rows, truncated };
}

// subtreeVisibility(rows, at, expand) → the hidden flags for the rows AFTER
// `at` in a flat, depth-annotated row list (the inspector's JSON tree), so the
// tree's fold buttons can be a pure function. rows[i] is {depth, container,
// expanded}. Collapsing hides every descendant. Expanding reveals them again
// EXCEPT under a container inside the subtree that is itself still folded — its
// own aria-expanded=false has to keep carrying over, or a parent's expand would
// silently unfold everything the user had closed below it. The array stops at
// the first row that is not a descendant of rows[at]; the caller applies
// flags[i] to rows[at + 1 + i] and leaves every other row alone.
export function subtreeVisibility(rows, at, expand) {
  const list = Array.isArray(rows) ? rows : [];
  const base = list[at];
  if (!base) return [];
  const baseDepth = Number(base.depth) || 0;
  const out = [];
  const folded = []; // depths of the folded containers inside the toggled subtree
  for (let i = at + 1; i < list.length; i++) {
    const row = list[i] || {};
    const depth = Number(row.depth) || 0;
    if (!(depth > baseDepth)) break;
    while (folded.length && depth <= folded[folded.length - 1]) folded.pop();
    if (!expand || folded.length) { out.push(true); continue; }
    out.push(false);
    if (row.container && row.expanded === false) folded.push(depth);
  }
  return out;
}

// tryParseJson(text) returns {ok, value} without throwing.
export function tryParseJson(text) {
  const clean = String(text == null ? '' : text).replace(/^\uFEFF/, '');
  try { return { ok: true, value: JSON.parse(clean) }; }
  catch (e) { return { ok: false, error: e.message }; }
}

// isJsonInspectorBody(body, contentType) reports whether the JSON inspector
// view can meaningfully display the message body. Objects and arrays always
// qualify; JSON primitives (strings, numbers, booleans, null) qualify only when
// the Content-Type declares JSON so plain-text numbers are not mislabelled.
export function isJsonInspectorBody(body, contentType) {
  const clean = String(body == null ? '' : body).replace(/^\uFEFF/, '').trim();
  if (!clean) return false;
  const parsed = tryParseJson(clean);
  if (!parsed.ok) return false;
  if (parsed.value !== null && typeof parsed.value === 'object') return true;
  return /json/i.test(String(contentType || ''));
}

// detectGraphQL(body, contentType) returns {isGraphQL, query, variables,
// operationName, batched}. A GraphQL POST is JSON (or a batch array) carrying a
// string `query`; application/graphql sends the query as the raw body.
export function detectGraphQL(body, contentType) {
  const ct = String(contentType || '').toLowerCase();
  const text = String(body == null ? '' : body).replace(/^\uFEFF/, '');
  if (ct && ct.indexOf('json') < 0 && ct.indexOf('graphql') < 0) return { isGraphQL: false };
  let parsed;
  try { parsed = JSON.parse(text); }
  catch (e) {
    if ((ct.indexOf('graphql') >= 0 || !ct) && /\b(query|mutation|subscription)\b|^\s*\{/.test(text)) {
      return { isGraphQL: true, query: text, variables: '', operationName: '', batched: false };
    }
    return { isGraphQL: false };
  }
  const op = Array.isArray(parsed) ? parsed[0] : parsed;
  if (!op || typeof op !== 'object' || typeof op.query !== 'string') return { isGraphQL: false };
  return {
    isGraphQL: true,
    query: op.query,
    variables: op.variables != null ? JSON.stringify(op.variables, null, 2) : '',
    operationName: typeof op.operationName === 'string' ? op.operationName : '',
    batched: Array.isArray(parsed),
  };
}

// isDecodedAvailable(d) reports whether /api/flows/{id}/decoded yielded a
// matched project codec with non-empty decoded content, fields, note, or error.
export function isDecodedAvailable(d) {
  if (!d || !d.matched) return false;
  if (typeof d.plaintext === 'string' && d.plaintext.length > 0) return true;
  if (Array.isArray(d.fields) && d.fields.length > 0) return true;
  if (d.fields && typeof d.fields === 'object' && !Array.isArray(d.fields) && Object.keys(d.fields).length > 0) return true;
  if (typeof d.note === 'string' && d.note.length > 0) return true;
  if (typeof d.error === 'string' && d.error.length > 0) return true;
  return false;
}

const REQ_INSPECTOR_VIEWS = ['raw', 'pretty', 'headers', 'json', 'params', 'gql', 'decoded', 'hex'];
const RES_INSPECTOR_VIEWS = ['raw', 'pretty', 'headers', 'json', 'decoded', 'hex', 'render'];
const FALLBACK_VIEW_ORDER = ['raw', 'headers', 'json', 'params', 'gql', 'decoded', 'hex', 'render'];

// inspectorViewAvailability computes per-view visibility and pending-probe flags
// for #inspect's request or response segmented control from known flow detail
// plus optional raw message / codec probe results.
export function inspectorViewAvailability({
  side = 'req',
  detail = null,
  raw = null,
  mime = '',
  binary = false,
  renderCap = 0,
  decodedMatched = null,
  hasMessage = true,
} = {}) {
  const views = side === 'res' ? RES_INSPECTOR_VIEWS : REQ_INSPECTOR_VIEWS;
  const visible = Object.fromEntries(views.map((v) => [v, false]));
  const pending = Object.fromEntries(views.map((v) => [v, false]));
  if (!hasMessage || !detail) return { visible, pending, anyVisible: false };

  const ct = String(mime || '').toLowerCase();
  const len = side === 'req' ? detail.reqLen : detail.resLen;
  const hasLen = typeof len === 'number' && Number.isFinite(len);
  const bodyDefinitelyEmpty = hasLen && len <= 0;
  const overCap = typeof renderCap === 'number' && renderCap > 0 && hasLen && len > renderCap;
  const rawKnown = typeof raw === 'string';
  const bodyText = rawKnown ? splitHeadBody(raw).body : '';
  const hasBodyText = rawKnown ? bodyText.length > 0 : !bodyDefinitelyEmpty;
  const hasRawText = rawKnown ? raw.length > 0 : true;

  visible.raw = hasRawText;
  visible.pretty = hasRawText;
  visible.hex = hasRawText;
  visible.headers = headerRows(side === 'req' ? detail.reqHeaders : detail.resHeaders).length > 0;

  if (decodedMatched === true) visible.decoded = true;
  else if (decodedMatched === false) visible.decoded = false;
  else pending.decoded = true;

  if (!binary && hasBodyText) {
    if (rawKnown) {
      visible.json = isJsonInspectorBody(bodyText, ct);
    } else if (overCap) {
      visible.json = ct.indexOf('json') >= 0;
    } else {
      visible.json = ct.indexOf('json') >= 0;
      pending.json = !ct || ((ct.indexOf('json') >= 0 || ct.indexOf('text/plain') >= 0) && ct.indexOf('jsonp') < 0);
    }
  }

  if (side === 'req') {
    const hasQuery = parseFormEncoded(queryOf(detail.path || '')).length > 0;
    const isForm = isFormUrlEncoded(ct);
    const boundary = multipartBoundary(ct);
    const bodyCouldHaveForm = !binary && hasBodyText && (isForm || !!boundary);
    if (hasQuery) {
      visible.params = true;
    } else if (bodyCouldHaveForm) {
      if (rawKnown) {
        visible.params = isForm
          ? parseFormEncoded(bodyText).length > 0
          : parseMultipart(bodyText, boundary).length > 0;
      } else {
        visible.params = true;
        pending.params = !overCap;
      }
    }

    if (!binary && hasBodyText) {
      if (rawKnown) {
        visible.gql = detectGraphQL(bodyText, ct).isGraphQL;
      } else if (overCap) {
        visible.gql = ct.indexOf('graphql') >= 0;
      } else {
        visible.gql = ct.indexOf('graphql') >= 0;
        pending.gql = !ct || ct.indexOf('json') >= 0 || ct.indexOf('graphql') >= 0;
      }
    }
  } else {
    const isHtml = !!ct && ct.indexOf('html') >= 0;
    if (isHtml && !binary && !bodyDefinitelyEmpty) {
      visible.render = rawKnown ? bodyText.trim().length > 0 : true;
    }
  }

  return { visible, pending, anyVisible: views.some((v) => visible[v]) };
}

// needsBackgroundBodyProbe returns true when a body-less view (Headers/Decoded)
// needs a background raw fetch to determine whether JSON, Params, or GraphQL
// applies without downloading large HTML/CSS/JS/binary payloads unnecessarily.
export function needsBackgroundBodyProbe({ side, detail, mime, binary, renderCap, rawKnown } = {}) {
  if (rawKnown || binary || !detail) return false;
  const len = side === 'req' ? detail.reqLen : detail.resLen;
  if (typeof len !== 'number' || !Number.isFinite(len) || len <= 0) return false;
  if (typeof renderCap === 'number' && renderCap > 0 && len > renderCap) return false;
  const avail = inspectorViewAvailability({ side, detail, raw: null, mime, binary, renderCap, decodedMatched: false, hasMessage: true });
  return Boolean(avail.pending.json || avail.pending.params || avail.pending.gql);
}

// fallbackInspectorView picks 'pretty' when visible, or the first visible view.
export function fallbackInspectorView(availOrVisible) {
  const visible = availOrVisible && availOrVisible.visible ? availOrVisible.visible : availOrVisible;
  if (visible && visible.pretty) return 'pretty';
  for (const v of FALLBACK_VIEW_ORDER) {
    if (visible && visible[v]) return v;
  }
  return 'pretty';
}
