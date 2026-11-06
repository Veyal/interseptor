// suppressed-banner.js — the History banner that says the capture filter dropped
// traffic. Without it, "the app made no requests" and "we filtered them" look
// identical: the proxy forwarded the requests and History has none of them.
//
// It polls only while the list is empty or muted, so a normal engagement pays
// nothing; it never hides a drop behind a dismiss button, and one click adds the
// top host to scope.
import { api, $, esc, toast, toastError } from './core.js';
import { suppressedBannerModel, suppressedReasonText } from './suppressed-model.js';

const POLL_MS = 30000;
let timer = null;
let lastKey = '';

function bannerHost() {
  return $('#suppressedBanner');
}

async function paint() {
  const host = bannerHost();
  if (!host) return;
  let rep;
  try {
    rep = await api('/api/flows/suppressed?windowSec=900');
  } catch (e) {
    return; // an unreachable API must not add noise to History
  }
  const m = suppressedBannerModel(rep);
  if (m.key === lastKey) return;
  lastKey = m.key;
  host.innerHTML = '';
  host.hidden = !m.show;
  if (!m.show) return;
  const top = m.host ? { host: m.host, reason: m.reason, count: Number((rep.hosts && rep.hosts[0] && rep.hosts[0].count) || 0) } : null;
  const text = document.createElement('span');
  text.innerHTML = '<strong>' + esc(m.text) + '</strong>';
  const row = document.createElement('div');
  row.className = 'row u-gap-2 u-wrap u-mt-2';
  if (top && top.reason === 'out-of-scope') {
    const add = document.createElement('button');
    add.type = 'button';
    add.className = 'btn btn-sm';
    add.textContent = 'Add ' + top.host + ' to scope';
    add.addEventListener('click', async () => {
      try {
        await api('/api/scope', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ action: 'include', host: top.host, enabled: true }),
        });
        toast('added ' + top.host + ' to scope — browse again');
        lastKey = '';
        paint();
      } catch (e) {
        toastError('Add to scope failed', e);
      }
    });
    row.appendChild(add);
  }
  const why = document.createElement('span');
  why.className = 'hint';
  why.textContent = m.hint;
  row.appendChild(why);
  host.append(text, row);
}

function schedule() {
  if (timer) clearInterval(timer);
  timer = setInterval(() => {
    if (!document.hidden) void paint();
  }, POLL_MS);
}

export function startSuppressedBanner() {
  const host = bannerHost();
  if (!host) return;
  void paint();
  schedule();
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) void paint();
  });
}

// Side-effect module: start as soon as the DOM is parsed.
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', startSuppressedBanner, { once: true });
  else startSuppressedBanner();
}
