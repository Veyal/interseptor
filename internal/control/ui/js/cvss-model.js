// cvss-model.js — pure CVSS v4.0 vector helpers (no DOM, no fetch). Node tests pin it.

export const METRICS = [
  ['AV', 'Attack Vector', [['N', 'Network'], ['A', 'Adjacent'], ['L', 'Local'], ['P', 'Physical']]],
  ['AC', 'Attack Complexity', [['L', 'Low'], ['H', 'High']]],
  ['AT', 'Attack Requirements', [['N', 'None'], ['P', 'Present']]],
  ['PR', 'Privileges Required', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['UI', 'User Interaction', [['N', 'None'], ['P', 'Passive'], ['A', 'Active']]],
  ['VC', 'Vulnerable Confidentiality', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['VI', 'Vulnerable Integrity', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['VA', 'Vulnerable Availability', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['SC', 'Subsequent Confidentiality', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['SI', 'Subsequent Integrity', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
  ['SA', 'Subsequent Availability', [['N', 'None'], ['L', 'Low'], ['H', 'High']]],
];
const BASE_METRICS = new Set(METRICS.map(([key]) => key));

export function parseVector(vector) {
  const values = {}, extras = [];
  const parts = String(vector || '').trim().split('/');
  if (parts.shift() !== 'CVSS:4.0') return { values, extras, valid: false };
  for (const part of parts) {
    const [key, value] = part.split(':');
    if (!key || !value) continue;
    if (BASE_METRICS.has(key)) values[key] = value;
    else extras.push(part);
  }
  return { values, extras, valid: METRICS.every(([key]) => values[key]) };
}

export function buildVector(values, extras = []) {
  if (!METRICS.every(([key]) => values[key])) return '';
  const base = METRICS.map(([key]) => `${key}:${values[key]}`);
  return `CVSS:4.0/${base.concat(extras).join('/')}`;
}

export function displayRating(rating) {
  return rating === 'INFO' ? 'Info' : rating ? rating[0] + rating.slice(1).toLowerCase() : '';
}


// previewGate decides whether a vector is worth sending to /api/finding-cvss.
// A CVSS:4.0 vector that is still missing base metrics (the state after every
// keystroke while typing) is answered locally; the server would only reject it
// with a 400 per keystroke. Other prefixes (legacy 3.1) still go to the server,
// which explains why they cannot be saved.
export function previewGate(vector) {
  const v = String(vector || '').trim();
  if (!v) return { send: false, message: 'Enter a CVSS:4.0 vector or choose the metrics.' };
  if (v.startsWith('CVSS:4.0')) {
    const parsed = parseVector(v);
    if (!parsed.valid) {
      const set = METRICS.filter(([key]) => parsed.values[key]).length;
      return { send: false, message: `Incomplete vector: ${set} of ${METRICS.length} base metrics set.` };
    }
  }
  return { send: true, message: '' };
}

// previewStatusText joins the score, the server explanation and the severity
// mismatch note as separate sentences.
export function previewStatusText(result, currentSeverity) {
  const head = `Score ${Number(result.score).toFixed(1)} · ${displayRating(result.rating)} (${result.nomenclature})`;
  const sentence = (t) => { const x = String(t || '').trim(); return x ? (/[.!?]$/.test(x) ? x : x + '.') : ''; };
  const mismatch = currentSeverity && currentSeverity.toLowerCase() !== String(result.severity || '').toLowerCase()
    ? `Current severity ${currentSeverity} differs; Apply sets ${result.severity}.` : '';
  const expl = sentence(result.explanation);
  return [expl ? `${head} — ${expl}` : `${head}.`, mismatch].filter(Boolean).join(' ');
}
