/* Interseptor pages: the features filter, the compare matrix's scroll cue and the 404's request line.
   Every page works without this file: the features grid shows all 32 abilities, the matrix scrolls
   on its own, and the 404 keeps its search link and its chips. */
(() => {
  'use strict';

  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
  // Lower case with accents folded, so "proxy" finds "Proxy" and "cafe" finds "café".
  const fold = (text) => String(text).normalize('NFKD').replace(/[\u0300-\u036f]/g, '').toLowerCase();
  const debounce = (fn, wait) => {
    let timer = 0;
    return (...args) => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => fn(...args), wait);
    };
  };

  /* ---------- Features: filter the abilities by words and by theme ---------- */
  function initAbilities() {
    const tools = $('[data-ability-tools]');
    const grid = $('#ability-grid');
    const input = $('#ability-filter');
    if (!tools || !grid || !input) return;

    const cards = $$('.ability', grid);
    const cap = $('[data-ability-cap]', grid);
    const clear = $('[data-ability-clear]', tools);
    const shownEl = $('[data-ability-shown]', tools);
    const status = $('#ability-status');
    const radios = $$('input[name="ability-theme"]', tools);
    const empty = $('[data-ability-empty]');
    const queryEl = empty && $('[data-ability-query]', empty);
    const reset = empty && $('[data-ability-reset]', empty);
    const palette = empty && $('[data-search-open]', empty);
    const total = cards.length;

    const items = cards.map((card) => ({
      card,
      theme: card.dataset.topic || '',
      text: fold([
        $('h2', card)?.textContent,
        $('.ability-text', card)?.textContent,
        $('.ability-theme', card)?.textContent,
      ].join(' ')),
    }));

    const theme = () => (radios.find((radio) => radio.checked) || { value: 'all' }).value;
    const setTheme = (value) => radios.forEach((radio) => { radio.checked = radio.value === value; });

    const announce = debounce((message) => { if (status) status.textContent = message; }, 450);

    function apply({ speak = true } = {}) {
      const terms = fold(input.value).split(/\s+/).filter(Boolean);
      const chosen = theme();
      let shown = 0;
      for (const item of items) {
        const match = (chosen === 'all' || item.theme === chosen) && terms.every((term) => item.text.includes(term));
        item.card.hidden = !match;
        if (match) shown += 1;
      }
      const filtering = terms.length > 0 || chosen !== 'all';
      if (shownEl) shownEl.textContent = String(shown);
      if (clear) clear.hidden = input.value === '';
      if (cap) cap.hidden = filtering;
      if (empty) {
        empty.hidden = shown !== 0;
        if (queryEl) queryEl.textContent = input.value.trim();
        if (palette) palette.dataset.searchQuery = input.value.trim();
      }
      if (speak) {
        announce(!filtering ? '' : shown === 0 ? 'No abilities match.' : `${shown} of ${total} abilities shown.`);
      }
    }

    function clearAll() {
      input.value = '';
      setTheme('all');
      apply();
    }

    input.addEventListener('input', () => apply());
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && input.value !== '') {
        event.preventDefault();
        input.value = '';
        apply();
      }
    });
    clear?.addEventListener('click', () => { input.value = ''; apply(); input.focus(); });
    radios.forEach((radio) => radio.addEventListener('change', () => apply()));
    reset?.addEventListener('click', () => { clearAll(); input.focus(); });

    // /features/?q=agent starts with the filter filled in.
    try {
      const initial = new URLSearchParams(window.location.search).get('q');
      if (initial) input.value = initial.slice(0, 80);
    } catch (_) { /* a malformed query string just means no preset filter */ }
    apply({ speak: false });

    // A link to a card that the filter is hiding (the search palette, a bookmark) clears the filter.
    function revealTarget() {
      let id = '';
      try { id = decodeURIComponent(window.location.hash.slice(1)); } catch (_) { return; }
      if (!id) return;
      const target = document.getElementById(id);
      if (target && target.classList.contains('ability') && target.hidden) {
        clearAll();
        target.scrollIntoView({ block: 'start' });
      }
    }
    window.addEventListener('hashchange', revealTarget);
    revealTarget();
  }

  /* ---------- Compare: show the "scroll for more tools" cue only while it helps ---------- */
  function initMatrix() {
    const matrix = $('.matrix');
    const region = matrix && $('.matrix-scroll', matrix);
    if (!matrix || !region) return;
    let frame = 0;
    function measure() {
      frame = 0;
      const max = region.scrollWidth - region.clientWidth;
      const overflows = max > 2 || region.scrollHeight - region.clientHeight > 2;
      matrix.dataset.scrollable = String(max > 2);
      matrix.dataset.edge = max <= 2 ? 'none' : region.scrollLeft <= 2 ? 'start' : region.scrollLeft >= max - 2 ? 'end' : 'middle';
      // A region that scrolls is a tab stop, so the keyboard can scroll it; one that fits is not.
      if (overflows) region.setAttribute('tabindex', '0');
      else region.removeAttribute('tabindex');
      // The hint describes sideways scrolling, so it only describes the region while that is possible.
      if (max > 2) region.setAttribute('aria-describedby', 'matrix-hint');
      else region.removeAttribute('aria-describedby');
    }
    const queue = () => { if (!frame) frame = window.requestAnimationFrame(measure); };
    region.addEventListener('scroll', queue, { passive: true });
    window.addEventListener('resize', queue);
    if ('ResizeObserver' in window) {
      const observer = new ResizeObserver(queue);
      observer.observe(region);
      const table = $('table', region);
      if (table) observer.observe(table);
    }
    measure();

    // A link to a row (the scoreboard) in a frame that scrolls: bring the frame's top edge into view
    // and put the row just under its sticky header, so the column names stay on screen. On a wide
    // page the frame does not scroll and the browser's own jump is already right.
    function revealRow() {
      let id = '';
      try { id = decodeURIComponent(window.location.hash.slice(1)); } catch (_) { return; }
      const row = /^row-\d+$/.test(id) ? document.getElementById(id) : null;
      if (!row || !region.contains(row) || region.scrollHeight - region.clientHeight <= 2) return;
      const head = $('thead', region);
      const bar = head ? head.getBoundingClientRect().height : 0;
      const inside = row.getBoundingClientRect().top - region.getBoundingClientRect().top + region.scrollTop;
      region.scrollTop = Math.max(0, inside - bar - 8);
      const site = $('.site-header');
      const offset = (site ? site.getBoundingClientRect().height : 0) + 16;
      window.scrollTo({ top: window.scrollY + region.getBoundingClientRect().top - offset });
    }
    window.addEventListener('hashchange', () => window.requestAnimationFrame(revealRow));
    window.addEventListener('load', () => window.setTimeout(revealRow, 80));
    // Clicking the link for the row that is already in the address bar fires no hashchange.
    document.addEventListener('click', (event) => {
      if (event.target instanceof Element && event.target.closest('a[href^="#row-"]')) window.requestAnimationFrame(revealRow);
    });
  }

  /* ---------- 404: print the request that was dropped ---------- */
  function initNotFound() {
    const line = $('[data-request-line]');
    const path = line && $('[data-request-path]', line);
    if (!line || !path) return;
    const requested = window.location.pathname + window.location.search;
    path.textContent = requested.length > 140 ? `${requested.slice(0, 137)}...` : requested;
    line.hidden = false;
  }

  initAbilities();
  initMatrix();
  initNotFound();
})();
