// JWT X-Ray Flow Inspector tab: decodes JWTs found in the inspected flow's
// request and/or response and surfaces alg=none and claim warnings inline.

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

function severityLabel(sev) {
  switch (sev) {
    case 'high': return 'HIGH';
    case 'medium': return 'MED';
    case 'low': return 'LOW';
    default: return 'OK';
  }
}

function matchesSide(token, side) {
  if (!side || side === 'both') return true;
  const srcs = Array.isArray(token.sources) && token.sources.length
    ? token.sources
    : [token.source || ''];
  const prefix = side === 'req' ? 'req.' : 'res.';
  return srcs.some((s) => String(s).startsWith(prefix) || s === 'input');
}

function formatEpoch(sec) {
  if (typeof sec !== 'number' || !Number.isFinite(sec)) return null;
  try {
    return `${sec} (${new Date(sec * 1000).toISOString()})`;
  } catch {
    return String(sec);
  }
}

async function copyText(text, ctx) {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      ctx?.toast?.('Copied JWT to clipboard', 'success');
      return;
    }
  } catch {
    // Fall through.
  }
  ctx?.toast?.('Clipboard write unavailable in this context', 'warn');
}

export async function mount(container, ctx) {
  unmount(container);

  const state = {
    alive: true,
    ctx,
    showAllSides: false,
    tokens: [],
    cleanups: [],
  };
  instances.set(container, state);

  const root = el('div', 'ab-jwt is-compact');
  const head = el('header', 'ab-jwt-head');
  const titleWrap = el('div', 'ab-jwt-title-wrap');
  const titleEl = el('h3', 'ab-jwt-title', 'JWT Inspector');
  const subEl = el('p', 'ab-jwt-sub', 'Checking flow for JWTs...');
  titleWrap.append(titleEl, subEl);

  const actions = el('div', 'ab-jwt-actions');
  const sideBtn = el('button', 'ab-jwt-btn is-small', 'Show Both Sides');
  sideBtn.type = 'button';
  sideBtn.hidden = true;
  const reanalyzeBtn = el('button', 'ab-jwt-btn is-small', 'Refresh');
  reanalyzeBtn.type = 'button';
  actions.append(sideBtn, reanalyzeBtn);
  head.append(titleWrap, actions);

  const body = el('div', 'ab-jwt-list');
  root.append(head, body);
  container.replaceChildren(root);

  function renderTokens() {
    body.replaceChildren();
    const side = state.ctx?.side || 'both';
    const visible = state.showAllSides || side === 'both'
      ? state.tokens
      : state.tokens.filter((t) => matchesSide(t, side));

    if (side !== 'both' && state.tokens.length > visible.length) {
      sideBtn.hidden = false;
      sideBtn.textContent = state.showAllSides
        ? `Filter (${side.toUpperCase()})`
        : `Show All (${state.tokens.length})`;
    } else if (state.showAllSides && side !== 'both') {
      sideBtn.hidden = false;
      sideBtn.textContent = `Filter (${side.toUpperCase()})`;
    } else {
      sideBtn.hidden = true;
    }

    if (visible.length === 0) {
      const msg = state.tokens.length > 0 && !state.showAllSides
        ? `No JWTs on the ${side === 'req' ? 'request' : 'response'} side (${state.tokens.length} on the other side).`
        : 'No JWTs detected in this flow.';
      subEl.textContent = msg;
      body.append(el('p', 'ab-jwt-empty', msg));
      return;
    }

    subEl.textContent = `${visible.length} JWT(s) decoded`;

    for (const item of visible) {
      const card = el('section', 'ab-jwt-card');
      const cardHead = el('div', 'ab-jwt-card-head');
      const meta = el('div', 'ab-jwt-card-meta');
      meta.append(
        el('span', `ab-jwt-badge sev-${item.severity || 'info'}`, severityLabel(item.severity)),
        el('span', 'ab-jwt-badge', `alg: ${item.alg || '(none)'}`),
        el('span', 'ab-jwt-badge', Array.isArray(item.sources) && item.sources.length ? item.sources.join(', ') : (item.source || 'flow'))
      );

      const copyBtn = el('button', 'ab-jwt-btn is-small', 'Copy');
      copyBtn.type = 'button';
      copyBtn.addEventListener('click', () => {
        void copyText(item.raw, state.ctx);
      });
      cardHead.append(meta, copyBtn);
      card.append(cardHead);

      if (Array.isArray(item.warnings) && item.warnings.length > 0) {
        const warnList = el('ul', 'ab-jwt-warn-list');
        for (const w of item.warnings) {
          const isHigh = item.severity === 'high' && (String(w).includes('none') || String(w).includes('Signature'));
          warnList.append(el('li', `ab-jwt-warn-item${isHigh ? ' is-high' : ''}`, w));
        }
        card.append(warnList);
      }

      const payload = item.payload && typeof item.payload === 'object' ? item.payload : {};
      const timingRows = [];
      for (const k of ['iss', 'sub', 'aud', 'exp', 'nbf', 'iat']) {
        if (payload[k] !== undefined) {
          const val = (k === 'exp' || k === 'nbf' || k === 'iat') && typeof payload[k] === 'number'
            ? formatEpoch(payload[k])
            : (typeof payload[k] === 'object' ? JSON.stringify(payload[k]) : String(payload[k]));
          timingRows.push([k, val]);
        }
      }
      if (timingRows.length > 0) {
        const dl = el('dl', 'ab-jwt-claims');
        for (const [k, v] of timingRows) {
          dl.append(el('dt', '', k), el('dd', '', v));
        }
        card.append(dl);
      }

      const jsonGrid = el('div', 'ab-jwt-json-grid');
      const hdrCol = el('div');
      hdrCol.append(
        el('h4', 'ab-jwt-sec-title', 'Header'),
        el('pre', 'ab-jwt-pre', JSON.stringify(item.header || {}, null, 2))
      );
      const payCol = el('div');
      payCol.append(
        el('h4', 'ab-jwt-sec-title', 'Payload'),
        el('pre', 'ab-jwt-pre', JSON.stringify(payload, null, 2))
      );
      jsonGrid.append(hdrCol, payCol);
      card.append(jsonGrid);

      body.append(card);
    }
  }

  async function analyzeCurrent() {
    if (!state.alive) return;
    if (typeof state.ctx?.runAction !== 'function') {
      subEl.textContent = 'Host context does not expose runAction()';
      return;
    }

    subEl.textContent = 'Analyzing flow JWTs...';
    reanalyzeBtn.disabled = true;
    try {
      const flow = normalizeFlow(
        state.ctx.flow || (await Promise.resolve(state.ctx.getSelectedFlow?.()))
      );
      if (!state.alive) return;

      let actionInput = null;
      if (flow?.id != null) {
        actionInput = { flowId: flow.id };
      } else if (flow) {
        actionInput = { flow };
      } else if (state.ctx.raw || state.ctx.text || state.ctx.body) {
        actionInput = { token: String(state.ctx.raw || state.ctx.text || state.ctx.body) };
      }

      if (!actionInput) {
        state.tokens = [];
        renderTokens();
        return;
      }

      const rawRes = await state.ctx.runAction('analyze_flow', actionInput);
      if (!state.alive) return;
      const data = unwrapResult(rawRes);
      state.tokens = Array.isArray(data?.tokens) ? data.tokens : [];
      renderTokens();
    } catch (err) {
      if (!state.alive) return;
      subEl.textContent = `JWT analysis failed: ${err?.message || String(err)}`;
      body.replaceChildren(el('p', 'ab-jwt-status is-error', subEl.textContent));
    } finally {
      if (state.alive) reanalyzeBtn.disabled = false;
    }
  }

  sideBtn.addEventListener('click', () => {
    state.showAllSides = !state.showAllSides;
    renderTokens();
  });

  reanalyzeBtn.addEventListener('click', () => {
    void analyzeCurrent();
  });

  state.analyzeCurrent = analyzeCurrent;
  await analyzeCurrent();
}

export async function update(container, ctx) {
  const state = instances.get(container);
  if (!state) {
    return mount(container, ctx);
  }
  state.ctx = ctx;
  if (typeof state.analyzeCurrent === 'function') {
    await state.analyzeCurrent();
  }
}

export function unmount(container) {
  const state = instances.get(container);
  if (state) {
    state.alive = false;
    for (const fn of state.cleanups) {
      try {
        fn();
      } catch {
        // Ignore teardown errors.
      }
    }
    state.cleanups = [];
    instances.delete(container);
  }
  if (container && typeof container.replaceChildren === 'function') {
    container.replaceChildren();
  }
}
