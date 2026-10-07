// setup.js — first-run setup wizard. Sequences the four things 90% of users need
// (point at the proxy → trust the CA → set target scope → done) instead of making
// them hunt across the Settings sections. Shown once on boot unless skipped, and
// reopenable from Settings → Project & data.
import { $, esc, escAttr, state, toast, toastError, api, openModal, closeModal, copyText, projectStorageKey, renderLoadError } from './core.js';
import { getSystemProxyStatus, setSystemProxyEnabled } from './settings.js';
import { projectState } from './project-state.js';

const SETUP_KEY = 'interceptor.setupDone';
let step = 0;
const LAST = 4;
let setupLastScopeHost = '';
let setupActionBusy = false;
let setupSystemProxyEpoch = 0;
let setupReadinessEpoch = 0;
function setSetupNavigationBusy(busy) {
  ['setupNext', 'setupBack', 'setupSkip'].forEach(id => {
    const control = $('#'+id);
    if (!control) return;
    control.disabled = !!busy;
    if (busy) control.setAttribute('aria-busy', 'true');
    else control.removeAttribute('aria-busy');
  });
}
function setSetupActionBusy(button, busy, label) {
  setupActionBusy = !!busy;
  setSetupNavigationBusy(setupActionBusy);
  const scopeInput = $('#setupScopeHost');
  if (scopeInput) {
    scopeInput.disabled = setupActionBusy;
    if (setupActionBusy) scopeInput.setAttribute('aria-busy', 'true');
    else scopeInput.removeAttribute('aria-busy');
  }
  if (!button) return;
  button.disabled = setupActionBusy;
  if (setupActionBusy) button.setAttribute('aria-busy', 'true');
  else button.removeAttribute('aria-busy');
  if (label) button.textContent = label;
}

function renderSetupSystemProxyState(button,status){
  if(!button||!status)return;
  if(!status.supported){
    button.style.display='none';
    return;
  }
  button.style.display='';
  button.removeAttribute('title');
  button.disabled=!!status.enabled;
  button.setAttribute('aria-pressed',status.enabled?'true':'false');
  button.textContent=status.enabled?'System proxy is on':'Set as system proxy';
}

function renderSetupSystemProxyError(button,error){
  if(!button)return;
  button.style.display='';
  button.disabled=false;
  button.removeAttribute('aria-pressed');
  button.textContent='Retry system proxy status';
  button.title=error?.message||'Could not read system proxy status';
}

function osHint() {
  const p = (navigator.platform || '') + ' ' + (navigator.userAgent || '');
  if (/Mac|iPhone|iPad/.test(p)) return 'mac';
  if (/Win/.test(p)) return 'win';
  if (/Linux|X11/.test(p)) return 'linux';
  return '';
}

const TRUST_STEPS = {
  mac: `<li>Open the downloaded <code>interseptor-ca.crt</code> — Keychain Access opens.</li><li>Add it to <b>System</b> (or login) → double-click <b>Interseptor</b> CA → <b>Trust</b> → <b>Always Trust</b>.</li>`,
  win: `<li>Double-click the <code>.crt</code> → <b>Install Certificate</b> → <b>Local Machine</b> <span class="hint">(needs admin — choose <b>Current User</b> if you're not)</span> → <b>Place all certificates in: Trusted Root Certification Authorities</b>.</li>`,
  linux: `<li><b>Debian/Ubuntu:</b> copy to <code>/usr/local/share/ca-certificates/interseptor.crt</code> → <code>sudo update-ca-certificates</code>.</li><li>Or one-off: <code>curl --cacert ~/.interseptor/ca/ca.crt -x http://127.0.0.1:8080 https://…</code></li>`,
};

// One-line terminal trust commands (CA download assumed to land in ~/Downloads).
const TRUST_COMMANDS = {
  mac: `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/Downloads/interseptor-ca.crt`,
  win: `Import-Certificate -FilePath "$env:USERPROFILE\Downloads\interseptor-ca.crt" -CertStoreLocation Cert:\LocalMachine\Root`,
  linux: `sudo cp ~/Downloads/interseptor-ca.crt /usr/local/share/ca-certificates/interseptor.crt && sudo update-ca-certificates`,
};

function setupDoneKey(){return projectStorageKey(SETUP_KEY);}
async function setupReadiness(){
  const box=$('#setupReadiness');if(!box)return null;
  const readinessStep=step;
  const readinessNode=box;
  const epoch=++setupReadinessEpoch;
  const ownsReadiness=()=>epoch===setupReadinessEpoch&&step===readinessStep&&readinessNode.isConnected;
  box.textContent='Checking proxy, TLS, and traffic…';
  try{
    const report=await api('/api/readiness');
    if(!ownsReadiness())return null;
    const ids=['proxy','tls_intercept','traffic'];
    const checks=ids.map(id=>(report.checks||[]).find(c=>c.id===id)).filter(Boolean);
    box.innerHTML=checks.map(c=>`<div class="${c.ok?'u-accent':'u-warn'}">${c.ok?'<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-status-done"/></svg>':'<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-alert"/></svg>'} ${esc(c.detail)}${!c.ok&&c.fix?' — '+esc(c.fix):''}</div>`).join('');
    return report;
  }catch(e){
    if(!ownsReadiness())return null;
    renderLoadError(box,'Readiness check',e,setupReadiness,false);
    return null;
  }
}

function renderStep() {
  if (setupActionBusy) return;
  ++setupSystemProxyEpoch;
  $('#setupStep').textContent = (step + 1) + ' / ' + (LAST + 1);
  $('#setupBack').style.display = step > 0 ? '' : 'none';
  const next=$('#setupNext');
  // The CA step owns its trust gate. Never carry that disabled state into a
  // different step when the operator navigates Back or forward again.
  next.disabled=false;
  next.textContent = step === LAST ? 'Finish' : 'Next ▸';
  const b = $('#setupBody');
  if (step === 0) {
    const addr = esc(state.proxyAddr || '127.0.0.1:8080');
    b.innerHTML = `<p class="u-m-0 u-mb-3">Choose the correct project before capture so engagement data stays separated. <button class="btn xs" id="setupChooseProject">Choose project…</button></p>
      <p class="u-m-0 u-mb-3">Interseptor is running. Point your browser or HTTP client's proxy at:</p>
      <div class="row u-gap-2 u-mb-4">
        <code class="evidence u-flex-1 u-m-0 u-fs-md">${addr}</code>
        <button class="btn" id="setupCopyAddr">Copy</button>
      </div>
       <button class="btn u-mb-3" id="setupSysProxy">Set as system proxy</button>
      <p class="hint u-m-0">HTTP works immediately. For <b>HTTPS</b>, the next step trusts the interception CA. The control UI (this window) is at <code>${esc(state.controlAddr||'127.0.0.1:9966')}</code>.</p>
      <div id="setupReadiness" class="evidence u-mt-3" role="status" aria-live="polite" aria-atomic="true"></div>`;
    $('#setupChooseProject').onclick=()=>import('./settings.js').then(m=>m.openProjectModal());
     $('#setupCopyAddr').onclick = () => copyText(state.proxyAddr || '127.0.0.1:8080', 'proxy address copied');
     const systemProxyEpoch=setupSystemProxyEpoch;
     const systemProxyButton=$('#setupSysProxy');
     const ownsSystemProxy=()=>systemProxyEpoch===setupSystemProxyEpoch&&step===0&&systemProxyButton?.isConnected;
     getSystemProxyStatus({throwOnError:true})
       .then(st=>{if(ownsSystemProxy())renderSetupSystemProxyState(systemProxyButton,st);})
       .catch(error=>{if(ownsSystemProxy())renderSetupSystemProxyError(systemProxyButton,error);});
     $('#setupSysProxy').onclick = async () => {
      if (setupActionBusy) return;
      const button = $('#setupSysProxy');
      const actionEpoch=++setupSystemProxyEpoch;
      const label = button.textContent;
      setSetupActionBusy(button, true, 'Setting proxy…');
      let acknowledged=null;
      let statusError=null;
      try {
        const st = await getSystemProxyStatus({throwOnError:true});
        acknowledged=st;
        if (!st?.supported) { toast('automatic system-proxy is macOS-only — set it manually on Windows/Linux'); return; }
        acknowledged=st.enabled?st:await setSystemProxyEnabled(true);
        toast('system proxy on — point your browser at the proxy now');
      } catch (e) {
        toastError('Setup action failed', e);
        try{acknowledged=await getSystemProxyStatus({render:false,throwOnError:true});}
        catch(statusErr){statusError=statusErr;}
      }
      finally {
        setSetupActionBusy(button, false, label);
        if(actionEpoch===setupSystemProxyEpoch&&step===0&&button.isConnected){
          if(acknowledged)renderSetupSystemProxyState(button,acknowledged);
          else if(statusError)renderSetupSystemProxyError(button,statusError);
        }
      }
    };
    setupReadiness();
  } else if (step === 1) {
    const os = osHint();
    const trust = TRUST_STEPS[os] || `<li>Install the CA into your OS/browser root trust store.</li>`;
    const cmd = TRUST_COMMANDS[os];
    const cmdBox = cmd ? `<div class="row u-gap-2 u-mt-3"><code class="evidence u-flex-1 u-m-0 u-fs-xs u-prewrap u-break">${esc(cmd)}</code><button class="btn" id="setupCopyCmd" aria-label="Copy trust command" title="Copy trust command"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-copy"/></svg></button></div><p class="hint u-m-0 u-mt-1">…or paste this one-liner into a terminal after downloading.</p>` : '';
    b.innerHTML = `<p class="u-m-0 u-mb-3">Download the CA and trust it so HTTPS traffic can be decrypted and edited.</p>
      <a class="btn accent u-no-underline u-inline-block u-mb-4" href="/api/ca.crt" download><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-download"/></svg> Download CA certificate</a>
      <details class="ca-how"${os ? ' open' : ''}><summary>${os === 'mac' ? 'macOS' : os === 'win' ? 'Windows' : os === 'linux' ? 'Linux' : 'Trust it'} — how to</summary><ol class="setup-steps u-fg2">${trust}</ol></details>
      ${cmdBox}
      <label class="icpt-chk u-flex u-gap-2 u-mt-3 u-cursor u-fg2"><input type="checkbox" id="setupTrusted"> I've installed &amp; trusted the CA</label>
      <p class="hint u-m-0 u-mt-2">This is a one-time manual step — Interseptor never modifies your OS trust store itself.</p>
      <p class="hint panel-card u-m-0 u-mt-3"><b>Mobile apps:</b> installing the CA is not enough for most Android/iOS apps. SSL <b>pinning</b> must be bypassed on the device (Frida, patched APK) — Interseptor only detects when pinning blocks traffic (red <b>PIN</b> rows).</p>`;
    next.disabled = true;
    $('#setupTrusted').onchange = e => { next.disabled = !e.target.checked; };
    if (cmd) $('#setupCopyCmd').onclick = () => copyText(cmd, 'trust command copied');
  } else if (step === 2) {
    b.innerHTML = `<p class="u-m-0 u-mb-2">Add the host you're testing so history, the intercept gate, and the scanner focus on it.</p>
      <p class="hint u-m-0 u-mb-3">e.g. <code>*.example.com</code>, <code>api.example.com</code>, or regex <code>.*example\\.com</code>. You can skip this and add it later from Settings → Scope.</p>
      <div class="row u-gap-2">
        <input id="setupScopeHost" class="btn u-bg3 u-mono u-flex-1" aria-label="Scope host" placeholder="*.example.com" spellcheck="false">
        <button class="btn" id="setupScopeAdd">+ Add to scope</button>
      </div>
      <div id="setupScopeMsg" class="hint u-mt-2" role="status" aria-live="polite"></div>`;
    $('#setupScopeAdd').onclick = async () => {
      if (setupActionBusy) return;
      const host = $('#setupScopeHost').value.trim();
      if (!host) { toast('enter a host'); return; }
      const button = $('#setupScopeAdd');
      const label = button.textContent;
      const actionStep = step;
      setSetupActionBusy(button, true, 'Adding…');
      try {
        await api('/api/scope', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ action: 'include', host, enabled: true }) });
        // The wizard owns this request. Keep the success state tied to the same
        // step and DOM nodes so a future caller cannot paint a recycled panel.
        if (step !== actionStep || !button.isConnected) return;
        const msg = $('#setupScopeMsg');
        if (msg) {
          msg.setAttribute('role', 'status');
          msg.innerHTML = '<span class="u-accent"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-check"/></svg> added ' + esc(host) + ' to scope</span>';
        }
        const input = $('#setupScopeHost');
        setupLastScopeHost = host;
        if (input && input.value.trim() === host) input.value = '';
      } catch (e) {
        if (step !== actionStep || !button.isConnected) return;
        const msg = $('#setupScopeMsg');
        if (msg) {
          msg.setAttribute('role', 'alert');
          msg.textContent = 'Could not add '+host+' to scope: '+(e.message || 'request failed');
        }
        toastError('Setup action failed', e);
      }
      finally {
        // Navigation and the input remain locked for the complete request,
        // including failure, then recover through the shared busy-state path.
        setSetupActionBusy(button, false, label);
      }
    };
  } else if (step === 3) {
    b.innerHTML = `<p class="u-m-0 u-mb-2">Set the authorised target and turn scope on, so findings and the readiness meter know what you are allowed to test.</p>
      <p class="hint u-m-0 u-mb-3">This saves the target in the engagement brief and adds an include rule if none covers it yet. Add the rest of the brief later from the Engagement strip.</p>
      <div class="row u-gap-2">
        <input id="setupTargetInput" class="btn u-bg3 u-mono u-flex-1" aria-label="Authorised target" aria-describedby="setupTargetMsg" placeholder="*.example.com" spellcheck="false" value="${escAttr(setupLastScopeHost)}">
        <button class="btn" id="setupTargetSave">Set target and enable scope</button>
      </div>
      <div id="setupTargetMsg" class="hint u-mt-2" role="status" aria-live="polite"></div>`;
    $('#setupTargetSave').onclick = () => saveSetupTarget();
  } else {
    b.innerHTML = `<p class="u-m-0 u-mb-3">Configuration steps are saved. Send HTTPS traffic through the proxy to verify CA trust and interception before testing.</p>
      <ul class="u-m-0 u-mb-4 u-fg2 u-list-pad">
        <li><b>Repeater</b> / <b>Intruder</b> to replay & fuzz requests</li>
        <li><b>Scanner</b> for passive checks, <b>Findings</b> to curate vulns</li>
        <li><b>Ctrl/⌘+K</b> opens the command palette; <b>?</b> shows shortcuts</li>
      </ul>

      <div id="setupReadiness" class="evidence u-mt-3" role="status" aria-live="polite" aria-atomic="true"></div>`;
    setupReadiness();
  }
}

// The brief's `scope` field is the authorised-target list; keep every other field
// and any lines already there, then make sure an enabled include rule covers it.
async function saveSetupTarget() {
  if (setupActionBusy) return;
  const target = ($('#setupTargetInput')?.value || '').trim();
  const msg = $('#setupTargetMsg');
  const say = (text, role) => { if (msg) { msg.setAttribute('role', role); msg.textContent = text; } };
  if (!target || /\s/.test(target)) { say('Enter one host or wildcard, for example *.example.com.', 'alert'); return; }
  const button = $('#setupTargetSave');
  const label = button.textContent;
  const actionStep = step;
  setSetupActionBusy(button, true, 'Saving…');
  try {
    const brief = await api('/api/engagement-brief');
    const fields = ['scope', 'authorisation', 'conductRules', 'rateLimits', 'doNotTouch', 'credentialPolicy'];
    const next = {};
    fields.forEach(f => { next[f] = brief[f] || ''; });
    const lines = next.scope.split(/\r?\n/).map(l => l.trim());
    if (!lines.includes(target)) next.scope = lines.filter(Boolean).concat(target).join('\n');
    await api('/api/engagement-brief', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify(next) });
    const scope = await api('/api/scope');
    const covered = (scope.rules || []).some(r => r.action === 'include' && r.enabled !== false && r.host === target);
    if (!covered) await api('/api/scope', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ action: 'include', host: target, enabled: true }) });
    projectState.refresh({ reason: 'setup.target' });
    if (step !== actionStep || !button.isConnected) return;
    say('Target ' + target + ' saved and scope is on.', 'status');
  } catch (e) {
    if (step !== actionStep || !button.isConnected) return;
    say('Could not set the target: ' + (e.message || 'request failed'), 'alert');
    toastError('Setup action failed', e);
  } finally {
    setSetupActionBusy(button, false, label);
  }
}

export function openSetup() {
  if (setupActionBusy) return;
  step = 0;
  openModal($('#setupModal'), { onEscape: requestCloseSetup, onDismiss: requestCloseSetup });
  renderStep();
}

function requestCloseSetup() {
  if (setupActionBusy) {
    toast('wait for the current setup action to finish');
    return;
  }
  closeModal($('#setupModal'));
}

function finish() {
  try { localStorage.setItem(setupDoneKey(), '1'); } catch (e) {}
  closeModal($('#setupModal'));
  toast('setup choices saved — verify readiness before testing');
}

$('#setupNext').onclick = () => {
  if (setupActionBusy) return;
  if (step < LAST) { step++; renderStep(); }
  else finish();
};
$('#setupBack').onclick = () => { if (setupActionBusy) return; if (step > 0) { step--; renderStep(); } };
$('#setupSkip').onclick = () => { if (setupActionBusy) return; try { localStorage.setItem(setupDoneKey(), '1'); } catch (e) {} requestCloseSetup(); };
$('#setupBody').addEventListener('keydown', e => {
  if (e.key === 'Enter' && e.target.tagName === 'INPUT' && step !== 1) {
    // Enter in a text input advances (except on the CA-checkbox step).
    if (!$('#setupNext').disabled) { e.preventDefault(); $('#setupNext').click(); }
  }
});

// Show on first boot (unless the user already completed/skipped or has traffic).
export function maybeShowSetup() {
  try { if (localStorage.getItem(setupDoneKey())) return; } catch (e) {}
  // Don't pester returning users who already have captured flows.
  if (state.flows && state.flows.length) { try { localStorage.setItem(setupDoneKey(), '1'); } catch (e) {} return; }
  openSetup();
}
