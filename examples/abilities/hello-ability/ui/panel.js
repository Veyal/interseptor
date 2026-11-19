// Hello Ability panel: demonstrates selection inspection, action invocation,
// live flow.new SSE updates, and clean teardown on unmount.

const instances = new WeakMap();

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function normalizeFlow(raw) {
  if (!raw || typeof raw !== 'object') return null;
  return raw.flow && typeof raw.flow === 'object' ? raw.flow : raw;
}

function formatFlowLabel(flow) {
  if (!flow) return 'None';
  const method = String(flow.method || 'GET').toUpperCase();
  const host = String(flow.host || '');
  const path = String(flow.path || '/');
  const status = flow.status ? ` [${flow.status}]` : '';
  const idPrefix = flow.id != null ? `#${flow.id} ` : '';
  return `${idPrefix}${method} ${host}${path}${status}`.trim();
}

export async function mount(container, ctx) {
  unmount(container);

  const state = {
    alive: true,
    cleanups: [],
    selectedFlow: null,
    liveFlows: [],
    totalSeen: 0,
  };
  instances.set(container, state);

  const root = el('div', 'ab-hello');

  // Header
  const head = el('header', 'ab-hello-head');
  const titleWrap = el('div', 'ab-hello-title-wrap');
  titleWrap.append(
    el('h2', 'ab-hello-title', 'Hello Ability'),
    el('p', 'ab-hello-sub', 'Minimal starter Copy Ability: selected flow summary + live SSE feed')
  );

  const headActions = el('div', 'ab-hello-actions');
  const refreshBtn = el('button', 'ab-hello-btn', 'Refresh Selection');
  refreshBtn.type = 'button';
  const summarizeBtn = el('button', 'ab-hello-btn is-primary', 'Summarize Flow');
  summarizeBtn.type = 'button';
  headActions.append(refreshBtn, summarizeBtn);
  head.append(titleWrap, headActions);

  // Grid
  const grid = el('div', 'ab-hello-grid');

  // Selected flow + action output card
  const selCard = el('section', 'ab-hello-card');
  const selHead = el('div', 'ab-hello-card-head');
  const selTitle = el('h3', 'ab-hello-card-title', 'Selected Flow');
  const selBadge = el('span', 'ab-hello-pill', 'none');
  selHead.append(selTitle, selBadge);

  const selBody = el('div');
  const statusMsg = el('p', 'ab-hello-status', 'Ready');
  const resultPre = el('pre', 'ab-hello-pre', 'Click "Summarize Flow" to run hooks/summarize.star');
  selCard.append(selHead, selBody, statusMsg, resultPre);

  // Live SSE feed card
  const feedCard = el('section', 'ab-hello-card');
  const feedHead = el('div', 'ab-hello-card-head');
  const feedTitle = el('h3', 'ab-hello-card-title', 'Live Traffic (flow.new)');
  const feedBadge = el('span', 'ab-hello-pill', '0 seen');
  feedHead.append(feedTitle, feedBadge);

  const feedBody = el('div');
  feedCard.append(feedHead, feedBody);

  grid.append(selCard, feedCard);
  root.append(head, grid);
  container.replaceChildren(root);

  function setStatus(text, isError) {
    statusMsg.textContent = text;
    statusMsg.classList.toggle('is-error', Boolean(isError));
  }

  function renderSelectedFlow() {
    const flow = state.selectedFlow;
    selBody.replaceChildren();
    if (!flow) {
      selBadge.textContent = 'none';
      selBadge.classList.remove('is-accent');
      summarizeBtn.disabled = true;
      selBody.append(el('p', 'ab-hello-empty', 'Select a flow in Proxy History or click a live flow item.'));
      return;
    }

    selBadge.textContent = flow.id != null ? `#${flow.id}` : 'selected';
    selBadge.classList.add('is-accent');
    summarizeBtn.disabled = false;

    const dl = el('dl', 'ab-hello-kv');
    const rows = [
      ['Method', String(flow.method || '').toUpperCase() || '-'],
      ['Host', String(flow.host || '-')],
      ['Path', String(flow.path || '/')],
      ['Status', flow.status ? String(flow.status) : 'pending'],
      ['Tags', Array.isArray(flow.tags) && flow.tags.length ? flow.tags.join(', ') : '(none)'],
    ];
    for (const [k, v] of rows) {
      dl.append(el('dt', '', k), el('dd', '', v));
    }
    selBody.append(dl);
  }

  function renderResult(res) {
    const data = res && res.result !== undefined ? res.result : res;
    if (!data || typeof data !== 'object') {
      resultPre.textContent = String(data ?? '(empty result)');
      return;
    }
    resultPre.textContent = JSON.stringify(data, null, 2);
  }

  async function runSummarize(flowId) {
    if (!state.alive) return;
    if (typeof ctx?.runAction !== 'function') {
      setStatus('Host context does not expose runAction()', true);
      return;
    }
    setStatus(`Running summarize_flow${flowId != null ? ` on #${flowId}` : ''}...`, false);
    summarizeBtn.disabled = true;
    try {
      const payload = flowId != null ? { flowId } : {};
      const res = await ctx.runAction('summarize_flow', payload);
      if (!state.alive) return;
      renderResult(res);
      setStatus('Action completed', false);
    } catch (err) {
      if (!state.alive) return;
      setStatus(`Action failed: ${err?.message || String(err)}`, true);
    } finally {
      if (state.alive) {
        summarizeBtn.disabled = !state.selectedFlow;
      }
    }
  }

  function renderLiveFeed() {
    feedBadge.textContent = `${state.totalSeen} seen`;
    feedBadge.classList.toggle('is-accent', state.totalSeen > 0);
    feedBody.replaceChildren();

    if (!state.liveFlows.length) {
      feedBody.append(el('p', 'ab-hello-empty', 'Waiting for new flows via SSE (flow.new)...'));
      return;
    }

    const list = el('ul', 'ab-hello-feed');
    for (const f of state.liveFlows) {
      const item = el('li', 'ab-hello-feed-item');
      const label = el('span', 'ab-hello-feed-label', formatFlowLabel(f));
      label.title = formatFlowLabel(f);

      const btn = el('button', 'ab-hello-btn', 'Summarize');
      btn.type = 'button';
      btn.addEventListener('click', () => {
        state.selectedFlow = f;
        renderSelectedFlow();
        if (f.id != null) {
          void runSummarize(f.id);
        }
      });

      item.append(label, btn);
      list.append(item);
    }
    feedBody.append(list);
  }

  async function refreshSelection() {
    if (!state.alive) return;
    if (typeof ctx?.getSelectedFlow !== 'function') {
      state.selectedFlow = null;
      renderSelectedFlow();
      return;
    }
    try {
      const raw = await Promise.resolve(ctx.getSelectedFlow());
      if (!state.alive) return;
      state.selectedFlow = normalizeFlow(raw);
      renderSelectedFlow();
    } catch (err) {
      if (!state.alive) return;
      setStatus(`Could not read selected flow: ${err?.message || String(err)}`, true);
    }
  }

  refreshBtn.addEventListener('click', () => {
    void refreshSelection();
  });

  summarizeBtn.addEventListener('click', () => {
    const flowId = state.selectedFlow?.id;
    void runSummarize(flowId);
  });

  if (typeof ctx?.onSse === 'function') {
    const unsub = ctx.onSse('flow.new', (ev) => {
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
      if (!flow) return;
      state.totalSeen += 1;
      state.liveFlows = [flow, ...state.liveFlows.filter((x) => x.id !== flow.id)].slice(0, 12);
      renderLiveFeed();
    });
    if (typeof unsub === 'function') {
      state.cleanups.push(unsub);
    } else if (typeof ctx.offSse === 'function') {
      state.cleanups.push(() => ctx.offSse('flow.new'));
    }
  }

  renderLiveFeed();
  await refreshSelection();

  // If opened from a flow context menu that already ran summarize_flow, show its output immediately.
  if (state.alive && (ctx?.actionResult !== undefined || ctx?.result !== undefined)) {
    renderResult(ctx.actionResult !== undefined ? ctx.actionResult : ctx.result);
    setStatus('Loaded context action result', false);
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
        // Ignore teardown errors from host SSE subscriptions.
      }
    }
    state.cleanups = [];
    instances.delete(container);
  }
  if (container && typeof container.replaceChildren === 'function') {
    container.replaceChildren();
  }
}
