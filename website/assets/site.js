/* Interseptor site behaviour on every page: the theme switch, the menu drawer, copy buttons
   and code cards, and the docs outline (section tracking and reading progress).
   Contracts other pages rely on:
   - document.documentElement.dataset.theme is 'light' or 'dark'; a change dispatches
     document 'themechange' with detail {theme}.
   - button.copy-button copies data-copy-url (fetched text), data-copy-text (a literal), or
     data-copy="#selector" (that element's text without [data-copy-skip] parts), then shows
     data-state="copied" for about two seconds and announces the result.
   - .code-card > .code-bar > button.code-copy[hidden] is revealed and copies its pre code. */
(() => {
  'use strict';
  const root = document.documentElement;
  // Smooth in-page scrolling only after the browser's own jump to a #section on arrival.
  const smooth = () => requestAnimationFrame(() => requestAnimationFrame(() => root.classList.add('smooth-scroll')));
  if (document.readyState === 'complete') smooth(); else window.addEventListener('load', smooth, { once: true });
  const body = document.body;

  /* ---------- Theme ---------- */
  const THEME_KEY = 'interseptor-docs-theme';
  const THEME_COLORS = { light: '#fff8fb', dark: '#221a3a' };
  const systemDark = window.matchMedia('(prefers-color-scheme: dark)');
  function storedTheme() {
    try {
      const theme = localStorage.getItem(THEME_KEY);
      return theme === 'dark' || theme === 'light' ? theme : null;
    } catch (_) { return null; }
  }
  function applyTheme(theme) {
    const changed = root.dataset.theme !== theme;
    root.dataset.theme = theme;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_COLORS[theme]);
    document.querySelectorAll('.theme-toggle').forEach(button => button.setAttribute('aria-pressed', String(theme === 'dark')));
    if (changed) document.dispatchEvent(new CustomEvent('themechange', { detail: { theme } }));
  }
  applyTheme(storedTheme() || root.dataset.theme || (systemDark.matches ? 'dark' : 'light'));
  document.querySelectorAll('.theme-toggle').forEach(button => {
    button.addEventListener('click', () => {
      const next = root.dataset.theme === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      try { localStorage.setItem(THEME_KEY, next); } catch (_) {}
    });
  });
  systemDark.addEventListener('change', event => {
    if (!storedTheme()) applyTheme(event.matches ? 'dark' : 'light');
  });

  /* ---------- Shared helpers ---------- */
  const status = document.createElement('div');
  status.className = 'sr-only';
  status.setAttribute('role', 'status');
  status.setAttribute('aria-atomic', 'true');
  body.append(status);
  let statusTimer;
  function announce(message) {
    status.textContent = '';
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { status.textContent = message; }, 40);
  }
  function focusables(container) {
    return [...container.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), summary, [tabindex]:not([tabindex="-1"])')]
      .filter(el => el.getClientRects().length > 0);
  }
  // Keeps Tab inside an open modal dialog, wrapping at either end.
  function trapFocus(dialog) {
    dialog.addEventListener('keydown', event => {
      if (event.key !== 'Tab') return;
      const items = focusables(dialog);
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    });
  }
  // Scrolls box (not the page) so item sits in its middle, when it is out of view.
  function revealWithin(box, item) {
    if (!box || !item || box.scrollHeight <= box.clientHeight) return;
    const outer = box.getBoundingClientRect();
    const inner = item.getBoundingClientRect();
    if (inner.top < outer.top || inner.bottom > outer.bottom) {
      box.scrollTop += inner.top - outer.top - (outer.height - inner.height) / 2;
    }
  }

  /* ---------- Menu drawer (below 960px) ---------- */
  const drawer = document.getElementById('site-menu');
  const menuButton = document.querySelector('.menu-btn');
  if (drawer && menuButton && typeof drawer.showModal === 'function') {
    const closeDrawer = () => { if (drawer.open) drawer.close(); };
    menuButton.addEventListener('click', () => {
      if (drawer.open) return;
      drawer.showModal();
      menuButton.setAttribute('aria-expanded', 'true');
      revealWithin(drawer.querySelector('.drawer-body'), drawer.querySelector('.drawer-docs [aria-current="page"]'));
      drawer.querySelector('[data-drawer-close]')?.focus();
    });
    drawer.addEventListener('close', () => {
      menuButton.setAttribute('aria-expanded', 'false');
      menuButton.focus({ preventScroll: true });
    });
    drawer.querySelector('[data-drawer-close]')?.addEventListener('click', closeDrawer);
    drawer.addEventListener('click', event => {
      if (event.target === drawer) {
        const box = drawer.getBoundingClientRect();
        const inside = event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom;
        if (!inside) closeDrawer();
        return;
      }
      if (event.target.closest('a[href]')) closeDrawer();
    });
    trapFocus(drawer);
    window.matchMedia('(min-width: 960px)').addEventListener('change', event => { if (event.matches) closeDrawer(); });
  }

  /* ---------- Clipboard ---------- */
  async function writeText(text, anchor) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch (_) {}
    // Older browsers and plain-http previews: copy through a hidden textarea next to the
    // button, so it stays inside an open dialog and is not inert.
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.setAttribute('aria-hidden', 'true');
    area.style.position = 'fixed';
    area.style.top = '-1000px';
    area.style.opacity = '0';
    const returnTo = document.activeElement;
    (anchor?.parentElement || body).append(area);
    area.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (_) {}
    area.remove();
    if (returnTo && typeof returnTo.focus === 'function') returnTo.focus({ preventScroll: true });
    return ok;
  }
  // Copies the text at url. Safari keeps the click's permission only when the clipboard
  // write starts at once, which a ClipboardItem holding a promise allows.
  async function writeFetched(url, anchor) {
    const load = () => fetch(url, { credentials: 'omit' }).then(response => {
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.text();
    });
    if (window.ClipboardItem && navigator.clipboard?.write && window.isSecureContext) {
      try {
        const blob = load().then(text => new Blob([text], { type: 'text/plain' }));
        await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]);
        return true;
      } catch (_) {}
    }
    try { return await writeText(await load(), anchor); } catch (_) { return false; }
  }
  // Selects node's text so Ctrl+C or Command+C still works when no clipboard path does.
  function selectText(node) {
    const selection = window.getSelection();
    if (!node || !selection) return;
    const range = document.createRange();
    range.selectNodeContents(node);
    selection.removeAllRanges();
    selection.addRange(range);
  }
  function plainText(node, skip) {
    const clone = node.cloneNode(true);
    clone.querySelectorAll(skip).forEach(part => part.remove());
    return clone.textContent;
  }
  // The visible label is the last text outside icons and screen-reader-only text.
  function labelNode(button) {
    const walker = document.createTreeWalker(button, NodeFilter.SHOW_TEXT, {
      acceptNode: node => node.nodeValue.trim() && !node.parentElement.closest('svg, .sr-only') ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT,
    });
    let last = null;
    for (let node = walker.nextNode(); node; node = walker.nextNode()) last = node;
    return last;
  }
  function showCopied(button, ok, message, failedLabel = 'Press Ctrl+C') {
    const label = labelNode(button);
    if (label && button.dataset.copyLabel === undefined) button.dataset.copyLabel = label.nodeValue;
    if (label) label.nodeValue = ok ? 'Copied' : failedLabel;
    button.dataset.state = ok ? 'copied' : 'failed';
    announce(message);
    clearTimeout(button.copyTimer);
    button.copyTimer = setTimeout(() => {
      if (label) label.nodeValue = button.dataset.copyLabel;
      delete button.dataset.state;
    }, 2000);
  }
  function copiedMessage(text) {
    const clean = text.trim();
    return clean.length <= 120 ? `Copied: ${clean}` : 'Copied to the clipboard.';
  }
  const FAILED = 'Copy failed. The text is selected; press Ctrl+C or Command+C.';
  const BLOCKED = 'Copy failed: this browser blocked the clipboard.';

  document.querySelectorAll('.copy-button').forEach(button => {
    const source = () => {
      if (button.dataset.copy) {
        try { return document.querySelector(button.dataset.copy); } catch (_) { return null; }
      }
      return button.closest('.command, .terminal')?.querySelector('.command-text, .terminal-body') || null;
    };
    button.addEventListener('click', async () => {
      if (button.dataset.copyUrl) {
        if (await writeFetched(button.dataset.copyUrl, button)) { showCopied(button, true, 'Full text copied to the clipboard.'); return; }
        const fallback = button.dataset.copyText || '';
        const ok = fallback ? await writeText(fallback, button) : false;
        showCopied(button, ok, ok ? 'The full text did not load, so the short prompt was copied instead.' : 'Copy failed. Check your connection and try again.', 'Copy failed');
        return;
      }
      const node = button.dataset.copyText ? null : source();
      const text = button.dataset.copyText || (node ? plainText(node, '[data-copy-skip]').replace(/[ \t]+$/gm, '').trim() : '');
      const ok = text ? await writeText(text, button) : false;
      if (!ok) selectText(node);
      if (ok || node) showCopied(button, ok, ok ? copiedMessage(text) : FAILED);
      else showCopied(button, false, BLOCKED, 'Copy failed');
    });
  });

  /* ---------- Code cards ---------- */
  const scrollable = 'ResizeObserver' in window ? new ResizeObserver(entries => entries.forEach(entry => markScrollable(entry.target))) : null;
  // A code block that scrolls sideways becomes a focusable, named region for keyboard users.
  const codeCards = [...document.querySelectorAll('.code-card')];
  function markScrollable(pre) {
    const card = pre.closest('.code-card');
    if (pre.scrollWidth > pre.clientWidth + 1) {
      pre.tabIndex = 0;
      pre.setAttribute('role', 'region');
      pre.setAttribute('aria-label', `Code example ${codeCards.indexOf(card) + 1}, ${card?.dataset.lang || 'text'}`);
    } else if (pre.hasAttribute('tabindex')) {
      pre.removeAttribute('tabindex');
      pre.removeAttribute('role');
      pre.removeAttribute('aria-label');
    }
  }
  // Raw <pre> blocks (HTML pages) get the same card markup the Markdown renderer emits.
  document.querySelectorAll('.prose pre:not(.code)').forEach(pre => {
    if (pre.closest('.code-card')) return;
    const lang = ((pre.querySelector('code')?.className.match(/language-([\w+-]+)/) || [])[1] || 'text');
    const card = document.createElement('div');
    card.className = 'code-card';
    card.dataset.lang = lang;
    const bar = document.createElement('div');
    bar.className = 'code-bar';
    const name = document.createElement('span');
    name.className = 'code-lang';
    name.textContent = lang;
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'code-copy';
    copy.hidden = true;
    copy.textContent = 'Copy';
    bar.append(name, copy);
    pre.classList.add('code');
    pre.replaceWith(card);
    card.append(bar, pre);
  });
  codeCards.splice(0, codeCards.length, ...document.querySelectorAll('.code-card'));
  codeCards.forEach(card => {
    const button = card.querySelector('.code-copy');
    const pre = card.querySelector('pre');
    if (!pre) return;
    if (scrollable) scrollable.observe(pre); else markScrollable(pre);
    if (!button) return;
    const lang = card.dataset.lang || 'text';
    const extra = document.createElement('span');
    extra.className = 'sr-only';
    extra.textContent = lang === 'text' ? ' code block' : ` ${lang} code`;
    button.append(extra);
    button.hidden = false;
    button.addEventListener('click', async () => {
      const code = pre.querySelector('code') || pre;
      // Prompts ($ ) are not part of the command.
      const text = plainText(code, '.tok-prompt, [data-copy-skip]').replace(/\n$/, '');
      const ok = await writeText(text, button);
      if (!ok) selectText(code);
      showCopied(button, ok, ok ? 'Code copied to the clipboard.' : FAILED);
    });
  });

  // Raw tables inside prose scroll in their own region, like the Markdown ones.
  document.querySelectorAll('.prose table').forEach(table => {
    if (table.closest('.table-wrap, .table-scroll')) return;
    const wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    wrap.tabIndex = 0;
    wrap.setAttribute('role', 'region');
    const caption = table.querySelector('caption')?.textContent.trim();
    wrap.setAttribute('aria-label', caption ? `Table: ${caption}` : 'Table');
    table.replaceWith(wrap);
    wrap.append(table);
  });

  // Long unbroken tokens in table cells (API paths, dotted field names, value lists such as
  // a|b|c, CVSS vectors) may wrap after / . _ : | and similar separators instead of widening
  // the table. <wbr> adds a break opportunity and is never copied with the text.
  const SEPARATORS = '/._:,|=&?]';
  document.querySelectorAll('.table-wrap td, .table-scroll td').forEach(cell => {
    const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT);
    const long = [];
    for (let node = walker.nextNode(); node; node = walker.nextNode()) if (/\S{20,}/.test(node.nodeValue)) long.push(node);
    long.forEach(node => {
      const parts = document.createDocumentFragment();
      node.nodeValue.split(/(\s+)/).forEach(chunk => {
        if (chunk.length < 20 || /\s/.test(chunk)) { parts.append(chunk); return; }
        let start = 0;
        for (let i = 0; i < chunk.length - 1; i++) {
          if (SEPARATORS.includes(chunk[i]) && i + 1 - start >= 3) {
            parts.append(chunk.slice(start, i + 1), document.createElement('wbr'));
            start = i + 1;
          }
        }
        parts.append(chunk.slice(start));
      });
      node.replaceWith(parts);
    });
  });

  // Scroll regions need distinct names: a repeated table label gets a number.
  const regionNames = new Map();
  document.querySelectorAll('.table-wrap[aria-label], .table-scroll[aria-label]').forEach(region => {
    const name = region.getAttribute('aria-label');
    const seen = (regionNames.get(name) || 0) + 1;
    regionNames.set(name, seen);
    if (seen > 1) region.setAttribute('aria-label', `${name} (${seen})`);
  });

  // Task-list checkboxes are read-only status marks; name each after its item.
  document.querySelectorAll('.prose li > input[type="checkbox"]').forEach(box => {
    if (box.labels?.length || box.hasAttribute('aria-label') || box.hasAttribute('aria-labelledby')) return;
    const item = box.parentElement.cloneNode(true);
    item.querySelectorAll('ul, ol, input').forEach(part => part.remove());
    const text = item.textContent.replace(/\s+/g, ' ').trim();
    if (text) box.setAttribute('aria-label', text.length > 140 ? `${text.slice(0, 139)}…` : text);
  });

  /* ---------- Docs sidebar: keep the current page in view ---------- */
  const sidebar = document.querySelector('.docs-sidebar');
  revealWithin(sidebar, sidebar?.querySelector('[aria-current="page"]'));

  /* ---------- Outline: the section being read, and reading progress ---------- */
  const outlineLinks = [...document.querySelectorAll('.outline-list a[href^="#"]')];
  const ids = [...new Set(outlineLinks.map(link => decodeURIComponent(link.hash.slice(1))))];
  const headings = ids.map(id => document.getElementById(id)).filter(Boolean);
  const outlineScroll = document.querySelector('.outline .outline-scroll');
  const progress = document.querySelector('.outline-progress');
  const sheet = document.querySelector('.doc-sheet');
  if (headings.length && sheet) {
    let current = null;
    let queued = false;
    const update = () => {
      queued = false;
      const offset = (parseFloat(getComputedStyle(root).scrollPaddingTop) || 80) + 16;
      let active = null;
      for (const heading of headings) {
        if (heading.getBoundingClientRect().top <= offset) active = heading;
        else break;
      }
      const atEnd = window.innerHeight + window.scrollY >= root.scrollHeight - 4;
      if (atEnd) active = headings[headings.length - 1];
      if (active !== current) {
        current = active;
        const hash = active ? `#${active.id}` : '';
        outlineLinks.forEach(link => {
          if (hash && decodeURIComponent(link.hash) === hash) link.setAttribute('aria-current', 'location');
          else link.removeAttribute('aria-current');
        });
        revealWithin(outlineScroll, outlineScroll?.querySelector('[aria-current="location"]'));
      }
      if (progress) {
        const box = sheet.getBoundingClientRect();
        const travel = box.height - window.innerHeight;
        const read = travel > 0 ? Math.min(1, Math.max(0, -box.top / travel)) : 1;
        progress.style.setProperty('--read', read.toFixed(3));
      }
    };
    const queue = () => { if (!queued) { queued = true; requestAnimationFrame(update); } };
    window.addEventListener('scroll', queue, { passive: true });
    window.addEventListener('resize', queue, { passive: true });
    update();
  }
})();
