import { api, esc, escAttr } from './core.js';
import { METRICS, parseVector, buildVector, displayRating, previewGate, previewStatusText } from './cvss-model.js';

export function renderCvssEditor(initialVector = '') {
  const parsed = parseVector(initialVector);
  const metrics = METRICS.map(([key, label, options]) => `<label class="cvss-metric"><span>${esc(label)}</span><select data-cvss-metric="${key}" aria-label="${escAttr(label)}"><option value="">—</option>${options.map(([value, name]) => `<option value="${value}"${parsed.values[key] === value ? ' selected' : ''}>${esc(value)} · ${esc(name)}</option>`).join('')}</select></label>`).join('');
  return `<label for="findCvss">CVSS v4.0 vector</label><textarea id="findCvss" class="find-field-text cvss-vector" rows="3" wrap="soft" autocomplete="off" spellcheck="false" aria-describedby="findCvssStatus">${esc(initialVector)}</textarea><details id="findCvssCalculator" class="cvss-calculator"><summary>Metric calculator</summary><div class="cvss-metrics">${metrics}</div></details><div class="cvss-editor-actions"><button type="button" class="btn" data-cvss-preview>Preview</button><button type="button" class="btn btn-primary" data-cvss-apply disabled>Apply vector + severity</button><button type="button" class="btn" data-cvss-discard>Discard preview</button></div><p class="hint cvss-status" id="findCvssStatus" data-cvss-status role="status" aria-live="polite"></p>`;
}

// bindCvssEditor deliberately keeps preview separate from persistence. Typing
// or selecting a metric can only evaluate; Apply is the sole operation that
// sends both cvss and the matching normalized severity to the finding API.
export function bindCvssEditor(root, onApply, onPreviewChange = () => {}, onDiscard = null) {
  const input = root.querySelector('#findCvss');
  const status = root.querySelector('[data-cvss-status]');
  const previewButton = root.querySelector('[data-cvss-preview]');
  const applyButton = root.querySelector('[data-cvss-apply]');
  const discardButton = root.querySelector('[data-cvss-discard]');
  const selects = [...root.querySelectorAll('[data-cvss-metric]')];
  if (!input || !status || !previewButton || !applyButton) return;

  let generation = 0;
  let editGeneration = 0;
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
  const syncSelectsFromInput = (reset = false) => {
    const parsed = parseVector(input.value);
    if (!parsed.valid && !reset) return;
    for (const select of selects) {
      const value = parsed.values[select.dataset.cvssMetric];
      if (value && [...select.options].some(option => option.value === value)) select.value = value;
      else if (reset) select.value = '';
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
    const gate = previewGate(vector);
    if (!gate.send) { applyButton.disabled = true; setStatus(gate.message, !!vector); return; }
    setStatus('Evaluating…');
    try {
      const result = await api('/api/finding-cvss', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ vector }) });
      if (token !== generation) return;
      if (!root.isConnected) return;
      latest = result;
      latestVector = vector;
      if (result.legacy) {
        applyButton.disabled = true;
        setStatus(`Score ${Number(result.score).toFixed(1)} · ${displayRating(result.rating)} (${result.nomenclature}). Legacy CVSS 3.1 cannot be saved; enter a CVSS:4.0 vector.`, true);
        return;
      }
      applyButton.disabled = false;
      applyButton.textContent = `Apply vector (${Number(result.score).toFixed(1)} · ${displayRating(result.rating)})`;
      const current = root.ownerDocument?.querySelector?.('#findSeverityChip')?.dataset?.severity || root.ownerDocument?.querySelector?.('#findSeverity')?.value || '';
      setStatus(previewStatusText(result, current));
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
  // A vector is one line: the field wraps a long one for reading, but Enter must not add a newline.
  input.addEventListener('keydown', event => { if (event.key === 'Enter') event.preventDefault(); });
  input.addEventListener('input', () => { editGeneration++; syncSelectsFromInput(); onPreviewChange(input.value); schedulePreview(); });
  for (const select of selects) select.addEventListener('change', () => { editGeneration++; updateVectorFromSelects(); onPreviewChange(input.value); schedulePreview(); });
  previewButton.addEventListener('click', () => {
    if (timer) clearTimeout(timer);
    invalidate();
    runPreview(generation);
  });
  if (onDiscard) discardButton?.addEventListener('click', async () => {
    if (discardButton.disabled) return;
    if (timer) { clearTimeout(timer); timer = null; }
    invalidate();
    const token = editGeneration;
    discardButton.disabled = true;
    try {
      const vector = await onDiscard();
      if (token !== editGeneration || !root.isConnected) return;
      if (timer) { clearTimeout(timer); timer = null; }
      invalidate();
      input.value = vector;
      syncSelectsFromInput(true);
      setStatus('Preview discarded');
    } catch (error) {
      if (token === editGeneration && root.isConnected) setStatus(error.message || 'Could not discard preview', true);
    } finally {
      if (root.isConnected) discardButton.disabled = false;
    }
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
