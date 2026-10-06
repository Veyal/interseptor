// map-coverage.js — evidence-aware Map rows built only from data the API already
// exposes: the endpoint's observed statuses and the flows attached to findings.
// Nothing here is guessed. With no findings data loaded, no linkage is claimed
// and the "linked" pip is omitted. Pure: no imports, no DOM.

const pathOnly = (p) => String(p || '/').split('?')[0].split('#')[0] || '/';
const keyOf = (method, host, path) => String(method || '').toUpperCase() + ' ' + String(host || '').toLowerCase() + pathOnly(path);

// linkedIndex({flowIds, keys}) from the findings list, or null when it is unknown.
export function linkedIndex(findings) {
  if (!Array.isArray(findings)) return null;
  const flowIds = new Set(), keys = new Set();
  findings.forEach((f) => (Array.isArray(f && f.flows) ? f.flows : []).forEach((fl) => {
    if (fl && fl.flowId) flowIds.add(Number(fl.flowId));
    if (fl && fl.method && fl.host) keys.add(keyOf(fl.method, fl.host, fl.path));
  }));
  return { flowIds, keys };
}

export function endpointLinked(e, idx) {
  if (!idx || !e) return false;
  if (e.lastFlowId && idx.flowIds.has(Number(e.lastFlowId))) return true;
  return idx.keys.has(keyOf(e.method, e.host, e.path));
}

export function coveragePips(e, idx) {
  const pips = [{ id: 'captured', label: 'Captured in History', on: true }];
  if (idx) pips.push({ id: 'linked', label: endpointLinked(e, idx) ? 'Linked to a finding' : 'Not linked to a finding', on: endpointLinked(e, idx) });
  return pips;
}

export function coverageSummary(eps, idx) {
  if (!idx) return null;
  const total = (eps || []).length;
  const linked = (eps || []).filter((e) => endpointLinked(e, idx)).length;
  return { linked, total, text: 'Linked to a finding ' + linked + '/' + total };
}

export function withoutEvidence(eps, idx) {
  if (!idx) return (eps || []).slice();
  return (eps || []).filter((e) => !endpointLinked(e, idx));
}

// Auth is inferred only from statuses actually seen: 401/403 means a credential
// check was observed; 2xx/3xx with no such status reads as open; otherwise unknown.
export function authState(e) {
  const st = (e && Array.isArray(e.statuses) ? e.statuses : []).map(Number);
  if (st.some((s) => s === 401 || s === 403)) return { kind: 'auth', label: 'Auth required', icon: 'lock' };
  if (st.some((s) => s >= 200 && s < 400)) return { kind: 'open', label: 'Open', icon: 'lock-open' };
  return { kind: 'unknown', label: '', icon: '' };
}
