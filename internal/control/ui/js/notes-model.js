// notes-model.js — structured references inside notes. `#F-2` names a finding and
// `flow:123` a captured flow. References resolve only against ids the project
// actually has; anything else stays plain text. Pure: no imports, no DOM.

export const MAX_REFS = 40;
const RE = /#F-(\d+)\b|\bflow:(\d+)\b/gi;

export function parseNoteRefs(text) {
  const src = String(text == null ? '' : text);
  const out = [];
  let last = 0;
  RE.lastIndex = 0;
  let m;
  while ((m = RE.exec(src))) {
    const prev = m.index > 0 ? src[m.index - 1] : '';
    // "abc#F-2" and "xflow:5" are not references; neither is a URL fragment.
    if (prev && /[\w/.:#-]/.test(prev)) continue;
    if (m.index > last) out.push({ type: 'text', text: src.slice(last, m.index) });
    out.push(m[1] != null ? { type: 'finding', id: Number(m[1]), text: m[0] } : { type: 'flow', id: Number(m[2]), text: m[0] });
    last = m.index + m[0].length;
  }
  if (last < src.length) out.push({ type: 'text', text: src.slice(last) });
  return out;
}

export function collectRefIds(text) {
  const findings = new Set(), flows = new Set();
  for (const t of parseNoteRefs(text)) {
    if (t.type === 'finding' && findings.size < MAX_REFS) findings.add(t.id);
    if (t.type === 'flow' && flows.size < MAX_REFS) flows.add(t.id);
  }
  return { findings, flows };
}

export function resolveRefs(tokens, { findingIds, flowIds } = {}) {
  return tokens.map((t) => {
    if (t.type === 'finding' && !(findingIds && findingIds.has(t.id))) return { type: 'text', text: t.text };
    if (t.type === 'flow' && !(flowIds && flowIds.has(t.id))) return { type: 'text', text: t.text };
    return t;
  });
}

export const chipLabel = (t) => (t.type === 'finding' ? 'Finding F-' + t.id : 'Flow #' + t.id);

const MAX_TITLE = 120;
const MAX_DETAIL = 8000;

// promoteRequest turns a note selection into a finding draft. The first
// non-empty line (Markdown heading marks removed) is the title; the rest is the
// detail. It uses the ordinary create endpoint, so the server owns readiness.
export function promoteRequest(selection) {
  const lines = String(selection == null ? '' : selection).split('\n');
  const first = lines.findIndex((l) => l.trim());
  if (first < 0) return null;
  const title = lines[first].replace(/^\s*#{1,6}\s*/, '').replace(/^\s*[-*]\s+/, '').trim().slice(0, MAX_TITLE);
  if (!title) return null;
  const detail = lines.slice(first + 1).join('\n').trim().slice(0, MAX_DETAIL);
  const body = { severity: 'Medium', status: 'needs_verification', source: 'human', title };
  if (detail) body.detail = detail;
  return { path: '/api/findings', method: 'POST', body };
}
