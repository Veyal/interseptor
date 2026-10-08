(() => {
  const root = document.documentElement;
  const themeButton = document.querySelector('.theme-toggle');
  const darkPreference = window.matchMedia('(prefers-color-scheme: dark)');
  function applyTheme(theme) {
    root.dataset.theme = theme;
    themeButton?.setAttribute('aria-label', `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`);
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#1a1722' : '#fff6e8');
  }
  applyTheme(root.dataset.theme || (darkPreference.matches ? 'dark' : 'light'));
  if (themeButton) {
    themeButton.hidden = false;
    themeButton.addEventListener('click', () => {
      applyTheme(root.dataset.theme === 'dark' ? 'light' : 'dark');
      try { localStorage.setItem('interseptor-docs-theme', root.dataset.theme); } catch (_) {}
    });
  }

  const menu = document.querySelector('.menu-toggle');
  const sidebar = document.querySelector('.sidebar');
  const mobile = window.matchMedia('(max-width: 960px)');
  function setMenu(open, returnFocus = false) {
    document.body.classList.toggle('nav-open', open);
    menu?.setAttribute('aria-expanded', String(open));
    menu?.setAttribute('aria-label', `${open ? 'Close' : 'Open'} documentation menu`);
    if (returnFocus) menu?.focus();
  }
  if (menu) {
    document.body.classList.add('nav-enhanced');
    menu.hidden = false;
    menu.addEventListener('click', () => setMenu(menu.getAttribute('aria-expanded') !== 'true'));
    mobile.addEventListener('change', () => setMenu(false));
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape' && document.body.classList.contains('nav-open')) {
        event.preventDefault();
        setMenu(false, true);
      }
    });
  }
  const activePage = sidebar?.querySelector('[aria-current="page"]');
  if (activePage && !mobile.matches) {
    sidebar.scrollTop = Math.max(0, activePage.offsetTop - sidebar.offsetTop - sidebar.clientHeight / 2);
  }

  // Copy text with the async clipboard API, falling back to a hidden textarea; returns success.
  async function copyText(text, returnFocus) {
    try { await navigator.clipboard.writeText(text); return true; } catch (_) {
      const area = document.createElement('textarea');
      area.value = text; area.setAttribute('readonly', ''); area.style.cssText = 'position:fixed;top:-100px;opacity:0';
      document.body.append(area); area.select();
      let ok = false;
      try { ok = document.execCommand('copy'); } catch (__) {}
      area.remove(); returnFocus?.focus();
      return ok;
    }
  }

  // Code cards: label and copy button. Without JavaScript the plain bordered block remains.
  document.querySelectorAll('main div.highlighter-rouge').forEach(block => {
    const code = block.querySelector('pre');
    if (!code) return;
    const lang = ((block.className.match(/language-([\w+-]+)/) || [])[1] || 'code').replace(/^plaintext$/, 'text');
    const card = document.createElement('div');
    card.className = 'code-card';
    const bar = document.createElement('div');
    bar.className = 'code-bar';
    const label = document.createElement('span');
    label.className = 'code-lang';
    label.textContent = lang;
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'chip';
    copy.textContent = 'Copy';
    copy.setAttribute('aria-label', `Copy ${lang} code`);
    let reset;
    copy.addEventListener('click', async () => {
      const ok = await copyText(code.textContent.replace(/\n$/, ''), copy);
      copy.textContent = ok ? 'Copied' : 'Press Ctrl+C';
      copy.dataset.state = ok ? 'copied' : 'failed';
      clearTimeout(reset);
      reset = setTimeout(() => { copy.textContent = 'Copy'; delete copy.dataset.state; }, 1800);
    });
    bar.append(label, copy);
    block.replaceWith(card);
    card.append(bar, block);
  });
  // Agent guide: one-click copy of the ready-to-paste prompt or the full llms-full.txt text.
  // The fetch is same-origin; if it fails the prompt is copied instead.
  if (/\/ai-agents\/?$/.test(location.pathname)) {
    const base = document.body.dataset.baseurl || '/';
    const fullUrl = new URL(`${base}llms-full.txt`, location.origin).href;
    const prompt = `Read ${fullUrl} and follow it.`;
    const anchor = document.querySelector('main h1 + p');
    if (anchor) {
      const row = document.createElement('div');
      row.className = 'agent-actions';
      const status = document.createElement('span');
      status.className = 'sr-only';
      status.setAttribute('role', 'status');
      status.setAttribute('aria-live', 'polite');
      const make = (label, aria) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'chip';
        button.textContent = label;
        button.setAttribute('aria-label', aria);
        return button;
      };
      const flash = (button, label, ok, message) => {
        button.textContent = ok ? 'Copied' : 'Press Ctrl+C';
        button.dataset.state = ok ? 'copied' : 'failed';
        status.textContent = message;
        clearTimeout(button._reset);
        button._reset = setTimeout(() => { button.textContent = label; delete button.dataset.state; }, 2200);
      };
      const promptButton = make('Copy for agent', 'Copy the prompt that points an agent at llms-full.txt');
      promptButton.addEventListener('click', async () => {
        flash(promptButton, 'Copy for agent', await copyText(prompt, promptButton), 'Prompt copied');
      });
      const fullButton = make('Copy full guide text', 'Copy the complete agent guide as plain text');
      fullButton.addEventListener('click', async () => {
        let text = prompt, note = 'Could not load the guide; copied the prompt instead';
        try {
          const response = await fetch(fullUrl, { credentials: 'omit' });
          if (response.ok) { text = await response.text(); note = 'Full guide copied'; }
        } catch (_) {}
        flash(fullButton, 'Copy full guide text', await copyText(text, fullButton), note);
      });
      row.append(promptButton, fullButton, status);
      anchor.after(row);
    }
  }
  // Tables scroll inside their own focusable region instead of widening the page.
  document.querySelectorAll('main table').forEach(table => {
    if (table.parentElement.classList.contains('table-wrap')) return;
    const wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    wrap.tabIndex = 0;
    wrap.setAttribute('role', 'region');
    const caption = table.querySelector('caption')?.textContent.trim();
    wrap.setAttribute('aria-label', caption ? `${caption} (scrollable table)` : 'Scrollable table');
    table.replaceWith(wrap);
    wrap.append(table);
  });

  const outline = document.querySelector('.outline');
  const outlineNav = document.querySelector('#page-outline');
  const headings = [...document.querySelectorAll('main h2[id]')];
  if (outline && outlineNav && headings.length > 1) {
    outline.hidden = false;
    const links = headings.map(heading => {
      const link = document.createElement('a');
      link.href = `#${encodeURIComponent(heading.id)}`;
      link.textContent = heading.textContent;
      outlineNav.append(link);
      return link;
    });
    const toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'outline-toggle';
    toggle.textContent = 'On this page';
    toggle.setAttribute('aria-controls', 'page-outline');
    outline.prepend(toggle);
    const compact = window.matchMedia('(max-width: 1200px)');
    function setOutline(open) {
      outlineNav.hidden = !open;
      toggle.setAttribute('aria-expanded', String(open));
    }
    setOutline(!compact.matches);
    compact.addEventListener('change', () => setOutline(!compact.matches));
    toggle.addEventListener('click', () => setOutline(outlineNav.hidden));
    let queued = false;
    function highlightSection() {
      queued = false;
      const top = parseFloat(getComputedStyle(root).getPropertyValue('--header')) + 40;
      let current = 0;
      headings.forEach((heading, i) => { if (heading.getBoundingClientRect().top <= top) current = i; });
      links.forEach((link, i) => {
        if (i === current) link.setAttribute('aria-current', 'location');
        else link.removeAttribute('aria-current');
      });
    }
    window.addEventListener('scroll', () => {
      if (!queued) { queued = true; requestAnimationFrame(highlightSection); }
    }, { passive: true });
    highlightSection();
  }
})();
