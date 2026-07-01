// setup.js — first-run setup wizard. Sequences the four things 90% of users need
// (point at the proxy → trust the CA → set target scope → done) instead of making
// them hunt across the Settings sections. Shown once on boot unless skipped, and
// reopenable from Settings → Project & data.
import { $, esc, escAttr, state, toast, api, openModal, closeModal, copyText } from './core.js';

const SETUP_KEY = 'interceptor.setupDone';
let step = 0;
const LAST = 3;

function osHint() {
  const p = (navigator.platform || '') + ' ' + (navigator.userAgent || '');
  if (/Mac|iPhone|iPad/.test(p)) return 'mac';
  if (/Win/.test(p)) return 'win';
  if (/Linux|X11/.test(p)) return 'linux';
  return '';
}

const TRUST_STEPS = {
  mac: `<li>Open the downloaded <code>interceptor-ca.crt</code> — Keychain Access opens.</li><li>Add it to <b>System</b> (or login) → double-click <b>Interceptor</b> CA → <b>Trust</b> → <b>Always Trust</b>.</li>`,
  win: `<li>Double-click the <code>.crt</code> → <b>Install Certificate</b> → <b>Local Machine</b> → <b>Place all certificates in: Trusted Root Certification Authorities</b>.</li>`,
  linux: `<li><b>Debian/Ubuntu:</b> copy to <code>/usr/local/share/ca-certificates/interceptor.crt</code> → <code>sudo update-ca-certificates</code>.</li><li>Or one-off: <code>curl --cacert ~/.interceptor/ca/ca.crt -x http://127.0.0.1:8080 https://…</code></li>`,
};

function renderStep() {
  $('#setupStep').textContent = (step + 1) + ' / ' + (LAST + 1);
  $('#setupBack').style.display = step > 0 ? '' : 'none';
  $('#setupNext').textContent = step === LAST ? 'Finish ✓' : 'Next ▸';
  const b = $('#setupBody');
  if (step === 0) {
    const addr = esc(state.proxyAddr || '127.0.0.1:8080');
    b.innerHTML = `<p style="margin:0 0 10px">Interceptor is running. Point your browser or HTTP client's proxy at:</p>
      <div class="row" style="gap:8px;margin-bottom:14px">
        <code class="evidence" style="flex:1;margin:0;font-size:13px">${addr}</code>
        <button class="btn" id="setupCopyAddr">⧉ Copy</button>
      </div>
      <p class="hint" style="margin:0">HTTP works immediately. For <b>HTTPS</b>, the next step trusts the interception CA. The control UI (this window) is at <code>${esc(state.controlAddr||'127.0.0.1:9966')}</code>.</p>`;
    $('#setupCopyAddr').onclick = () => copyText(state.proxyAddr || '127.0.0.1:8080', 'proxy address copied');
  } else if (step === 1) {
    const os = osHint();
    const trust = TRUST_STEPS[os] || `<li>Install the CA into your OS/browser root trust store.</li>`;
    b.innerHTML = `<p style="margin:0 0 10px">Download the CA and trust it so HTTPS traffic can be decrypted and edited.</p>
      <a class="btn accent" href="/api/ca.crt" download style="text-decoration:none;display:inline-block;margin-bottom:14px">⤓ Download CA certificate</a>
      <details class="ca-how"${os ? ' open' : ''}><summary>${os === 'mac' ? 'macOS' : os === 'win' ? 'Windows' : os === 'linux' ? 'Linux' : 'Trust it'} — how to</summary><ol style="margin:8px 0 4px;padding-left:22px;color:var(--fg2)">${trust}</ol></details>
      <label class="icpt-chk" style="display:flex;align-items:center;gap:8px;margin-top:12px;cursor:pointer;color:var(--fg2)"><input type="checkbox" id="setupTrusted"> I've installed &amp; trusted the CA</label>
      <p class="hint" style="margin:8px 0 0">This is a one-time manual step — Interceptor never modifies your OS trust store itself.</p>
      <p class="hint" style="margin:10px 0 0;padding:8px 10px;border:1px solid var(--line);border-radius:6px;background:var(--bg2)"><b>Mobile apps:</b> installing the CA is not enough for most Android/iOS apps. SSL <b>pinning</b> must be bypassed on the device (Frida, patched APK) — Interceptor only detects when pinning blocks traffic (red <b>PIN</b> rows).</p>`;
    $('#setupNext').disabled = true;
    $('#setupTrusted').onchange = e => { $('#setupNext').disabled = !e.target.checked; };
  } else if (step === 2) {
    b.innerHTML = `<p style="margin:0 0 6px">Add the host you're testing so history, the intercept gate, and the scanner focus on it.</p>
      <p class="hint" style="margin:0 0 12px">e.g. <code>*.acme.com</code>, <code>api.target.com</code>, or regex <code>.*ohsome.*</code>. You can skip this and add it later from Settings → Target scope.</p>
      <div class="row" style="gap:8px">
        <input id="setupScopeHost" class="btn" style="flex:1;background:var(--bg3);font-family:var(--mono)" placeholder="*.acme.com" spellcheck="false">
        <button class="btn" id="setupScopeAdd">+ Add to scope</button>
      </div>
      <div id="setupScopeMsg" class="hint" style="margin-top:8px"></div>`;
    $('#setupScopeAdd').onclick = async () => {
      const host = $('#setupScopeHost').value.trim();
      if (!host) { toast('enter a host'); return; }
      try {
        await api('/api/scope', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ action: 'include', host, enabled: true }) });
        $('#setupScopeMsg').innerHTML = '<span style="color:var(--accent)">✓ added ' + esc(host) + ' to scope</span>';
        $('#setupScopeHost').value = '';
      } catch (e) { toast(e.message); }
    };
  } else {
    b.innerHTML = `<p style="margin:0 0 10px">You're set up. Send some traffic through the proxy and it'll appear in <b>Proxy History</b>.</p>
      <ul style="margin:0 0 14px;padding-left:20px;color:var(--fg2);line-height:1.7">
        <li><b>Repeater</b> / <b>Intruder</b> to replay & fuzz requests</li>
        <li><b>Scanner</b> for passive checks, <b>Findings</b> to curate vulns</li>
        <li><b>Ctrl+K</b> opens the command palette; <b>?</b> shows shortcuts</li>
      </ul>
      <p class="hint" style="margin:0">Optional: add an AI key in <b>Settings → AI assist</b> to explain requests, suggest payloads, or summarize findings. The MCP server lets an AI agent drive the same engine.</p>`;
  }
}

export function openSetup() {
  step = 0;
  openModal($('#setupModal'));
  renderStep();
}

function finish() {
  try { localStorage.setItem(SETUP_KEY, '1'); } catch (e) {}
  closeModal($('#setupModal'));
  toast('setup complete — happy testing');
}

$('#setupNext').onclick = () => {
  if (step < LAST) { step++; renderStep(); }
  else finish();
};
$('#setupBack').onclick = () => { if (step > 0) { step--; renderStep(); } };
$('#setupSkip').onclick = () => { try { localStorage.setItem(SETUP_KEY, '1'); } catch (e) {} closeModal($('#setupModal')); };
$('#setupBody').addEventListener('keydown', e => {
  if (e.key === 'Enter' && e.target.tagName === 'INPUT' && step !== 1) {
    // Enter in a text input advances (except on the CA-checkbox step).
    if (!$('#setupNext').disabled) { e.preventDefault(); $('#setupNext').click(); }
  }
});

// Show on first boot (unless the user already completed/skipped or has traffic).
export function maybeShowSetup() {
  try { if (localStorage.getItem(SETUP_KEY)) return; } catch (e) {}
  // Don't pester returning users who already have captured flows.
  if (state.flows && state.flows.length) { try { localStorage.setItem(SETUP_KEY, '1'); } catch (e) {} return; }
  openSetup();
}
