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

// tryParseJson(text) returns {ok, value} without throwing.
export function tryParseJson(text) {
  try { return { ok: true, value: JSON.parse(String(text == null ? '' : text)) }; }
  catch (e) { return { ok: false, error: e.message }; }
}

// detectGraphQL(body, contentType) returns {isGraphQL, query, variables,
// operationName, batched}. A GraphQL POST is JSON (or a batch array) carrying a
// string `query`; application/graphql sends the query as the raw body.
export function detectGraphQL(body, contentType) {
  const ct = String(contentType || '').toLowerCase();
  const text = String(body == null ? '' : body);
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
