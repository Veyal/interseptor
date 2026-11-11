// copyas.js — "Copy as" generators for captured flows: curl (bash and
// PowerShell), fetch, Python requests, Go net/http, httpie, raw HTTP, HAR entry
// and bundle, URL, headers only, body only, a markdown evidence block and an
// Interseptor repro document.
//
// buildCopyAs() is pure and synchronous (it runs under `node --test`);
// copyAs() resolves flows lazily: bodies are fetched only for kinds that need
// them, streamed and cut at 1 MB (never buffered whole in the UI), and core.js
// is imported on demand so this module has no load-time DOM dependency.
//
// Flow shape: {id, method, scheme, host, port, path, httpVersion, status,
// reqHeaders:{Name:[value]}, resHeaders, reqBody, resBody, mime, startedAt,
// durationMs, reqBodyTruncated, resBodyTruncated, reqBodyBytes, resBodyBytes}.
// reqBodyBytes / resBodyBytes (a Uint8Array) are set only when that body is not
// UTF-8 text; reqBody / resBody then hold a lossy decode for display, and every
// generator that produces something runnable sends the bytes instead.

export const MAX_BODY_BYTES = 1024 * 1024;
const SENSITIVE = new Set(['authorization', 'proxy-authorization', 'cookie', 'set-cookie']);

// bodies: which sides a kind needs; redact: default redaction for the kind.
export const COPY_AS_KINDS = [
  { kind: 'curl', label: 'curl (bash)', key: 'c', bodies: ['req'], redact: false },
  { kind: 'curl-ps', label: 'curl (PowerShell)', key: 'w', bodies: ['req'], redact: false },
  { kind: 'fetch', label: 'fetch', key: 'f', bodies: ['req'], redact: false },
  { kind: 'python', label: 'Python requests', key: 'p', bodies: ['req'], redact: false },
  { kind: 'go', label: 'Go net/http', key: 'g', bodies: ['req'], redact: false },
  { kind: 'httpie', label: 'httpie', key: 'i', bodies: ['req'], redact: false },
  { kind: 'raw', label: 'Raw HTTP', key: 'r', bodies: ['req'], redact: false },
  { kind: 'har', label: 'HAR entry', key: 'h', bodies: ['req', 'res'], redact: false },
  { kind: 'har-bundle', label: 'HAR bundle', key: 'b', bodies: ['req', 'res'], redact: false },
  { kind: 'url', label: 'URL', key: 'u', bodies: [], redact: false },
  { kind: 'headers', label: 'Headers only', key: 'e', bodies: [], redact: false },
  { kind: 'body', label: 'Body only', key: 'o', bodies: ['req'], redact: false },
  { kind: 'markdown', label: 'Markdown evidence block', key: 'm', bodies: ['req', 'res'], redact: true },
  { kind: 'repro', label: 'Interseptor repro', key: 'x', bodies: ['req'], redact: false },
];
const KIND_BY_NAME = Object.fromEntries(COPY_AS_KINDS.map((k) => [k.kind, k]));

// urlHost brackets an IPv6 literal. Captured flows store net/url's Hostname()
// ("::1", no brackets), so a raw concatenation built http://::1:8080/…, which
// curl and every other client parse as an invalid authority.
export function urlHost(host) {
  const h = String(host || '');
  // More than one colon means an IPv6 literal, not host:port.
  return h.includes(':') && !h.startsWith('[') && h.split(':').length > 2 ? '[' + h + ']' : h;
}

// flowURL is the flow's absolute URL. `scheme` overrides the transport scheme
// (ws / wss for a WebSocket flow) while the default-port rule keeps using the
// flow's own http scheme, so ws://…:443 and ws://…:80 read as written.
export function flowURL(f, { scheme = '' } = {}) {
  const s = scheme || f.scheme || 'http';
  const transport = s === 'wss' ? 'https' : s === 'ws' ? 'http' : s;
  const port = Number(f.port) || 0;
  const def = (transport === 'https' && port === 443) || (transport === 'http' && port === 80);
  return `${s}://${urlHost(f.host)}${port && !def ? ':' + port : ''}${f.path || '/'}`;
}

// truncateBody cuts `text` at `limit` UTF-8 *bytes* — the unit MAX_BODY_BYTES and
// streamBody's reader already use — and never splits a surrogate pair. Counting
// UTF-16 code units instead let a multibyte body through at up to three times the
// stated cap, and a cut inside an astral character left a lone surrogate in the
// copied text (curl/python/JSON all render that as U+FFFD).
export function truncateBody(text, limit = MAX_BODY_BYTES) {
  const s = String(text == null ? '' : text);
  const n = Number(limit);
  const max = n > 0 ? Math.floor(n) : 0;
  let bytes = 0;
  for (let i = 0; i < s.length;) {
    const cp = s.codePointAt(i);
    const width = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
    if (bytes + width > max) return { text: s.slice(0, i), truncated: true };
    bytes += width;
    i += cp > 0xffff ? 2 : 1;
  }
  return { text: s, truncated: false };
}

// decodeBody turns captured body bytes into {text} when they are UTF-8 text and
// {text, bytes} when they are not. Decoding binary as text replaces every
// invalid byte with U+FFFD (three different bytes when re-encoded), and a NUL
// cannot be carried in a shell argument, so such a body must travel as bytes.
// ignoreBOM keeps a leading EF BB BF, which TextDecoder drops by default.
// `truncated` says the bytes were cut at a size limit: a multibyte character
// split by that cut is dropped rather than making the whole body "binary".
export function decodeBody(bytes, truncated = false) {
  const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes || 0);
  try {
    const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(u8, { stream: !!truncated });
    if (!text.includes('\u0000')) return { text };
  } catch (e) { /* not UTF-8 */ }
  return { text: new TextDecoder('utf-8', { ignoreBOM: true }).decode(u8), bytes: u8 };
}
const bodyBytes = (f, side = 'req') => {
  const b = f[side + 'BodyBytes'];
  return b instanceof Uint8Array && b.length ? b : null;
};
const utf8Length = (s) => new TextEncoder().encode(s).length;
function base64(u8) {
  let bin = '';
  for (let i = 0; i < u8.length; i += 0x8000) bin += String.fromCharCode.apply(null, u8.subarray(i, i + 0x8000));
  return btoa(bin);
}
// byteLiteral writes bytes inside a double-quoted literal that Python (b"…") and
// Go ("…") both read byte for byte: printable ASCII as is, the rest as \xNN.
function byteLiteral(u8) {
  let out = '';
  for (const b of u8) {
    if (b === 0x22 || b === 0x5c) out += '\\' + String.fromCharCode(b);
    else if (b >= 0x20 && b < 0x7f) out += String.fromCharCode(b);
    else out += '\\x' + b.toString(16).padStart(2, '0');
  }
  return `"${out}"`;
}

export function redactHeaders(headers) {
  const out = {};
  for (const [k, v] of Object.entries(headers || {})) out[k] = SENSITIVE.has(k.toLowerCase()) ? (Array.isArray(v) ? v : [v]).map(() => '<redacted>') : v;
  return out;
}

const headerPairs = (headers, { skip = [] } = {}) => {
  const drop = new Set(skip.map((s) => s.toLowerCase()));
  const out = [];
  for (const [k, v] of Object.entries(headers || {})) {
    if (drop.has(k.toLowerCase())) continue;
    for (const val of Array.isArray(v) ? v : [v]) out.push([k, String(val)]);
  }
  return out;
};
const reqPairs = (f, redact) => headerPairs(redact ? redactHeaders(f.reqHeaders) : f.reqHeaders, { skip: ['content-length', 'host'] });
const hasBody = (f) => !!bodyBytes(f) || (typeof f.reqBody === 'string' && f.reqBody.length > 0);
const method = (f) => (f.method || 'GET').toUpperCase();
const sq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`; // POSIX single-quote
const psq = (s) => `'${String(s).replace(/'/g, "''")}'`; // PowerShell single-quote
const jq = (s) => JSON.stringify(String(s));
// A method is any RFC 9110 token, and a token may hold ` $ & | ' ! * and more: a
// captured "X`id`" pasted unquoted would run in the operator's shell. Plain
// methods stay bare so the usual command reads as before.
const shellMethod = (f, q) => (/^[A-Za-z0-9_-]+$/.test(method(f)) ? method(f) : q(method(f)));
const TRUNC = 'body truncated at 1 MB';
// A binary body is piped in, decoded from base64: nothing else survives a shell
// argument (a NUL ends it) or the clipboard (invalid UTF-8 is replaced).
const b64Pipe = (u8) => `printf %s ${sq(base64(u8))} | base64 --decode |\n  `;

function curl(f, redact, ps) {
  const q = ps ? psq : sq;
  const bare = method(f) === 'GET' && !hasBody(f);
  const bin = bodyBytes(f);
  const parts = [`${ps ? 'curl.exe' : 'curl'} ${bare ? '' : `-X ${shellMethod(f, q)} `}${q(flowURL(f))}`];
  for (const [k, v] of reqPairs(f, redact)) parts.push(`-H ${q(`${k}: ${v}`)}`);
  // PowerShell re-encodes bytes piped to a native command, so they go through a file.
  if (bin) parts.push(ps ? '--data-binary "@$bodyFile"' : '--data-binary @-');
  else if (hasBody(f)) parts.push(`--data-raw ${q(f.reqBody)}`);
  let out = parts.join(ps ? ' `\n  ' : ' \\\n  ');
  if (bin && ps) out = `$bodyFile = [IO.Path]::GetTempFileName()\n[IO.File]::WriteAllBytes($bodyFile, [Convert]::FromBase64String(${psq(base64(bin))}))\n${out}\nRemove-Item $bodyFile`;
  else if (bin) out = b64Pipe(bin) + out;
  if (f.reqBodyTruncated) out += `\n# ${TRUNC}`;
  return out;
}
function fetchSnippet(f, redact) {
  const lines = [`fetch(${jq(flowURL(f))}, {`, `  method: ${jq(method(f))},`];
  const pairs = reqPairs(f, redact);
  if (pairs.length) { lines.push('  headers: {'); pairs.forEach(([k, v]) => lines.push(`    ${jq(k)}: ${jq(v)},`)); lines.push('  },'); }
  const bin = bodyBytes(f);
  if (bin) lines.push(`  body: Uint8Array.from(atob(${jq(base64(bin))}), (c) => c.charCodeAt(0)),`);
  else if (hasBody(f)) lines.push(`  body: ${jq(f.reqBody)},`);
  lines.push('});');
  if (f.reqBodyTruncated) lines.push(`// ${TRUNC}`);
  return lines.join('\n');
}
function python(f, redact) {
  const lines = ['import requests', '', 'response = requests.request(', `    ${jq(method(f))},`, `    ${jq(flowURL(f))},`];
  const pairs = reqPairs(f, redact);
  if (pairs.length) { lines.push('    headers={'); pairs.forEach(([k, v]) => lines.push(`        ${jq(k)}: ${jq(v)},`)); lines.push('    },'); }
  const bin = bodyBytes(f);
  if (bin) lines.push(`    data=b${byteLiteral(bin)},`);
  else if (hasBody(f)) lines.push(`    data=${jq(f.reqBody)}.encode("utf-8"),`);
  lines.push(')', 'print(response.status_code, response.text)');
  if (f.reqBodyTruncated) lines.push(`# ${TRUNC}`);
  return lines.join('\n');
}
function goSnippet(f, redact) {
  const body = hasBody(f);
  const lines = ['package main', '', 'import (', '\t"fmt"', '\t"io"', '\t"net/http"', ...(body ? ['\t"strings"'] : []), ')', '', 'func main() {',
    `\treq, err := http.NewRequest(${jq(method(f))}, ${jq(flowURL(f))}, ${body ? `strings.NewReader(${bodyBytes(f) ? byteLiteral(bodyBytes(f)) : jq(f.reqBody)})` : 'nil'})`,
    '\tif err != nil {', '\t\tpanic(err)', '\t}'];
  // Set replaces, so a repeated header (two Cookie or X-Forwarded-For lines) kept
  // only its last value; net/http canonicalizes names, so compare without case.
  const seen = new Set();
  for (const [k, v] of reqPairs(f, redact)) {
    const name = k.toLowerCase();
    lines.push(`\treq.Header.${seen.has(name) ? 'Add' : 'Set'}(${jq(k)}, ${jq(v)})`);
    seen.add(name);
  }
  lines.push('\tresp, err := http.DefaultClient.Do(req)', '\tif err != nil {', '\t\tpanic(err)', '\t}', '\tdefer resp.Body.Close()',
    '\tdata, _ := io.ReadAll(resp.Body)', '\tfmt.Println(resp.StatusCode, string(data))', '}');
  if (f.reqBodyTruncated) lines.push(`// ${TRUNC}`);
  return lines.join('\n');
}
function httpie(f, redact) {
  const parts = [`http ${shellMethod(f, sq)} ${sq(flowURL(f))}`];
  for (const [k, v] of reqPairs(f, redact)) parts.push(sq(`${k}:${v}`));
  const bin = bodyBytes(f);
  if (!bin && hasBody(f)) parts.push(`--raw ${sq(f.reqBody)}`);
  // httpie sends redirected stdin as the raw request body.
  let out = (bin ? b64Pipe(bin) : '') + parts.join(' \\\n  ');
  if (f.reqBodyTruncated) out += `\n# ${TRUNC}`;
  return out;
}
function rawHTTP(f, redact) {
  const headers = headerPairs(redact ? redactHeaders(f.reqHeaders) : f.reqHeaders, { skip: ['host'] });
  // The default port depends on the scheme: https on :80 (or http on :443) must
  // keep its port, or the replayed Host names a different origin. A flow without
  // a scheme keeps the old rule (both well-known ports are dropped).
  const port = Number(f.port) || 0;
  const def = f.scheme === 'https' ? port === 443 : f.scheme === 'http' ? port === 80 : port === 80 || port === 443;
  let out = `${method(f)} ${f.path || '/'} ${f.httpVersion || 'HTTP/1.1'}\r\nHost: ${urlHost(f.host)}${port && !def ? ':' + port : ''}\r\n`;
  for (const [k, v] of headers) out += `${k}: ${v}\r\n`;
  out += '\r\n' + (hasBody(f) ? f.reqBody : '');
  return out;
}
const harHeaders = (h) => headerPairs(h).map(([name, value]) => ({ name, value }));
// A flow's start is `ts` (unix ms) in the API; `startedAt` is an ISO override.
function startedISO(f) {
  if (f.startedAt) return f.startedAt;
  const d = Number.isFinite(f.ts) && f.ts > 0 ? new Date(f.ts) : null;
  return d && !Number.isNaN(d.getTime()) ? d.toISOString() : '1970-01-01T00:00:00.000Z';
}
function harEntry(f, redact) {
  const url = flowURL(f);
  const q = [];
  try { new URL(url).searchParams.forEach((value, name) => q.push({ name, value })); } catch (e) { /* relative or odd URL: leave empty */ }
  const mime = (f.mime || '').split(';')[0];
  const reqType = headerPairs(f.reqHeaders).find(([k]) => k.toLowerCase() === 'content-type');
  const resText = typeof f.resBody === 'string' ? f.resBody : '';
  // HAR 1.2: a body that is not text is base64 with encoding:"base64" (the form
  // Interseptor's own HAR export writes and its importer reads); sizes are bytes.
  const reqBin = bodyBytes(f), resBin = bodyBytes(f, 'res');
  const resSize = resBin ? resBin.length : utf8Length(resText);
  const entry = {
    startedDateTime: startedISO(f),
    time: f.durationMs || 0,
    request: {
      method: method(f), url, httpVersion: f.httpVersion || 'HTTP/1.1', cookies: [],
      headers: harHeaders(redact ? redactHeaders(f.reqHeaders) : f.reqHeaders), queryString: q,
      headersSize: -1, bodySize: reqBin ? reqBin.length : hasBody(f) ? utf8Length(f.reqBody) : 0,
    },
    response: {
      status: f.status || 0, statusText: '', httpVersion: f.httpVersion || 'HTTP/1.1', cookies: [],
      headers: harHeaders(redact ? redactHeaders(f.resHeaders) : f.resHeaders),
      content: resBin ? { size: resSize, mimeType: mime, text: base64(resBin), encoding: 'base64' } : { size: resSize, mimeType: mime, text: resText },
      redirectURL: '', headersSize: -1, bodySize: resSize,
    },
    cache: {}, timings: { send: 0, wait: f.durationMs || 0, receive: 0 },
  };
  if (reqBin) entry.request.postData = { mimeType: reqType ? reqType[1] : '', text: base64(reqBin), encoding: 'base64' };
  else if (hasBody(f)) entry.request.postData = { mimeType: reqType ? reqType[1] : '', text: f.reqBody };
  return entry;
}
function fence(body) {
  let f = '```';
  while (body.includes(f)) f += '`';
  return f;
}
function markdown(f, redact) {
  const rq = redact ? redactHeaders(f.reqHeaders) : f.reqHeaders;
  const rs = redact ? redactHeaders(f.resHeaders) : f.resHeaders;
  const block = (head, headers, body) => {
    const text = [head, ...headerPairs(headers).map(([k, v]) => `${k}: ${v}`)].join('\n') + (body ? '\n\n' + body : '');
    const fc = fence(text);
    return `${fc}http\n${text}\n${fc}`;
  };
  const out = [`### ${method(f)} ${flowURL(f)}${f.status ? ` -> ${f.status}` : ''}`, '', '**Request**', '',
    block(`${method(f)} ${flowURL(f)}`, rq, hasBody(f) ? f.reqBody : '')];
  if (f.reqBodyTruncated) out.push('', `_Request ${TRUNC}._`);
  if (f.status) {
    out.push('', '**Response**', '', block(`HTTP ${f.status}`, rs, typeof f.resBody === 'string' ? f.resBody : ''));
    if (f.resBodyTruncated) out.push('', `_Response ${TRUNC}._`);
  }
  return out.join('\n');
}

// buildCopyAs(kind, flows, {redact, side}) -> {text, notices}
export function buildCopyAs(kind, flows, opts = {}) {
  const def = KIND_BY_NAME[kind];
  if (!def) throw new Error('unknown copy-as kind: ' + kind);
  const redact = opts.redact === undefined ? def.redact : !!opts.redact;
  const side = opts.side === 'res' ? 'res' : 'req';
  const notices = [];
  for (const f of flows) {
    if (f.reqBodyTruncated) notices.push(`Request body of flow ${f.id} truncated at 1 MB`);
    if (f.resBodyTruncated) notices.push(`Response body of flow ${f.id} truncated at 1 MB`);
  }
  // Formats that are text by nature cannot hold a binary body: say so instead of
  // handing over U+FFFD in place of the captured bytes without a word.
  const lossy = kind === 'raw' ? ['req'] : kind === 'markdown' ? ['req', 'res'] : kind === 'body' ? [side] : [];
  for (const f of flows) {
    for (const s of lossy) if (bodyBytes(f, s)) notices.push(`${s === 'res' ? 'Response' : 'Request'} body of flow ${f.id} is binary: bytes that are not UTF-8 text are shown as U+FFFD`);
  }
  const each = (fn, sep = '\n\n') => flows.map((f) => fn(f, redact)).join(sep);
  let text;
  switch (kind) {
    case 'curl': text = each((f, r) => curl(f, r, false)); break;
    case 'curl-ps': text = each((f, r) => curl(f, r, true)); break;
    case 'fetch': text = each(fetchSnippet); break;
    case 'python': text = each(python); break;
    case 'go': text = each(goSnippet); break;
    case 'httpie': text = each(httpie); break;
    case 'raw': text = each(rawHTTP, '\r\n\r\n'); break;
    case 'har': {
      const entries = flows.map((f) => harEntry(f, redact));
      text = JSON.stringify(entries.length === 1 ? entries[0] : entries, null, 2);
      break;
    }
    case 'har-bundle':
      text = JSON.stringify({ log: { version: '1.2', creator: { name: 'Interseptor', version: '' }, entries: flows.map((f) => harEntry(f, redact)) } }, null, 2);
      break;
    case 'url': text = flows.map((f) => flowURL(f)).join('\n'); break;
    case 'headers':
      text = flows.map((f) => headerPairs(side === 'res' ? (redact ? redactHeaders(f.resHeaders) : f.resHeaders) : (redact ? redactHeaders(f.reqHeaders) : f.reqHeaders)).map(([k, v]) => `${k}: ${v}`).join('\n')).join('\n\n');
      break;
    case 'body': text = flows.map((f) => (side === 'res' ? f.resBody : f.reqBody) || '').join('\n\n'); break;
    case 'markdown': text = each(markdown); break;
    case 'repro':
      text = JSON.stringify({
        interseptor_repro: 1,
        requests: flows.map((f) => ({ method: method(f), url: flowURL(f), headers: Object.fromEntries(reqPairs(f, redact)), body: hasBody(f) ? f.reqBody : '', ...(bodyBytes(f) ? { bodyBase64: base64(bodyBytes(f)) } : {}) })),
      }, null, 2);
      break;
  }
  return { text, notices };
}

const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`);

async function streamBody(id, side, limit = MAX_BODY_BYTES) {
  const r = await fetch(`/api/flows/${id}/body?side=${side}`, { credentials: 'same-origin' });
  if (r.status === 404 || r.status === 204 || !r.body) return { text: '', truncated: false };
  if (!r.ok) throw new Error(`body request failed (${r.status})`);
  const reader = r.body.getReader();
  const chunks = [];
  let total = 0, truncated = false;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (total + value.length > limit) {
      chunks.push(value.slice(0, limit - total));
      truncated = true;
      await reader.cancel();
      break;
    }
    chunks.push(value);
    total += value.length;
  }
  const buf = new Uint8Array(chunks.reduce((n, c) => n + c.length, 0));
  let off = 0;
  for (const c of chunks) { buf.set(c, off); off += c.length; }
  return { ...decodeBody(buf, truncated), truncated };
}
async function defaultDeps() {
  const core = await import('./core.js');
  return {
    fetchFlow: (id) => core.api(`/api/flows/${id}`),
    fetchBody: streamBody,
    copyText: core.copyText,
    toast: core.toast,
  };
}

// copyAs(kind, flows, deps) resolves details and bodies lazily, builds the text,
// copies it and toasts the size. It never throws; failures become a toast.
export async function copyAs(kind, flows, deps = {}) {
  let d = deps;
  try {
    if (!deps.copyText || !deps.toast || !deps.fetchFlow || !deps.fetchBody) d = { ...(await defaultDeps()), ...deps };
    const def = KIND_BY_NAME[kind];
    if (!def) throw new Error('unknown copy-as kind: ' + kind);
    const side = deps.side === 'res' ? 'res' : 'req';
    const needs = kind === 'body' ? [side] : def.bodies;
    const resolved = [];
    for (const input of flows) {
      let f = input;
      if (!f.method || !f.host || !f.reqHeaders) f = { ...(await d.fetchFlow(f.id)), ...f };
      f = { ...f };
      for (const s of needs) {
        const k = s + 'Body';
        if (typeof f[k] === 'string') { const t = truncateBody(f[k]); f[k] = t.text; if (t.truncated) f[k + 'Truncated'] = true; continue; }
        const got = await d.fetchBody(f.id, s);
        // A binary body is already cut in bytes by the reader; cutting its lossy
        // text again would count each U+FFFD as three bytes.
        if (got.bytes instanceof Uint8Array && got.bytes.length) {
          f[k] = got.text; f[k + 'Bytes'] = got.bytes;
          if (got.truncated) f[k + 'Truncated'] = true;
          continue;
        }
        const t = truncateBody(got.text);
        f[k] = t.text;
        if (got.truncated || t.truncated) f[s + 'BodyTruncated'] = true;
      }
      resolved.push(f);
    }
    const { text, notices } = buildCopyAs(kind, resolved, { redact: deps.redact, side });
    d.copyText(text, `copied ${def.label} (${fmtSize(new TextEncoder().encode(text).length)})`);
    for (const n of notices) d.toast(n);
    return { text, notices };
  } catch (e) {
    if (d.toast) d.toast('Copy failed: ' + ((e && e.message) || e), 'error');
    return null;
  }
}
