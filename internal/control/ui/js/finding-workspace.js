// Pure presentation rules shared by the Findings reader and its navigation.
export const FINDING_SECTIONS = [
  { id: 'overview', label: 'Overview' },
  { id: 'evidence', label: 'Evidence' },
  { id: 'remediation', label: 'Remediation' },
  { id: 'review', label: 'Review' },
];

export function filterFindingRecords(records, { query = '', severity = '', status = '', tag = '' } = {}) {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  return records.filter(f => {
    if (severity && f.severity !== severity) return false;
    if (status && f.status !== status) return false;
    if (tag && !(f.tags || []).includes(tag)) return false;
    const text = [`#${f.id}`, f.title, f.summary, f.target, ...(f.targets || []).flatMap(t => [t.url, ...(t.methods || []), t.method, t.variant, t.role, t.relation, t.note]), f.cwe, ...(f.tags || [])].filter(Boolean).join(' ').toLocaleLowerCase();
    return terms.every(term => text.includes(term));
  });
}

export function parseFindingRoute(hash) {
  const match = hash.match(/^#finding-(\d+)(?:\/(overview|evidence|remediation|review|flow-(\d+)))?$/i);
  if (!match || !Number.isSafeInteger(Number(match[1])) || Number(match[1]) <= 0) return null;
  const flowId = match[3] ? Number(match[3]) : null;
  if (flowId !== null && (!Number.isSafeInteger(flowId) || flowId <= 0)) return null;
  return { id: Number(match[1]), section: flowId ? 'evidence' : (match[2] || 'overview').toLowerCase(), flowId };
}

export function findingSectionForGap(gap) {
  if (gap.startsWith('capability:') || gap==='verification') return 'review';
  if(gap==='evidence_missing')return 'evidence';
  if (['evidence', 'proof', 'reproduction', 'action', 'result', 'control', 'visual'].includes(gap)) return 'evidence';
  if (['fix', 'retest'].includes(gap)) return 'remediation';
  return ['confidence','execution','execution_reason','cvss','severity'].includes(gap) ? 'review' : 'overview';
}

// Retain unsaved fields per finding. Only the acknowledgement for the newest
// value can clear it; an older rejection cannot poison a newer edit or retry.
export function createFindingDraftStore() {
  const records = new Map();
  const removeEmpty = id => { if (!records.get(id)?.size) records.delete(id); };
  return {
    stage(id, fields) {
      const record = records.get(id) || new Map(), tokens = {};
      for (const [key, value] of Object.entries(fields)) {
        const token = Symbol(key);
        tokens[key] = token;
        record.set(key, { value, token, failed: false });
      }
      records.set(id, record);
      return tokens;
    },
    tokens: id => Object.fromEntries([...(records.get(id) || [])].map(([key, draft]) => [key, draft.token])),
    acknowledge(id, tokens) {
      const record = records.get(id);
      for (const [key, token] of Object.entries(tokens)) {
        if (record?.get(key)?.token === token) record.delete(key);
      }
      removeEmpty(id);
    },
    fail(id, tokens) {
      const record = records.get(id);
      for (const [key, token] of Object.entries(tokens)) {
        const draft = record?.get(key);
        if (draft?.token === token) draft.failed = true;
      }
    },
    values: id => Object.fromEntries([...(records.get(id) || [])].map(([key, draft]) => [key, draft.value])),
    has: id => !!records.get(id)?.size,
    hasAny: () => records.size > 0,
    failed: id => [...(records.get(id)?.values() || [])].some(draft => draft.failed),
    discard(id, key) { records.get(id)?.delete(key); removeEmpty(id); },
  };
}
