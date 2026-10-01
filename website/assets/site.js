(() => {
  const root = document.documentElement;
  const themeButton = document.querySelector('.theme-toggle');
  const darkPreference = window.matchMedia('(prefers-color-scheme: dark)');
  function applyTheme(theme) {
    root.dataset.theme = theme;
    themeButton?.setAttribute('aria-label', `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`);
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#111318' : '#ffffff');
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
