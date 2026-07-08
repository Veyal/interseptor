// tlsdiag.js — surfaces SSL pinning / missing-traffic diagnosis in the UI.
import { $, esc, state, api, toast } from './core.js';

const VERDICT = {
  ok: { label: 'HTTPS OK', color: 'var(--accent)', icon: '✓' },
  tls_blocked: { label: 'TLS blocked — pinning or untrusted CA', color: 'var(--red)', icon: '⛔' },
  no_traffic: { label: 'No traffic captured yet', color: 'var(--amber)', icon: '○' },
  no_https: { label: 'No HTTPS traffic intercepted yet (HTTP only so far)', color: 'var(--amber)', icon: '?' },
};

export const BANNER_HIDDEN_KEY = 'tlsDiagBannerHidden';
let lastDiag = null;
let bannerDismissedVerdict = null;

function verdictMeta(v) {
  return VERDICT[v] || { label: v, color: 'var(--fg2)', icon: '·' };
}

function hostsLine(rep) {
  if (!rep.hostsBlocked || !rep.hostsBlocked.length) return '';
  return `<div style="margin-top:6px;font-size:11.5px;color:var(--fg3)">Blocked hosts: <code>${rep.hostsBlocked.map(h => esc(h)).join('</code>, <code>')}</code></div>`;
}

function bypassNote() {
  return `<p style="margin:8px 0 0;font-size:11.5px;color:var(--fg3)"><b>Interceptor cannot bypass SSL pinning to read this traffic</b> — that requires changes on the device (Frida, patched APK, emulator + system CA if the app does not pin). If these domains aren't important to your test, <b>pass them through</b> so the app keeps working while you intercept the rest.</p>`;
}

export function isTlsBannerHidden() {
  try {
    if (localStorage.getItem(BANNER_HIDDEN_KEY) === '1') return true;
  } catch (e) {}
  return false;
}

export function setTlsBannerHidden(hidden) {
  try {
    if (hidden) localStorage.setItem(BANNER_HIDDEN_KEY, '1');
    else localStorage.removeItem(BANNER_HIDDEN_KEY);
  } catch (e) {}
  syncTlsBannerSetting();
}

function isBannerSuppressed(rep) {
  if (!rep || rep.verdict === 'ok') return false;
  if (isTlsBannerHidden()) return true;
  return bannerDismissedVerdict === rep.verdict;
}

function dismissBannerForVerdict(verdict) {
  bannerDismissedVerdict = verdict || null;
  const banner = $('#tlsDiagBanner');
  if (banner) {
    banner.style.display = 'none';
    banner.innerHTML = '';
  }
}

function wireBannerDismiss(root, rep) {
  if (!root || !rep) return;
  root.querySelector('#tlsBannerDismiss')?.addEventListener('click', () => dismissBannerForVerdict(rep.verdict));
  root.querySelector('#tlsBannerDismissForever')?.addEventListener('click', () => {
    setTlsBannerHidden(true);
    dismissBannerForVerdict(rep.verdict);
  });
}

export function syncTlsBannerSetting() {
  const inp = $('#tlsShowBanner');
  if (!inp) return;
  inp.checked = !isTlsBannerHidden();
}

export function renderTrafficDiagnosis(rep) {
  lastDiag = rep;
  const v = verdictMeta(rep.verdict);
  const banner = $('#tlsDiagBanner');
  const panel = $('#tlsDiagPanel');

  const body = `<div style="display:flex;gap:10px;align-items:flex-start;flex-wrap:wrap">
    <span style="font-weight:700;color:${v.color};white-space:nowrap">${v.icon} ${esc(v.label)}</span>
    <span style="flex:1;min-width:200px;color:var(--fg2);font-size:12px;line-height:1.55">${esc(rep.detail || '')}</span>
    ${rep.verdict === 'tls_blocked' ? `<button type="button" class="btn" id="tlsFilterPinBtn" style="flex:none">Show PIN rows</button>` : ''}
    ${rep.verdict === 'tls_blocked' && rep.hostsBlocked && rep.hostsBlocked.length ? `<button type="button" class="btn accent" id="tlsPassthroughBtn" style="flex:none" title="Tunnel these pinned hosts straight through (no interception) so the app works">Pass through ${rep.hostsBlocked.length} host${rep.hostsBlocked.length > 1 ? 's' : ''}</button>` : ''}
    ${rep.verdict !== 'ok' ? `<button type="button" class="btn" id="tlsOpenSettingsBtn" style="flex:none">Settings → TLS</button>` : ''}
    <button type="button" class="btn" id="tlsBannerDismiss" title="Dismiss until verdict changes" style="flex:none;padding:3px 8px" aria-label="Dismiss TLS diagnosis banner">✕</button>
    <button type="button" class="btn" id="tlsBannerDismissForever" title="Never show this banner in Proxy History" style="flex:none;font-size:11px">Don't show again</button>
  </div>
  ${rep.fix ? `<div style="margin-top:6px;font-size:11.5px;color:var(--fg2)"><b>Fix:</b> ${esc(rep.fix)}</div>` : ''}
  ${hostsLine(rep)}
  ${rep.verdict === 'tls_blocked' ? bypassNote() : ''}`;

  if (banner) {
    if (rep.verdict === 'ok' && rep.totalFlows > 0) {
      bannerDismissedVerdict = null;
      banner.style.display = 'none';
      banner.innerHTML = '';
    } else if (isBannerSuppressed(rep)) {
      banner.style.display = 'none';
      banner.innerHTML = '';
    } else {
      banner.style.display = '';
      banner.style.cssText = 'display:block;padding:8px 12px;border-bottom:1px solid var(--line);background:var(--bg2);font-size:12px;line-height:1.55';
      banner.innerHTML = body;
      wireTrafficDiagnosisActions(banner);
      wireBannerDismiss(banner, rep);
    }
  }

  if (panel) {
    panel.innerHTML = body;
    wireTrafficDiagnosisActions(panel);
  }

  // Refresh empty-state card when diagnosis arrives after loadFlows.
  if (!state.flows || !state.flows.length) {
    import('./proxy.js').then(m => m.renderRows());
  }
}

function wireTrafficDiagnosisActions(root) {
  if (!root) return;
  const pin = root.querySelector('#tlsFilterPinBtn');
  if (pin) pin.onclick = () => {
    document.querySelector('.tab[data-tab="proxy"]')?.click();
    import('./proxy.js').then(m => {
      m.setShowTlsFailed(true);
      m.setFilter('tag', 'tls-failed');
    });
  };
  const set = root.querySelector('#tlsOpenSettingsBtn');
  if (set) set.onclick = () => {
    document.querySelector('.tab[data-tab="settings"]')?.click();
    document.querySelector('#setNav button[data-sec="tls"]')?.click();
  };
  const pass = root.querySelector('#tlsPassthroughBtn');
  if (pass) pass.onclick = () => addHostsToPassthrough((lastDiag && lastDiag.hostsBlocked) || []);
}

// addHostsToPassthrough merges the given hosts into the TLS-bypass list so the
// app can keep using them (untouched) while everything else stays intercepted.
async function addHostsToPassthrough(hosts) {
  hosts = (hosts || []).map(h => String(h).trim().toLowerCase()).filter(Boolean);
  if (!hosts.length) return;
  try {
    const cur = await api('/api/settings');
    const merged = [...new Set([...(cur.tlsBypassHosts || []), ...hosts])];
    await api('/api/settings', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ tlsBypassHosts: merged }) });
    toast('Passing through ' + hosts.length + ' pinned host' + (hosts.length > 1 ? 's' : '') + ' — reconnect the app');
    loadTrafficDiagnosis();
  } catch (e) { toast('passthrough: ' + e.message); }
}

export async function loadTrafficDiagnosis(host) {
  try {
    const q = host ? '?host=' + encodeURIComponent(host) : '';
    const rep = await api('/api/tls-diagnosis' + q);
    renderTrafficDiagnosis(rep);
    return rep;
  } catch (e) {
    const panel = $('#tlsDiagPanel');
    if (panel) panel.textContent = 'Could not load traffic diagnosis: ' + e.message;
    return null;
  }
}

export function getStartedDiagnosisHint() {
  if (!lastDiag || lastDiag.verdict === 'ok') return '';
  const v = verdictMeta(lastDiag.verdict);
  return `<div style="margin:14px 0;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg2);font-size:12px;line-height:1.6">
    <div style="font-weight:700;color:${v.color};margin-bottom:4px">${v.icon} ${esc(v.label)}</div>
    <div style="color:var(--fg2)">${esc(lastDiag.detail || '')}</div>
    ${lastDiag.verdict === 'tls_blocked' ? '<div style="margin-top:6px;color:var(--fg3)">Interceptor detects pinning but <b>cannot bypass it</b> — use Frida, a patched APK, or an emulator with system CA.</div>' : ''}
    ${lastDiag.fix ? `<div style="margin-top:6px;color:var(--fg2)"><b>Try:</b> ${esc(lastDiag.fix)}</div>` : ''}
  </div>`;
}

export function onFlowMaybeTLS(f) {
  if (!f) return;
  // A TLS-relevant flow (handshake failure/success) can always flip the
  // verdict. But "no_traffic"/"no_https" are about traffic volume, not TLS
  // specifically — any new flow (including plain HTTP) can move TotalFlows
  // off zero and stale-out one of those two verdicts, so re-check on every
  // flow while the banner is showing either, not just TLS-flagged ones.
  // Otherwise a run of non-TLS traffic never clears an initial "no traffic
  // reached the proxy yet" reading taken at page load.
  const stalable = lastDiag && (lastDiag.verdict === 'no_traffic' || lastDiag.verdict === 'no_https');
  if ((f.flags & 16) || stalable) loadTrafficDiagnosis();
}
