(() => {
  const input = document.querySelector('#search');
  const results = document.querySelector('#search-results');
  const status = document.querySelector('#search-status');
  if (!input || !results) return;
  const base = new URL(document.body.dataset.baseurl || '/', document.baseURI);
  let index = null;
  let pending = null;
  let failed = false;
  let dismissed = false;
  let timer;
  function announce(message) { if (status) status.textContent = message; }
  function closeResults() {
    clearTimeout(timer);
    dismissed = true;
    results.hidden = true;
    results.replaceChildren();
  }
  function message(text) {
    const p = document.createElement('p');
    p.className = 'search-empty';
    p.textContent = text;
    results.append(p);
    results.hidden = false;
    announce(text);
  }
  async function load() {
    if (index || pending) return pending;
    failed = false;
    pending = (async () => {
      try {
        const response = await fetch(new URL('website/data/search.json', base));
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const data = await response.json();
        if (!Array.isArray(data)) throw new Error('Invalid search index');
        index = data.filter(item => typeof item.title === 'string' && typeof item.url === 'string');
      } catch (_) { failed = true; }
      finally { pending = null; if (!dismissed) search(); }
    })();
    return pending;
  }
  function excerpt(text, query) {
    const clean = String(text || '').trim();
    const at = clean.toLowerCase().indexOf(query);
    const start = Math.max(0, at < 0 ? 0 : at - 42);
    return (start ? '…' : '') + clean.slice(start, start + 130) + (start + 130 < clean.length ? '…' : '');
  }
  function search() {
    const query = input.value.trim().toLowerCase();
    results.replaceChildren();
    if (!query) { results.hidden = true; announce(''); return; }
    if (!index) {
      if (failed) {
        message('Search could not load. Try again or browse the menu.');
        const retry = document.createElement('button');
        retry.type = 'button'; retry.className = 'button search-retry'; retry.textContent = 'Retry search';
        retry.addEventListener('click', () => { input.focus(); dismissed = false; load(); search(); });
        results.append(retry);
      } else message('Loading documentation search…');
      return;
    }
    const terms = query.split(/\s+/).filter(Boolean);
    const matches = index.map(item => {
      const title = item.title.toLowerCase();
      const haystack = `${title} ${item.text || ''}`.toLowerCase();
      return { item, matches: terms.every(term => haystack.includes(term)), score: (title === query ? 20 : 0) + terms.filter(term => title.includes(term)).length * 3 };
    }).filter(item => item.matches).sort((a,b) => b.score - a.score);
    if (!matches.length) { message('No results. Try a feature name, such as “Findings” or “Settings”.'); return; }
    matches.slice(0,8).forEach(({item}) => {
      const url = new URL(item.url.replace(/^\//,''), base);
      if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return;
      const link = document.createElement('a');
      link.href = url.href;
      const title = document.createElement('strong'); title.textContent = item.title;
      const text = document.createElement('span'); text.textContent = excerpt(item.text, query);
      link.append(title,text); results.append(link);
    });
    results.hidden = false;
    announce(`${matches.length} result${matches.length === 1 ? '' : 's'}. Showing up to eight.`);
  }
  input.addEventListener('focus', () => { dismissed = false; load(); search(); });
  input.addEventListener('input', () => {
    dismissed = false; clearTimeout(timer);
    timer = setTimeout(() => { load(); search(); }, 120);
  });
  input.addEventListener('keydown', event => {
    const links = [...results.querySelectorAll('a')];
    if (event.key === 'ArrowDown' && links.length) { event.preventDefault(); links[0].focus(); }
    if (event.key === 'ArrowUp' && links.length) { event.preventDefault(); links.at(-1).focus(); }
    if (event.key === 'Enter' && links.length) { event.preventDefault(); links[0].click(); }
    if (event.key === 'Escape') { clearTimeout(timer); closeResults(); }
  });
  results.addEventListener('keydown', event => {
    const links = [...results.querySelectorAll('a')];
    const at = links.indexOf(document.activeElement);
    if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && links.length) {
      event.preventDefault(); links[(at + (event.key === 'ArrowDown' ? 1 : -1) + links.length) % links.length].focus();
    }
    if (event.key === 'Escape') { event.preventDefault(); input.focus(); closeResults(); }
  });
  results.addEventListener('click', event => {
    if (event.target.closest('a')) {
      // Keep the link in the DOM until its native navigation completes, including
      // same-page heading jumps which do not reload the document.
      dismissed = true;
      results.hidden = true;
    }
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('.search')) { clearTimeout(timer); closeResults(); } });
  document.addEventListener('focusin', event => { if (!event.target.closest('.search')) closeResults(); });
  document.addEventListener('keydown', event => {
    const editing = event.target.closest('input,textarea,[contenteditable="true"]');
    if ((!editing && event.key === '/' && !event.metaKey && !event.ctrlKey && !event.altKey) || ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k')) {
      event.preventDefault(); input.focus(); input.select();
    }
  });
})();
