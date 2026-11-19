// JWT X-Ray Recon sidebar panel: scans recent flows (/api/flows?limit=50),
// inspects the selected flow, decodes pasted tokens, and updates on flow.new SSE.

const instances = new WeakMap();

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function unwrapResult(res) {
  return res && res.result !== undefined ? res.result : res;
}

function normalizeFlow(raw) {
  if (!raw || typeof raw !== 'object') return null;
  return raw.flow && typeof raw.flow === 'object' ? raw.flow : raw;
}

function formatEpoch(sec) {
  if (typeof sec !== 'number' || !Number.isFinite(sec)) return null;
  try {
    return `${sec} (${new Date(sec * 1000).toISOString()})`;
  } catch {
    return String(sec);
  }
}

function severityRank(sev) {
  switch (sev) {
    case 'high': return 3;
    case 'medium': return 2;
    case 'low': return 1;
    default: return 0;
  }
}

function severityLabel(sev) {
  switch (sev) {
    case 'high': return 'HIGH';
    case 'medium': return 'MED';
    case 'low': return 'LOW';
    default: return 'OK';
  }
}

function buildTokenKey(item) {
  const fid = item.flowId != null ? String(item.flowId) : 'manual';
  return `${fid}:${item.raw}`;
}

function summarizeFlowMeta(flow, fallbackId) {
  if (!flow && fallbackId == null) return 'Manual input';
  if (!flow) return `Flow #${fallbackId}`;
  const idPart = flow.id != null ? `#${flow.id} ` : (fallbackId != null ? `#${fallbackId} ` : '');
  const method = String(flow.method || 'GET').toUpperCase();
  const host = String(flow.host || '');
  const path = String(flow.path || '/');
  const status = flow.status ? ` -> ${flow.status}` : '';
  return `${idPart}${method} ${host}${path}${status}`.trim();
}

async function copyText(text, ctx) {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      ctx?.toast?.('Copied JWT to clipboard', 'success');
      return;
    }
  } catch {
    // Fall through to toast notice.
  }
  ctx?.toast?.('Clipboard write unavailable in this context', 'warn');
}

export async function mount(container, ctx) {
  unmount(container);

  const state = {
    alive: true,
    scanEpoch: 0,
    cleanups: [],
    items: [],
    selectedKey: null,
  };
  instances.set(container, state);

  const root = el('div', 'ab-jwt');

  // Header
  const head = el('header', 'ab-jwt-head');
  const titleWrap = el('div', 'ab-jwt-title-wrap');
  titleWrap.append(
    el('h2', 'ab-jwt-title', 'JWT X-Ray'),
    el('p', 'ab-jwt-sub', 'Detect Bearer & cookie JWTs, inspect claims, and flag alg=none or missing exp')
  );

  const headActions = el('div', 'ab-jwt-actions');
  const inspectSelBtn = el('button', 'ab-jwt-btn', 'Inspect Selected Flow');
  inspectSelBtn.type = 'button';
  const scanRecentBtn = el('button', 'ab-jwt-btn is-primary', 'Scan Recent (50)');
  scanRecentBtn.type = 'button';
  headActions.append(inspectSelBtn, scanRecentBtn);
  head.append(titleWrap, headActions);

  // Manual token decoder form
  const decodeForm = el('form', 'ab-jwt-decode-bar');
  const tokenInput = el('input', 'ab-jwt-input');
  tokenInput.type = 'text';
  tokenInput.placeholder = 'Paste raw JWT (eyJ...) to decode and check claims...';
  tokenInput.setAttribute('aria-label', 'Raw JWT input');
  const decodeBtn = el('button', 'ab-jwt-btn', 'Decode Token');
  decodeBtn.type = 'submit';
  decodeForm.append(tokenInput, decodeBtn);

  // Metrics strip
  const metrics = el('div', 'ab-jwt-metrics');
  const statusMsg = el('p', 'ab-jwt-status', 'Ready');

  // Master-detail body
  const layout = el('div', 'ab-jwt-layout');
  const listPane = el('div', 'ab-jwt-list');
  const detailPane = el('div');

  layout.append(listPane, detailPane);
  root.append(head, decodeForm, metrics, statusMsg, layout);
  container.replaceChildren(root);

  function setStatus(text, isError) {
    statusMsg.textContent = text;
    statusMsg.classList.toggle('is-error', Boolean(isError));
  }

  function mergeAnalysis(rawRes, flowMeta, fallbackId) {
    const data = unwrapResult(rawRes);
    if (!data || typeof data !== 'object') return [];
    const tokens = Array.isArray(data.tokens) ? data.tokens : [];
    const resolvedFlow = data.flow || flowMeta || null;
    const fid = data.flowId ?? resolvedFlow?.id ?? fallbackId ?? null;
    const added = [];
    for (const tok of tokens) {
      const entry = {
        ...tok,
        flowId: fid,
        flowMeta: resolvedFlow,
        flowSummary: summarizeFlowMeta(resolvedFlow, fid),
      };
      const key = buildTokenKey(entry);
      entry._key = key;
      const idx = state.items.findIndex((x) => x._key === key);
      if (idx >= 0) {
        state.items[idx] = entry;
      } else {
        state.items.unshift(entry);
      }
      added.push(entry);
    }
    state.items.sort((a, b) => severityRank(b.severity) - severityRank(a.severity));
    if (!state.selectedKey && state.items.length > 0) {
      state.selectedKey = state.items[0]._key;
    }
    return added;
  }

  function renderMetrics() {
    const total = state.items.length;
    const highCount = state.items.filter((t) => t.severity === 'high' || t.algNone).length;
    const noExpCount = state.items.filter((t) => t.missingExp).length;
    const expiredCount = state.items.filter((t) => t.expired).length;

    metrics.replaceChildren();
    const specs = [
      ['JWTs Found', total],
      ['High Risk / alg=none', highCount],
      ['Missing exp', noExpCount],
      ['Expired', expiredCount],
    ];
    for (const [label, val] of specs) {
      const box = el('div', 'ab-jwt-metric');
      box.append(el('span', 'ab-jwt-metric-val', String(val)), el('span', 'ab-jwt-metric-lbl', label));
      metrics.append(box);
    }
  }

  function renderDetail() {
    detailPane.replaceChildren();
    const item = state.items.find((x) => x._key === state.selectedKey) || state.items[0] || null;
    if (!item) {
      detailPane.append(el('p', 'ab-jwt-empty', 'No JWT selected. Scan recent traffic, select a flow, or paste a token above.'));
      return;
    }

    const card = el('section', 'ab-jwt-card');

    const cardHead = el('div', 'ab-jwt-card-head');
    const metaWrap = el('div', 'ab-jwt-card-meta');
    const sevBadge = el('span', `ab-jwt-badge sev-${item.severity || 'info'}`, severityLabel(item.severity));
    const algBadge = el('span', 'ab-jwt-badge', `alg: ${item.alg || '(none)'}`);
    const srcBadge = el('span', 'ab-jwt-badge', item.source || 'unknown');
    metaWrap.append(sevBadge, algBadge, srcBadge);

    const copyBtn = el('button', 'ab-jwt-btn is-small', 'Copy JWT');
    copyBtn.type = 'button';
    copyBtn.addEventListener('click', () => {
      void copyText(item.raw, ctx);
    });
    cardHead.append(metaWrap, copyBtn);
    card.append(cardHead);

    card.append(el('div', 'ab-jwt-row-src', item.flowSummary));

    const warnings = Array.isArray(item.warnings) ? item.warnings : [];
    if (warnings.length > 0) {
      const warnTitle = el('h3', 'ab-jwt-sec-title', 'Risk Warnings');
      const warnList = el('ul', 'ab-jwt-warn-list');
      for (const w of warnings) {
        const isHigh = item.severity === 'high' && (String(w).includes('none') || String(w).includes('Signature'));
        warnList.append(el('li', `ab-jwt-warn-item${isHigh ? ' is-high' : ''}`, w));
      }
      card.append(warnTitle, warnList);
    }

    const payload = item.payload && typeof item.payload === 'object' ? item.payload : {};
    const claimRows = [];
    for (const k of ['iss', 'sub', 'aud', 'exp', 'nbf', 'iat', 'jti']) {
      if (payload[k] !== undefined) {
        const val = (k === 'exp' || k === 'nbf' || k === 'iat') && typeof payload[k] === 'number'
          ? formatEpoch(payload[k])
          : (typeof payload[k] === 'object' ? JSON.stringify(payload[k]) : String(payload[k]));
        claimRows.push([k, val]);
      }
    }
    if (claimRows.length > 0) {
      const claimsSec = el('div');
      claimsSec.append(el('h3', 'ab-jwt-sec-title', 'Registered Claims'));
      const dl = el('dl', 'ab-jwt-claims');
      for (const [k, v] of claimRows) {
        dl.append(el('dt', '', k), el('dd', '', v));
      }
      claimsSec.append(dl);
      card.append(claimsSec);
    }

    const jsonGrid = el('div', 'ab-jwt-json-grid');
    const hdrBox = el('div');
    hdrBox.append(
      el('h3', 'ab-jwt-sec-title', 'Decoded Header'),
      el('pre', 'ab-jwt-pre', JSON.stringify(item.header || {}, null, 2))
    );
    const payBox = el('div');
    payBox.append(
      el('h3', 'ab-jwt-sec-title', 'Decoded Payload'),
      el('pre', 'ab-jwt-pre', JSON.stringify(payload, null, 2))
    );
    jsonGrid.append(hdrBox, payBox);
    card.append(jsonGrid);

    const rawBox = el('div');
    rawBox.append(
      el('h3', 'ab-jwt-sec-title', `Raw Token (${item.signaturePresent ? 'signature present' : 'empty signature'})`),
      el('pre', 'ab-jwt-pre', item.raw)
    );
    card.append(rawBox);

    detailPane.append(card);
  }

  function renderList() {
    listPane.replaceChildren();
    if (state.items.length === 0) {
      listPane.append(el('p', 'ab-jwt-empty', 'No JWTs detected yet.'));
      return;
    }

    for (const item of state.items) {
      const row = el('button', `ab-jwt-row${item._key === state.selectedKey ? ' is-active' : ''}`);
      row.type = 'button';
      row.setAttribute('aria-pressed', item._key === state.selectedKey ? 'true' : 'false');

      const top = el('div', 'ab-jwt-row-top');
      top.append(
        el('span', `ab-jwt-badge sev-${item.severity || 'info'}`, severityLabel(item.severity)),
        el('span', 'ab-jwt-badge', item.alg || '(none)'),
        el('span', 'ab-jwt-badge', item.source || 'token')
      );

      const src = el('span', 'ab-jwt-row-src', item.flowSummary);
      const warn = el(
        'span',
        'ab-jwt-row-warn',
        item.warnings?.length ? item.warnings[0] : 'No structural warnings'
      );

      row.append(top, src, warn);
      row.addEventListener('click', () => {
        state.selectedKey = item._key;
        renderList();
        renderDetail();
      });
      listPane.append(row);
    }
  }

  function renderAll() {
    renderMetrics();
    renderList();
    renderDetail();
  }

  async function inspectFlow(flow) {
    if (!state.alive || !flow || typeof ctx?.runAction !== 'function') return [];
    const payload = flow.id != null ? { flowId: flow.id } : { flow };
    const res = await ctx.runAction('analyze_flow', payload);
    if (!state.alive) return [];
    return mergeAnalysis(res, flow, flow.id);
  }

  async function scanRecentFlows() {
    if (!state.alive) return;
    if (typeof ctx?.api !== 'function' || typeof ctx?.runAction !== 'function') {
      setStatus('Host context missing api() or runAction()', true);
      return;
    }

    const epoch = ++state.scanEpoch;
    scanRecentBtn.disabled = true;
    setStatus('Loading recent flows (/api/flows?limit=50)...', false);

    try {
      const resp = await ctx.api('/api/flows?limit=50');
      if (!state.alive || epoch !== state.scanEpoch) return;

      const flows = Array.isArray(resp?.flows) ? resp.flows : (Array.isArray(resp) ? resp : []);
      if (flows.length === 0) {
        setStatus('No captured flows found yet.', false);
        renderAll();
        return;
      }

      // Prioritize flows already tagged by hooks/on_flow.star.
      const prioritized = [...flows].sort((a, b) => {
        const aTagged = Array.isArray(a.tags) && a.tags.includes('jwt') ? 1 : 0;
        const bTagged = Array.isArray(b.tags) && b.tags.includes('jwt') ? 1 : 0;
        return bTagged - aTagged;
      });

      const batchSize = 4;
      let scanned = 0;
      for (let i = 0; i < prioritized.length; i += batchSize) {
        if (!state.alive || epoch !== state.scanEpoch) return;
        const batch = prioritized.slice(i, i + batchSize);
        await Promise.all(batch.map(async (f) => {
          try {
            await inspectFlow(f);
          } catch {
            // Keep scanning remaining flows if one fails.
          }
        }));
        scanned += batch.length;
        if (!state.alive || epoch !== state.scanEpoch) return;
        setStatus(`Scanned ${scanned}/${prioritized.length} flows (${state.items.length} JWTs found)...`, false);
        renderAll();
      }

      setStatus(`Scan complete: ${state.items.length} JWT(s) across ${prioritized.length} flow(s).`, false);
    } catch (err) {
      if (!state.alive || epoch !== state.scanEpoch) return;
      setStatus(`Scan failed: ${err?.message || String(err)}`, true);
    } finally {
      if (state.alive) scanRecentBtn.disabled = false;
    }
  }

  inspectSelBtn.addEventListener('click', async () => {
    if (typeof ctx?.getSelectedFlow !== 'function') {
      setStatus('Host context does not provide getSelectedFlow()', true);
      return;
    }
    setStatus('Inspecting selected flow...', false);
    try {
      const flow = normalizeFlow(await Promise.resolve(ctx.getSelectedFlow()));
      if (!state.alive) return;
      if (!flow) {
        setStatus('No flow is currently selected in Proxy History.', false);
        return;
      }
      const added = await inspectFlow(flow);
      if (!state.alive) return;
      if (added.length > 0) {
        state.selectedKey = added[0]._key;
      }
      renderAll();
      setStatus(`Selected flow #${flow.id ?? '?'}: ${added.length} JWT(s) decoded.`, false);
    } catch (err) {
      if (!state.alive) return;
      setStatus(`Inspection failed: ${err?.message || String(err)}`, true);
    }
  });

  scanRecentBtn.addEventListener('click', () => {
    void scanRecentFlows();
  });

  decodeForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const token = tokenInput.value.trim();
    if (!token) {
      setStatus('Paste a JWT before clicking Decode Token.', true);
      return;
    }
    if (typeof ctx?.runAction !== 'function') {
      setStatus('Host context missing runAction()', true);
      return;
    }
    setStatus('Decoding pasted token...', false);
    try {
      const rawRes = await ctx.runAction('analyze_flow', { token });
      if (!state.alive) return;
      const data = unwrapResult(rawRes);
      if (data?.error && (!data.tokens || data.tokens.length === 0)) {
        setStatus(data.error, true);
        return;
      }
      const added = mergeAnalysis(rawRes, null, null);
      if (added.length > 0) {
        state.selectedKey = added[0]._key;
        renderAll();
        setStatus(`Decoded ${added.length} token(s) from input.`, false);
      } else {
        setStatus('No valid three-segment JWT found in input.', true);
      }
    } catch (err) {
      if (!state.alive) return;
      setStatus(`Decode failed: ${err?.message || String(err)}`, true);
    }
  });

  if (typeof ctx?.onSse === 'function') {
    const unsub = ctx.onSse('flow.new', async (ev) => {
      if (!state.alive) return;
      let parsed = ev;
      if (typeof ev === 'string') {
        try {
          parsed = JSON.parse(ev);
        } catch {
          return;
        }
      }
      const flow = normalizeFlow(parsed);
      if (!flow || flow.id == null) return;
      const tags = Array.isArray(flow.tags) ? flow.tags : [];
      if (tags.includes('jwt') || tags.includes('jwt-alg-none')) {
        try {
          const added = await inspectFlow(flow);
          if (state.alive && added.length > 0) {
            renderAll();
            setStatus(`Live capture #${flow.id}: ${added.length} JWT(s) detected.`, false);
          }
        } catch {
          // Ignore transient errors on background live updates.
        }
      }
    });
    if (typeof unsub === 'function') {
      state.cleanups.push(unsub);
    } else if (typeof ctx.offSse === 'function') {
      state.cleanups.push(() => ctx.offSse('flow.new'));
    }
  }

  renderAll();

  let handledActionFlowId = null;
  if (ctx?.actionResult !== undefined || ctx?.result !== undefined) {
    const initialFlow = normalizeFlow(ctx.flow || (await Promise.resolve(ctx.getSelectedFlow?.())));
    const added = mergeAnalysis(
      ctx.actionResult !== undefined ? ctx.actionResult : ctx.result,
      initialFlow,
      initialFlow?.id
    );
    handledActionFlowId = initialFlow?.id ?? null;
    if (added.length > 0) {
      state.selectedKey = added[0]._key;
      renderAll();
    }
  }

  if (state.alive && typeof ctx?.getSelectedFlow === 'function') {
    try {
      const sel = normalizeFlow(await Promise.resolve(ctx.getSelectedFlow()));
      if (state.alive && sel && sel.id !== handledActionFlowId) {
        const added = await inspectFlow(sel);
        if (state.alive && added.length > 0) {
          state.selectedKey = added[0]._key;
          renderAll();
        }
      }
    } catch {
      // Ignore selection lookup errors on mount; scanRecentFlows handles the list.
    }
  }

  if (state.alive && typeof ctx?.api === 'function') {
    await scanRecentFlows();
  }
}

export function unmount(container) {
  const state = instances.get(container);
  if (state) {
    state.alive = false;
    state.scanEpoch += 1;
    for (const fn of state.cleanups) {
      try {
        fn();
      } catch {
        // Ignore SSE teardown errors.
      }
    }
    state.cleanups = [];
    instances.delete(container);
  }
  if (container && typeof container.replaceChildren === 'function') {
    container.replaceChildren();
  }
}
