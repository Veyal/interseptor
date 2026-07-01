// tlsdiag.js — surfaces SSL pinning / missing-traffic diagnosis in the UI.
import { $, esc, state, api } from './core.js';

const VERDICT = {
  ok: { label: 'HTTPS OK', color: 'var(--accent)', icon: '✓' },
  tls_blocked: { label: 'TLS blocked — pinning or untrusted CA', color: 'var(--red)', icon: '⛔' },
  no_traffic: { label: 'No traffic reached proxy', color: 'var(--amber)', icon: '○' },
  no_https: { label: 'No HTTPS intercepted yet', color: 'var(--amber)', icon: '?' },
};

let lastDiag = null;

function verdictMeta(v) {
  return VERDICT[v] || { label: v, color: 'var(--fg2)', icon: '·' };
}

function hostsLine(rep) {
  if (!rep.hostsBlocked || !rep.hostsBlocked.length) return '';
  return `<div style="margin-top:6px;font-size:11.5px;color:var(--fg3)">Blocked hosts: <code>${rep.hostsBlocked.map(h => esc(h)).join('</code>, <code>')}</code></div>`;
}

function bypassNote() {
  return `<p style="margin:8px 0 0;font-size:11.5px;color:var(--fg3)"><b>Interceptor cannot bypass SSL pinning</b> — bypass requires changes on the device (Frida, patched APK, emulator + system CA if the app does not pin).</p>`;
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
    ${rep.verdict !== 'ok' ? `<button type="button" class="btn" id="tlsOpenSettingsBtn" style="flex:none">Settings → TLS</button>` : ''}
  </div>
  ${rep.fix ? `<div style="margin-top:6px;font-size:11.5px;color:var(--fg2)"><b>Fix:</b> ${esc(rep.fix)}</div>` : ''}
  ${hostsLine(rep)}
  ${rep.verdict === 'tls_blocked' ? bypassNote() : ''}`;

  if (banner) {
    if (rep.verdict === 'ok' && rep.totalFlows > 0) {
      banner.style.display = 'none';
      banner.innerHTML = '';
    } else {
      banner.style.display = '';
      banner.style.cssText = 'display:block;padding:8px 12px;border-bottom:1px solid var(--line);background:var(--bg2);font-size:12px;line-height:1.55';
      banner.innerHTML = body;
      wireTrafficDiagnosisActions(banner);
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
    import('./proxy.js').then(m => m.setFilter('tag', 'tls-failed'));
  };
  const set = root.querySelector('#tlsOpenSettingsBtn');
  if (set) set.onclick = () => {
    document.querySelector('.tab[data-tab="settings"]')?.click();
    document.querySelector('#setNav button[data-sec="tls"]')?.click();
  };
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
  if (f && (f.flags & 16)) loadTrafficDiagnosis();
}
