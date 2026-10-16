/* Interseptor search palette, on every page.
   Opens from the header search button, "/", Cmd/Ctrl+K, and any [data-search-open]
   element (optional data-search-query prefills the field). The index
   (website/data/search.json: title, text, url, page, group) loads on first use.
   The palette is a modal <dialog> holding a combobox and a grouped listbox: focus stays in
   the field, arrow keys move through results, Enter opens one and Escape closes. */
(() => {
  'use strict';
  const config = document.currentScript?.dataset || {};
  const base = new URL(document.body.dataset.baseurl || '/', location.href);
  const indexURL = new URL(config.searchIndex || 'website/data/search.json', base);
  const mascotURL = config.searchMascot || '';
  const groupColors = new Map((config.searchGroups || '').split('|').filter(Boolean).map(pair => pair.split('=')));
  const SUGGESTIONS = ['Install', 'CA certificate', 'Intercept', 'Findings', 'MCP', 'Collections'];
  const MAX_RESULTS = 30;
  const MAX_PER_PAGE = 5;

  const svg = paths => `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`;
  const ICONS = {
    search: svg('<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>'),
    close: svg('<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'),
    enter: svg('<polyline points="9 10 4 15 9 20"/><path d="M20 4v7a4 4 0 0 1-4 4H4"/>'),
    retry: svg('<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>'),
    grid: svg('<rect width="7" height="7" x="3" y="3" rx="1"/><rect width="7" height="7" x="14" y="3" rx="1"/><rect width="7" height="7" x="14" y="14" rx="1"/><rect width="7" height="7" x="3" y="14" rx="1"/>'),
  };

  const isMac = /Mac|iPhone|iPad|iPod/i.test(navigator.userAgentData?.platform || navigator.platform || navigator.userAgent);
  document.querySelectorAll('.search-trigger-key').forEach(key => { key.textContent = isMac ? '⌘K' : 'Ctrl K'; });

  // Without scripts the header trigger is a link to the feature index; here it becomes a button.
  document.querySelectorAll('a.search-trigger').forEach(link => {
    const button = document.createElement('button');
    button.type = 'button';
    for (const attr of link.attributes) if (attr.name !== 'href') button.setAttribute(attr.name, attr.value);
    button.setAttribute('aria-haspopup', 'dialog');
    button.append(...link.childNodes);
    link.replaceWith(button);
  });

  /* ---------- Index ---------- */
  let index = null;
  let loading = null;
  let failed = false;
  const fold = s => String(s || '').toLowerCase().normalize('NFKD').replace(/[\u0300-\u036f]/g, '');
  function load() {
    if (index || loading) return loading;
    failed = false;
    loading = fetch(indexURL, { credentials: 'same-origin' })
      .then(response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.json();
      })
      .then(data => {
        if (!Array.isArray(data)) throw new Error('The search index is not a list.');
        index = data
          .filter(item => typeof item.title === 'string' && typeof item.url === 'string')
          .map((item, order) => ({ ...item, order, _title: fold(item.title), _text: fold(item.text), _page: fold(item.page) }));
      })
      .catch(() => { failed = true; })
      .finally(() => {
        loading = null;
        if (dialog?.open) render();
      });
    return loading;
  }

  /* ---------- Ranking: titles first, then pages, then body text ---------- */
  const escapeRegExp = s => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  function occurrences(haystack, term) {
    let count = 0;
    for (let at = haystack.indexOf(term); at !== -1 && count < 5; at = haystack.indexOf(term, at + term.length)) count++;
    return count;
  }
  function rank(item, terms, phrase) {
    let score = 0;
    let titleHits = 0;
    for (const term of terms) {
      const inTitle = item._title.includes(term);
      const inPage = item._page.includes(term);
      const inText = item._text.includes(term);
      if (!inTitle && !inPage && !inText) return null;
      if (inTitle) {
        titleHits++;
        score += 10;
        if (item._title.startsWith(term) || item._title.includes(` ${term}`)) score += 4;
      }
      if (inPage) score += 3;
      if (inText) score += 1 + occurrences(item._text, term) * 0.5;
    }
    if (item._title === phrase) score += 100;
    else if (item._title.startsWith(phrase)) score += 40;
    else if (item._title.includes(phrase)) score += 20;
    if (terms.length > 1 && item._text.includes(phrase)) score += 6;
    if (!item.url.includes('#')) score += 1;
    const tier = titleHits === terms.length ? 2 : titleHits ? 1 : 0;
    return { item, tier, score };
  }
  function search(query) {
    const phrase = fold(query).trim().replace(/\s+/g, ' ');
    const terms = [...new Set(phrase.split(' ').filter(Boolean))];
    if (!terms.length) return { terms, groups: [], total: 0 };
    const hits = index.map(item => rank(item, terms, phrase)).filter(Boolean)
      .sort((a, b) => b.tier - a.tier || b.score - a.score || a.item.order - b.item.order);
    const groups = new Map();
    let shown = 0;
    for (const { item } of hits) {
      if (shown >= MAX_RESULTS) break;
      const key = item.page || item.title;
      if (!groups.has(key)) groups.set(key, { page: key, group: item.group || '', items: [] });
      const group = groups.get(key);
      if (group.items.length >= MAX_PER_PAGE) continue;
      group.items.push(item);
      shown++;
    }
    return { terms, groups: [...groups.values()], total: hits.length };
  }

  /* ---------- Highlighting ---------- */
  function highlight(target, text, terms) {
    target.replaceChildren();
    if (!terms.length) { target.textContent = text; return; }
    const pattern = new RegExp(`(${terms.slice().sort((a, b) => b.length - a.length).map(escapeRegExp).join('|')})`, 'gi');
    let last = 0;
    for (const match of text.matchAll(pattern)) {
      if (match.index > last) target.append(text.slice(last, match.index));
      const mark = document.createElement('mark');
      mark.textContent = match[0];
      target.append(mark);
      last = match.index + match[0].length;
    }
    if (last < text.length) target.append(text.slice(last));
  }
  function excerpt(text, terms) {
    const clean = String(text || '').replace(/\s+/g, ' ').trim();
    const lower = clean.toLowerCase();
    let at = -1;
    for (const term of terms) {
      const found = lower.indexOf(term);
      if (found !== -1 && (at === -1 || found < at)) at = found;
    }
    let start = Math.max(0, at - 48);
    if (start > 0) {
      const space = clean.indexOf(' ', start);
      start = space !== -1 && space < at ? space + 1 : start;
    }
    const end = Math.min(clean.length, start + 170);
    return (start > 0 ? '…' : '') + clean.slice(start, end) + (end < clean.length ? '…' : '');
  }

  /* ---------- The palette ---------- */
  let dialog;
  let input;
  let bodyEl;
  let listbox;
  let statusEl;
  let options = [];
  let active = -1;
  let opener = null;
  let announceTimer;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function build() {
    dialog = el('dialog', 'search-palette');
    dialog.setAttribute('aria-label', 'Search the docs');
    dialog.innerHTML = `
      <div class="search-head">
        <div class="search-field">
          ${ICONS.search}
          <input class="search-input" type="search" role="combobox" aria-label="Search the docs" aria-autocomplete="list" aria-expanded="false" aria-controls="search-listbox" placeholder="Search docs" autocomplete="off" autocapitalize="off" spellcheck="false" enterkeyhint="go">
          <kbd class="search-esc" aria-hidden="true">Esc</kbd>
        </div>
        <button class="icon-btn search-close" type="button">${ICONS.close}<span class="sr-only">Close search</span></button>
      </div>
      <div class="search-body">
        <div class="search-panel"></div>
        <div class="search-results" id="search-listbox" role="listbox" aria-label="Search results"></div>
      </div>
      <div class="search-foot" aria-hidden="true">
        <span><kbd>↑</kbd><kbd>↓</kbd> move</span>
        <span><kbd>Enter</kbd> open</span>
        <span><kbd>Esc</kbd> close</span>
      </div>
      <div class="sr-only" role="status" aria-atomic="true"></div>`;
    document.body.append(dialog);
    input = dialog.querySelector('.search-input');
    bodyEl = dialog.querySelector('.search-panel');
    listbox = dialog.querySelector('.search-results');
    statusEl = dialog.querySelector('[role="status"]');

    input.addEventListener('input', render);
    input.addEventListener('keydown', onKey);
    dialog.querySelector('.search-close').addEventListener('click', close);
    dialog.addEventListener('keydown', event => {
      if (event.key === 'Escape') { event.preventDefault(); close(); return; }
      if (event.key === 'Tab') trap(event);
    });
    dialog.addEventListener('cancel', event => { event.preventDefault(); close(); });
    dialog.addEventListener('close', () => {
      clearTimeout(announceTimer);
      const target = opener;
      opener = null;
      if (target && target.isConnected && typeof target.focus === 'function') target.focus({ preventScroll: true });
    });
    dialog.addEventListener('click', event => {
      if (event.target !== dialog) return;
      const box = dialog.getBoundingClientRect();
      const inside = event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom;
      if (!inside) close();
    });
    // Only a pointer that really moved picks an option; scrolling with the arrow keys can
    // fire pointer events under a resting mouse.
    let lastPointer = '';
    listbox.addEventListener('pointermove', event => {
      const at = `${event.clientX},${event.clientY}`;
      if (at === lastPointer) return;
      lastPointer = at;
      const option = event.target.closest('[role="option"]');
      if (option) setActive(options.indexOf(option), false);
    });
    listbox.addEventListener('click', event => {
      const option = event.target.closest('[role="option"]');
      if (!option) return;
      go(option, event.metaKey || event.ctrlKey || event.shiftKey);
    });
    // Middle-click opens a result in a new tab, as a link would.
    listbox.addEventListener('auxclick', event => {
      const option = event.target.closest('[role="option"]');
      if (option && event.button === 1) go(option, true);
    });
  }
  function trap(event) {
    const items = [...dialog.querySelectorAll('input, button:not([disabled]), a[href]:not([tabindex="-1"])')].filter(node => node.getClientRects().length > 0);
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
  function announce(message, delay = 450) {
    clearTimeout(announceTimer);
    announceTimer = setTimeout(() => { statusEl.textContent = message; }, delay);
  }

  function setActive(next, scroll = true) {
    if (!options.length) { active = -1; input.removeAttribute('aria-activedescendant'); return; }
    active = (next + options.length) % options.length;
    options.forEach((option, i) => option.setAttribute('aria-selected', String(i === active)));
    input.setAttribute('aria-activedescendant', options[active].id);
    if (scroll) options[active].scrollIntoView({ block: 'nearest' });
  }
  function onKey(event) {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (!options.length) return;
      event.preventDefault();
      setActive(active + (event.key === 'ArrowDown' ? 1 : -1));
    } else if (event.key === 'Enter') {
      if (!options.length || event.isComposing) return;
      event.preventDefault();
      go(options[Math.max(0, active)], event.metaKey || event.ctrlKey);
    }
  }
  function go(option, newTab = false) {
    const href = option?.dataset.href;
    if (!href) return;
    if (newTab) { window.open(href, '_blank', 'noopener'); return; }
    const url = new URL(href);
    const samePage = url.origin === location.origin && url.pathname === location.pathname;
    if (samePage && url.hash) {
      const target = document.getElementById(decodeURIComponent(url.hash.slice(1)));
      opener = null;
      close();
      if (target) {
        history.pushState(null, '', url.hash);
        target.scrollIntoView({ block: 'start' });
        if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1');
        target.focus({ preventScroll: true });
        return;
      }
    }
    location.assign(url.href);
  }

  /* ---------- States ---------- */
  function message({ title, text, mascot = false, actions = [] }) {
    const box = el('div', 'search-message');
    if (mascot && mascotURL) {
      const img = el('img', 'search-mascot');
      img.src = mascotURL;
      img.alt = '';
      img.width = 112;
      img.height = 112;
      img.decoding = 'async';
      box.append(img);
    }
    box.append(el('p', 'search-message-title', title));
    if (text) box.append(el('p', '', text));
    if (actions.length) {
      const row = el('div', 'search-suggest');
      row.append(...actions);
      box.append(row);
    }
    return box;
  }
  function suggestionChips() {
    return SUGGESTIONS.map(word => {
      const chip = el('button', 'chip', word);
      chip.type = 'button';
      chip.addEventListener('click', () => { input.value = word; input.focus(); render(); });
      return chip;
    });
  }
  function clearResults() {
    options = [];
    active = -1;
    listbox.replaceChildren();
    listbox.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    input.removeAttribute('aria-activedescendant');
  }

  function render() {
    const query = input.value.trim();
    bodyEl.replaceChildren();
    clearResults();
    if (!query) {
      bodyEl.append(message({ title: 'Search every guide', text: 'Find a setting, a feature or a fix across the docs and the feature index.', actions: suggestionChips() }));
      announce('', 0);
      return;
    }
    if (!index) {
      if (failed) {
        const retry = el('button', 'btn-ghost btn-sm');
        retry.type = 'button';
        retry.innerHTML = `${ICONS.retry}<span>Try again</span>`;
        retry.addEventListener('click', () => { input.focus(); load(); render(); });
        bodyEl.append(message({ title: 'Search did not load', text: 'The search index could not be fetched. Check your connection, then try again.', actions: [retry] }));
        announce('Search did not load. Check your connection, then try again.', 0);
      } else {
        bodyEl.append(message({ title: 'Loading the search index…' }));
        announce('Loading the search index.', 0);
        load();
      }
      return;
    }
    const { terms, groups, total } = search(query);
    if (!groups.length) {
      const browse = el('a', 'chip');
      browse.href = new URL('features/', base).href;
      browse.innerHTML = `${ICONS.grid}<span>Browse all features</span>`;
      bodyEl.append(message({ title: `No results for “${query}”`, text: 'Try a shorter word, or a feature name such as Intruder, Scope or Findings.', mascot: true, actions: [browse] }));
      announce(`No results for ${query}.`);
      return;
    }
    let n = 0;
    groups.forEach((group, g) => {
      const list = el('ul', 'search-group');
      list.setAttribute('role', 'group');
      list.setAttribute('aria-labelledby', `search-group-${g}`);
      const color = groupColors.get(group.group);
      if (color) list.dataset.color = color;
      const title = el('li', 'search-group-title');
      title.id = `search-group-${g}`;
      title.setAttribute('role', 'presentation');
      title.append(el('span', 'search-group-dot'), el('span', '', group.page));
      if (group.group) {
        const name = el('span', 'search-group-name', group.group);
        name.setAttribute('aria-hidden', 'true');
        title.append(name);
      }
      list.append(title);
      for (const item of group.items) {
        const option = el('li', 'search-option');
        option.id = `search-option-${n}`;
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', 'false');
        option.dataset.href = new URL(item.url.replace(/^\/+/, ''), base).href;
        const card = el('span', 'search-option-body');
        const name = el('span', 'search-option-title');
        name.id = `search-option-${n}-title`;
        highlight(name, item.title, terms);
        const text = el('span', 'search-option-text');
        text.id = `search-option-${n}-text`;
        // A page's introduction repeats its title first; the excerpt starts after it.
        const body = item.text?.startsWith(item.title) ? item.text.slice(item.title.length) : item.text;
        highlight(text, excerpt(body, terms), terms);
        const enter = el('span', 'search-option-enter');
        enter.innerHTML = ICONS.enter;
        enter.setAttribute('aria-hidden', 'true');
        card.append(name, enter, text);
        option.setAttribute('aria-labelledby', name.id);
        option.setAttribute('aria-describedby', text.id);
        option.append(card);
        list.append(option);
        options.push(option);
        n++;
      }
      listbox.append(list);
    });
    listbox.hidden = false;
    input.setAttribute('aria-expanded', 'true');
    setActive(0, false);
    listbox.parentElement.scrollTop = 0;
    const shown = options.length < total ? `, showing the top ${options.length}` : '';
    announce(`${total} result${total === 1 ? '' : 's'} for ${query}${shown}. Use the arrow keys to move and Enter to open.`);
  }

  function open(query) {
    if (!dialog) build();
    document.querySelectorAll('dialog[open]').forEach(other => { if (other !== dialog) other.close(); });
    if (!dialog.open) {
      opener = document.activeElement;
      dialog.showModal();
    }
    if (typeof query === 'string' && query) input.value = query;
    input.focus();
    input.select();
    load();
    render();
  }
  function close() { if (dialog?.open) dialog.close(); }

  /* ---------- Openers ---------- */
  document.addEventListener('click', event => {
    const trigger = event.target.closest?.('[data-search-open]');
    if (!trigger || event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault();
    open(trigger.dataset.searchQuery);
  });
  document.addEventListener('keydown', event => {
    if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      if (dialog?.open) close(); else open();
      return;
    }
    if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented) return;
    if (event.target.closest?.('input, textarea, select, [contenteditable]:not([contenteditable="false"])')) return;
    if (document.querySelector('dialog[open]')) return;
    event.preventDefault();
    open();
  });
  // Warm the index when someone heads for the search button.
  const warm = event => { if (event.target.closest?.('[data-search-open]')) load(); };
  document.addEventListener('pointerover', warm, { passive: true });
  document.addEventListener('focusin', warm);
  // A link that opens the palette (the 404 search box) is announced as the button it
  // becomes once scripting runs; without scripting it stays a plain link.
  document.querySelectorAll('a[data-search-open]').forEach(link => {
    link.setAttribute('role', 'button');
    link.setAttribute('aria-haspopup', 'dialog');
    link.addEventListener('keydown', event => {
      if (event.key !== ' ') return;
      event.preventDefault();
      open(link.dataset.searchQuery);
    });
  });
})();
