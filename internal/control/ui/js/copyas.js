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
// durationMs, reqBodyTruncated, resBodyTruncated}.

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

export function flowURL(f) {
  const scheme = f.scheme || 'http';
  const def = (scheme === 'https' && f.port === 443) || (scheme === 'http' && f.port === 80);
  const port = f.port && !def ? ':' + f.port : '';
  return `${scheme}://${f.host || ''}${port}${f.path || '/'}`;
}

export function truncateBody(text, limit = MAX_BODY_BYTES) {
  const s = String(text == null ? '' : text);
  return s.length > limit ? { text: s.slice(0, limit), truncated: true } : { text: s, truncated: false };
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
const hasBody = (f) => typeof f.reqBody === 'string' && f.reqBody.length > 0;
const method = (f) => (f.method || 'GET').toUpperCase();
const sq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`; // POSIX single-quote
const psq = (s) => `'${String(s).replace(/'/g, "''")}'`; // PowerShell single-quote
const jq = (s) => JSON.stringify(String(s));
const TRUNC = 'body truncated at 1 MB';

function curl(f, redact, ps) {
  const q = ps ? psq : sq;
  const bare = method(f) === 'GET' && !hasBody(f);
  const parts = [`${ps ? 'curl.exe' : 'curl'} ${bare ? '' : `-X ${method(f)} `}${q(flowURL(f))}`];
  for (const [k, v] of reqPairs(f, redact)) parts.push(`-H ${q(`${k}: ${v}`)}`);
  if (hasBody(f)) parts.push(`--data-raw ${q(f.reqBody)}`);
  let out = parts.join(ps ? ' `\n  ' : ' \\\n  ');
  if (f.reqBodyTruncated) out += `\n# ${TRUNC}`;
  return out;
}
function fetchSnippet(f, redact) {
  const lines = [`fetch(${jq(flowURL(f))}, {`, `  method: ${jq(method(f))},`];
  const pairs = reqPairs(f, redact);
  if (pairs.length) { lines.push('  headers: {'); pairs.forEach(([k, v]) => lines.push(`    ${jq(k)}: ${jq(v)},`)); lines.push('  },'); }
  if (hasBody(f)) lines.push(`  body: ${jq(f.reqBody)},`);
  lines.push('});');
  if (f.reqBodyTruncated) lines.push(`// ${TRUNC}`);
  return lines.join('\n');
}
function python(f, redact) {
  const lines = ['import requests', '', 'response = requests.request(', `    ${jq(method(f))},`, `    ${jq(flowURL(f))},`];
  const pairs = reqPairs(f, redact);
  if (pairs.length) { lines.push('    headers={'); pairs.forEach(([k, v]) => lines.push(`        ${jq(k)}: ${jq(v)},`)); lines.push('    },'); }
  if (hasBody(f)) lines.push(`    data=${jq(f.reqBody)}.encode("utf-8"),`);
  lines.push(')', 'print(response.status_code, response.text)');
  if (f.reqBodyTruncated) lines.push(`# ${TRUNC}`);
  return lines.join('\n');
}
function goSnippet(f, redact) {
  const body = hasBody(f);
  const lines = ['package main', '', 'import (', '\t"fmt"', '\t"io"', '\t"net/http"', ...(body ? ['\t"strings"'] : []), ')', '', 'func main() {',
    `\treq, err := http.NewRequest(${jq(method(f))}, ${jq(flowURL(f))}, ${body ? `strings.NewReader(${jq(f.reqBody)})` : 'nil'})`,
    '\tif err != nil {', '\t\tpanic(err)', '\t}'];
  for (const [k, v] of reqPairs(f, redact)) lines.push(`\treq.Header.Set(${jq(k)}, ${jq(v)})`);
  lines.push('\tresp, err := http.DefaultClient.Do(req)', '\tif err != nil {', '\t\tpanic(err)', '\t}', '\tdefer resp.Body.Close()',
    '\tdata, _ := io.ReadAll(resp.Body)', '\tfmt.Println(resp.StatusCode, string(data))', '}');
  if (f.reqBodyTruncated) lines.push(`// ${TRUNC}`);
  return lines.join('\n');
}
function httpie(f, redact) {
  const parts = [`http ${method(f)} ${sq(flowURL(f))}`];
  for (const [k, v] of reqPairs(f, redact)) parts.push(sq(`${k}:${v}`));
  if (hasBody(f)) parts.push(`--raw ${sq(f.reqBody)}`);
  let out = parts.join(' \\\n  ');
  if (f.reqBodyTruncated) out += `\n# ${TRUNC}`;
  return out;
}
function rawHTTP(f, redact) {
  const headers = headerPairs(redact ? redactHeaders(f.reqHeaders) : f.reqHeaders, { skip: ['host'] });
  let out = `${method(f)} ${f.path || '/'} ${f.httpVersion || 'HTTP/1.1'}\r\nHost: ${f.host}${f.port && ![80, 443].includes(f.port) ? ':' + f.port : ''}\r\n`;
  for (const [k, v] of headers) out += `${k}: ${v}\r\n`;
  out += '\r\n' + (hasBody(f) ? f.reqBody : '');
  return out;
}
const harHeaders = (h) => headerPairs(h).map(([name, value]) => ({ name, value }));
function harEntry(f, redact) {
  const url = flowURL(f);
  const q = [];
  try { new URL(url).searchParams.forEach((value, name) => q.push({ name, value })); } catch (e) { /* relative or odd URL: leave empty */ }
  const mime = (f.mime || '').split(';')[0];
  const reqType = headerPairs(f.reqHeaders).find(([k]) => k.toLowerCase() === 'content-type');
  const resText = typeof f.resBody === 'string' ? f.resBody : '';
  const entry = {
    startedDateTime: f.startedAt || '1970-01-01T00:00:00.000Z',
    time: f.durationMs || 0,
    request: {
      method: method(f), url, httpVersion: f.httpVersion || 'HTTP/1.1', cookies: [],
      headers: harHeaders(redact ? redactHeaders(f.reqHeaders) : f.reqHeaders), queryString: q,
      headersSize: -1, bodySize: hasBody(f) ? f.reqBody.length : 0,
    },
    response: {
      status: f.status || 0, statusText: '', httpVersion: f.httpVersion || 'HTTP/1.1', cookies: [],
      headers: harHeaders(redact ? redactHeaders(f.resHeaders) : f.resHeaders),
      content: { size: resText.length, mimeType: mime, text: resText }, redirectURL: '', headersSize: -1, bodySize: resText.length,
    },
    cache: {}, timings: { send: 0, wait: f.durationMs || 0, receive: 0 },
  };
  if (hasBody(f)) entry.request.postData = { mimeType: reqType ? reqType[1] : '', text: f.reqBody };
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
    case 'url': text = flows.map(flowURL).join('\n'); break;
    case 'headers':
      text = flows.map((f) => headerPairs(side === 'res' ? (redact ? redactHeaders(f.resHeaders) : f.resHeaders) : (redact ? redactHeaders(f.reqHeaders) : f.reqHeaders)).map(([k, v]) => `${k}: ${v}`).join('\n')).join('\n\n');
      break;
    case 'body': text = flows.map((f) => (side === 'res' ? f.resBody : f.reqBody) || '').join('\n\n'); break;
    case 'markdown': text = each(markdown); break;
    case 'repro':
      text = JSON.stringify({
        interseptor_repro: 1,
        requests: flows.map((f) => ({ method: method(f), url: flowURL(f), headers: Object.fromEntries(reqPairs(f, redact)), body: hasBody(f) ? f.reqBody : '' })),
      }, null, 2);
      break;
  }
  return { text, notices };
}

const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`);

async function streamBody(id, side, limit = MAX_BODY_BYTES) {
  const r = await fetch(`/api/flows/${id}/body?side=${side}`, { credentials: 'same-origin' });
  if (r.status === 404 || r.status === 204) return { text: '', truncated: false };
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
  return { text: new TextDecoder().decode(buf), truncated };
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
      if (!f.method || !f.host) f = { ...(await d.fetchFlow(f.id)), ...f };
      f = { ...f };
      for (const s of needs) {
        const k = s + 'Body';
        if (typeof f[k] === 'string') { const t = truncateBody(f[k]); f[k] = t.text; if (t.truncated) f[k + 'Truncated'] = true; continue; }
        const got = await d.fetchBody(f.id, s);
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
