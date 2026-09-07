import { api, esc, escAttr } from './core.js';

const METRICS = [
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

function parseVector(vector) {
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

function buildVector(values, extras = []) {
  if (!METRICS.every(([key]) => values[key])) return '';
  const base = METRICS.map(([key]) => `${key}:${values[key]}`);
  return `CVSS:4.0/${base.concat(extras).join('/')}`;
}

function displayRating(rating) {
  return rating === 'INFO' ? 'Info' : rating ? rating[0] + rating.slice(1).toLowerCase() : '';
}

export function renderCvssEditor(initialVector = '') {
  const parsed = parseVector(initialVector);
  const metrics = METRICS.map(([key, label, options]) => `<label class="cvss-metric"><span>${esc(label)}</span><select data-cvss-metric="${key}" aria-label="${escAttr(label)}"><option value="">—</option>${options.map(([value, name]) => `<option value="${value}"${parsed.values[key] === value ? ' selected' : ''}>${esc(value)} · ${esc(name)}</option>`).join('')}</select></label>`).join('');
  return `<label for="findCvss">CVSS v4.0 vector</label><input id="findCvss" class="find-field-text cvss-vector" type="text" value="${escAttr(initialVector)}" autocomplete="off" spellcheck="false" aria-describedby="findCvssStatus"><details id="findCvssCalculator" class="cvss-calculator"><summary>Metric calculator</summary><div class="cvss-metrics">${metrics}</div></details><div class="cvss-editor-actions"><button type="button" class="btn" data-cvss-preview>Preview</button><button type="button" class="btn btn-primary" data-cvss-apply disabled>Apply vector + severity</button></div><p class="hint cvss-status" id="findCvssStatus" data-cvss-status role="status" aria-live="polite"></p>`;
}

// bindCvssEditor deliberately keeps preview separate from persistence. Typing
// or selecting a metric can only evaluate; Apply is the sole operation that
// sends both cvss and the matching normalized severity to the finding API.
export function bindCvssEditor(root, onApply) {
  const input = root.querySelector('#findCvss');
  const status = root.querySelector('[data-cvss-status]');
  const previewButton = root.querySelector('[data-cvss-preview]');
  const applyButton = root.querySelector('[data-cvss-apply]');
  const selects = [...root.querySelectorAll('[data-cvss-metric]')];
  if (!input || !status || !previewButton || !applyButton) return;

  let generation = 0;
  let timer = null;
  let latest = null;
  let latestVector = '';

  const setStatus = (message, error = false) => {
    status.textContent = message;
    status.classList.toggle('error', error);
    status.classList.toggle('success', !error && !!message);
  };
  const invalidate = () => {
    generation++;
    latest = null;
    latestVector = '';
    applyButton.disabled = true;
  };
  const syncSelectsFromInput = () => {
    const parsed = parseVector(input.value);
    if (!parsed.valid) return;
    for (const select of selects) {
      const value = parsed.values[select.dataset.cvssMetric];
      if (value && [...select.options].some(option => option.value === value)) select.value = value;
    }
  };
  const updateVectorFromSelects = () => {
    const values = Object.fromEntries(selects.map(select => [select.dataset.cvssMetric, select.value]));
    const parsed = parseVector(input.value);
    const vector = buildVector(values, parsed.extras);
    if (vector) input.value = vector;
  };
  const runPreview = async token => {
    const vector = input.value.trim();
    setStatus('Evaluating…');
    try {
      const result = await api('/api/finding-cvss', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ vector }) });
      if (token !== generation) return;
      if (!root.isConnected) return;
      latest = result;
      latestVector = vector;
      applyButton.disabled = false;
      applyButton.textContent = `Apply vector + ${displayRating(result.rating)} severity`;
      setStatus(`Score ${Number(result.score).toFixed(1)} · ${displayRating(result.rating)} (${result.nomenclature})`);
    } catch (error) {
      if (token !== generation) return;
      if (!root.isConnected) return;
      applyButton.disabled = true;
      setStatus(error.message || 'Invalid CVSS v4.0 vector', true);
    }
  };
  const schedulePreview = () => {
    invalidate();
    if (timer) clearTimeout(timer);
    const token = generation;
    timer = setTimeout(() => { timer = null; runPreview(token); }, 180);
  };
  input.addEventListener('input', () => { syncSelectsFromInput(); schedulePreview(); });
  for (const select of selects) select.addEventListener('change', () => { updateVectorFromSelects(); schedulePreview(); });
  previewButton.addEventListener('click', () => {
    if (timer) clearTimeout(timer);
    invalidate();
    runPreview(generation);
  });
  applyButton.addEventListener('click', async () => {
    if (!latest || latestVector !== input.value.trim()) return;
    const applying = latest; const token = generation;
    applyButton.disabled = true;
    try {
      await onApply({ vector: latest.canonicalVector || latestVector, severity: latest.rating === 'INFO' ? 'Info' : displayRating(latest.rating), evaluation: latest });
      if(token===generation && root.isConnected)setStatus(`Applied ${displayRating(applying.rating)} severity`);
    } catch (error) {
      if(token===generation && root.isConnected){applyButton.disabled = false; setStatus(error.message || 'Could not apply CVSS vector', true);}
    }
  });
}
