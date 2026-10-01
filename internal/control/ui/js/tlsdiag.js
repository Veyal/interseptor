// tlsdiag.js — surfaces SSL pinning / missing-traffic diagnosis in the UI.
import { $, esc, state, api, toast, renderLoadError } from './core.js';

const VERDICT = {
  ok: { label: 'HTTPS OK', color: 'var(--accent)', icon: '✓' },
  tls_blocked: { label: 'TLS blocked — pinning or untrusted CA', color: 'var(--red)', icon: '<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-block"/></svg>' },
  no_traffic: { label: 'No traffic captured yet', color: 'var(--amber)', icon: '○' },
  no_https: { label: 'No HTTPS traffic intercepted yet (HTTP only so far)', color: 'var(--amber)', icon: '?' },
};

export const BANNER_HIDDEN_KEY = 'tlsDiagBannerHidden';
let lastDiag = null;
let bannerDismissedVerdict = null;
let diagRefreshTimer=null;
let trafficDiagnosisEpoch=0;

// Capture SSE can arrive in large bursts. Coalesce diagnosis refreshes so a
// busy HTTP-only target does not turn one flow into one control-plane request.
function scheduleTrafficDiagnosis(delay=180) {
  if(diagRefreshTimer)return;
  diagRefreshTimer=setTimeout(()=>{
    diagRefreshTimer=null;
    loadTrafficDiagnosis();
  },delay);
}

function verdictMeta(v) {
  return VERDICT[v] || { label: v, color: 'var(--fg2)', icon: '·' };
}

function hostsLine(rep) {
  if (!rep.hostsBlocked || !rep.hostsBlocked.length) return '';
  return `<div style="margin-top:6px;font-size:var(--fs-xs);color:var(--fg3)">Blocked hosts: <code>${rep.hostsBlocked.map(h => esc(h)).join('</code>, <code>')}</code></div>`;
}

function bypassNote() {
  return `<p style="margin:8px 0 0;font-size:var(--fs-xs);color:var(--fg3)"><b>Interseptor cannot bypass SSL pinning to read this traffic</b> — that requires changes on the device (Frida, patched APK, emulator + system CA if the app does not pin). If these domains aren't important to your test, <b>pass them through</b> so the app keeps working while you intercept the rest.</p>`;
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
  root.querySelector('[data-tls-action="dismiss"]')?.addEventListener('click', () => dismissBannerForVerdict(rep.verdict));
  root.querySelector('[data-tls-action="dismiss-forever"]')?.addEventListener('click', () => {
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
    <span style="flex:1;min-width:200px;color:var(--fg2);font-size:var(--fs-sm);line-height:1.55">${esc(rep.detail || '')}</span>
    ${rep.verdict === 'tls_blocked' ? `<button type="button" class="btn" data-tls-action="filter-pin" style="flex:none">Show TLS-failed rows</button>` : ''}
    ${rep.verdict === 'tls_blocked' && rep.hostsBlocked && rep.hostsBlocked.length ? `<button type="button" class="btn accent" data-tls-action="passthrough" style="flex:none" title="Tunnel these pinned hosts straight through (no interception) so the app works">Pass through ${rep.hostsBlocked.length} host${rep.hostsBlocked.length > 1 ? 's' : ''}</button>` : ''}
    ${rep.verdict !== 'ok' ? `<button type="button" class="btn" data-tls-action="open-settings" style="flex:none">Settings → TLS</button>` : ''}
    <button type="button" class="btn" data-tls-action="dismiss" title="Dismiss until verdict changes" style="flex:none;padding:3px 8px" aria-label="Dismiss TLS diagnosis banner">✕</button>
    <button type="button" class="btn" data-tls-action="dismiss-forever" title="Never show this banner in Proxy History" style="flex:none;font-size:var(--fs-xs)">Don't show again</button>
  </div>
  ${rep.fix ? `<div style="margin-top:6px;font-size:var(--fs-xs);color:var(--fg2)"><b>Fix:</b> ${esc(rep.fix)}</div>` : ''}
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
      banner.style.cssText = 'display:block;padding:8px 12px;border-bottom:1px solid var(--line);background:var(--bg2);font-size:var(--fs-sm);line-height:1.55';
      banner.innerHTML = body;
      wireTrafficDiagnosisActions(banner,rep);
      wireBannerDismiss(banner, rep);
    }
  }

  if (panel) {
    panel.innerHTML = body;
    wireTrafficDiagnosisActions(panel,rep);
  }

  // Refresh empty-state card when diagnosis arrives after loadFlows.
  if (!state.flows || !state.flows.length) {
    import('./proxy.js').then(m => m.renderRows());
  }
}

function wireTrafficDiagnosisActions(root,rep) {
  if (!root) return;
  const pin = root.querySelector('[data-tls-action="filter-pin"]');
  if (pin) pin.onclick = () => {
    document.querySelector('.tab[data-tab="proxy"]')?.click();
    import('./proxy.js').then(m => {
      m.setShowTlsFailed(true);
      m.setFilter('tag', 'tls-failed');
    });
  };
  const set = root.querySelector('[data-tls-action="open-settings"]');
  if (set) set.onclick = () => {
    document.querySelector('.tab[data-tab="settings"]')?.click();
    document.querySelector('#setNav button[data-sec="tls"]')?.click();
  };
  const pass = root.querySelector('[data-tls-action="passthrough"]');
  if (pass) pass.onclick = () => addHostsToPassthrough((rep && rep.hostsBlocked) || []);
}

// addHostsToPassthrough merges the given hosts into the TLS-bypass list so the
// app can keep using them (untouched) while everything else stays intercepted.
async function addHostsToPassthrough(hosts) {
  hosts = (hosts || []).map(h => String(h).trim().toLowerCase()).filter(Boolean);
  if (!hosts.length) return;
  try {
    const { addTLSBypassHosts } = await import('./settings.js');
    await addTLSBypassHosts(hosts);
    toast('Passing through ' + hosts.length + ' pinned host' + (hosts.length > 1 ? 's' : '') + ' — reconnect the app');
    loadTrafficDiagnosis();
  } catch (e) { toast('passthrough: ' + e.message); }
}

export async function loadTrafficDiagnosis(host) {
  const epoch=++trafficDiagnosisEpoch;
  try {
    const q = host ? '?host=' + encodeURIComponent(host) : '';
    const rep = await api('/api/tls-diagnosis' + q);
    if(epoch!==trafficDiagnosisEpoch)return null;
    renderTrafficDiagnosis(rep);
    return rep;
  } catch (e) {
    if(epoch!==trafficDiagnosisEpoch)return null;
    renderLoadError($('#tlsDiagPanel'),'Traffic diagnosis',e,()=>loadTrafficDiagnosis(host),false);
    return null;
  }
}

export function getStartedDiagnosisHint() {
  if (!lastDiag || lastDiag.verdict === 'ok') return '';
  const v = verdictMeta(lastDiag.verdict);
  return `<div style="margin:14px 0;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg2);font-size:var(--fs-sm);line-height:1.6">
    <div style="font-weight:700;color:${v.color};margin-bottom:4px">${v.icon} ${esc(v.label)}</div>
    <div style="color:var(--fg2)">${esc(lastDiag.detail || '')}</div>
    ${lastDiag.verdict === 'tls_blocked' ? '<div style="margin-top:6px;color:var(--fg3)">Interseptor detects pinning but <b>cannot bypass it</b> — use Frida, a patched APK, or an emulator with system CA.</div>' : ''}
    ${lastDiag.fix ? `<div style="margin-top:6px;color:var(--fg2)"><b>Try:</b> ${esc(lastDiag.fix)}</div>` : ''}
  </div>`;
}

export function onFlowMaybeTLS(f) {
  if (!f) return;
  // The first captured flow can move "no traffic" to another verdict. Once
  // plain HTTP is already known, only HTTPS traffic or a TLS failure can change
  // the diagnosis; more HTTP requests merely change the total count.
  const stalable = lastDiag && lastDiag.verdict === 'no_traffic';
  const tlsRelevant = (f.flags & 16) || f.scheme === 'https';
  if (tlsRelevant || stalable) scheduleTrafficDiagnosis();
}
