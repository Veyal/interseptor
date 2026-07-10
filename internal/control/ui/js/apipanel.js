import { $, esc, escAttr, api, toast, methodColor, copyText, uiConfirm } from './core.js';

/* ---- api module ---- */
$('#apiSub').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  $('#apiSub').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  ['Keys','Share','Rest','Mcp'].forEach(s=>{const el=$('#api'+s);if(el)el.style.display=(s.toLowerCase()===b.dataset.s)?'block':'none';});
  if(b.dataset.s==='share')loadShare();
});
export async function loadApiKeys(){
  try{const d=await api('/api/keys');const keys=d.keys||[];
    $('#keyList').innerHTML=keys.length?keys.map(k=>`<tr>
      <td style="font-family:var(--mono);color:var(--accent)">${esc(k.prefix)}…</td>
      <td>${esc(k.label)}</td>
      <td><span class="sev ${k.scope==='read'?'Info':'Low'}">${esc(k.scope||'full')}</span></td>
      <td style="color:var(--fg3)">${k.created?esc(new Date(k.created).toLocaleString()):'—'}${k.expires?'<br><span style="color:var(--amber)">exp '+esc(new Date(k.expires).toLocaleDateString())+'</span>':''}</td>
      <td><button class="btn danger" data-revoke="${k.id}" data-kp="${escAttr(k.prefix||'')}" data-kl="${escAttr(k.label||'')}">Revoke</button></td></tr>`).join('')
      :'<tr><td colspan="5" class="hint" style="padding:10px">No keys yet.</td></tr>';
    $('#keyList').querySelectorAll('[data-revoke]').forEach(b=>b.onclick=()=>revokeKey(Number(b.dataset.revoke),b.dataset.kp,b.dataset.kl));
  }catch(e){}
}
export async function createApiKey(){
  const label=$('#keyLabel').value.trim()||'key';
  const scope=($('#keyScope')||{}).value||'full';
  const expiresIn=Number(($('#keyExpiry')||{}).value||0);
  try{const d=await api('/api/keys',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({label,scope,expiresIn})});
    $('#keyNew').style.display='block';
    $('#keyNew').innerHTML='New '+esc(scope)+' token — copy now, it is shown only once:<br><b style="color:var(--accent);user-select:all">'+esc(d.token)+'</b>';
    $('#keyLabel').value='';loadApiKeys();
  }catch(e){toast(e.message);}
}

/** Reveal the API token for the current cookie session (remote Tailscale login). */
export async function revealSessionKey(){
  const box=$('#keySession'); if(!box)return;
  try{
    const d=await api('/api/session/access-key');
    box.style.display='block';
    box.innerHTML='Session access key ('+esc(d.scope||'full')+(d.prefix?' · '+esc(d.prefix)+'…':'')+') — <button class="btn" id="keySessionCopy" style="padding:2px 10px;vertical-align:middle">Copy</button><br><b style="color:var(--accent);user-select:all;word-break:break-all">'+esc(d.token)+'</b>';
    const cp=$('#keySessionCopy'); if(cp)cp.onclick=()=>copyText(d.token,'Access key copied');
  }catch(e){
    box.style.display='block';
    box.innerHTML='<span style="color:var(--amber)">'+esc(e.message||'No session key')+'</span><div class="hint" style="margin-top:6px">This only works when signed in via /login (cookie session). Loopback use has no session key.</div>';
  }
}
{const rb=$('#keyRevealSession'); if(rb)rb.onclick=revealSessionKey;}

/* ---- Share (Cloudflare tunnel) + peer sync ---- */
export async function loadShare(){
  try{const s=await api('/api/share/status');
    let html;
    if(s.running&&s.url){
      html='<div class="row" style="gap:8px;align-items:center"><span class="sev Low">live</span> <b style="color:var(--accent);user-select:all">'+esc(s.url)+'</b> <button class="btn" id="shareCopy" style="padding:2px 10px">Copy</button></div>'+
           '<div class="hint" style="margin-top:6px">Send this URL + an access key to a teammate, or point a VPS AI agent at <code>'+esc(s.url)+'/mcp</code> with a full-access key.</div>';
    }else if(s.running){
      html='<span class="sev Info">starting…</span> waiting for the public URL — refresh in a moment.';
    }else if(!s.installed){
      html='<span class="sev Medium">cloudflared not installed</span><div class="hint" style="margin-top:6px">Install <code>cloudflared</code> (<a href="https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" target="_blank" rel="noopener">download</a>) and retry — Interseptor runs a free quick tunnel, no account needed.</div>';
    }else if(!s.hasKeys){
      html='<span class="sev Medium">no access key</span><div class="hint" style="margin-top:6px">Create an access key first (Keys tab) — sharing is refused with no key so the surface is never exposed unauthenticated.</div>';
    }else{
      html='Not sharing. Click <b>Start sharing</b> to open a public tunnel.';
    }
    if(s.err)html+='<div class="hint" style="color:var(--red);margin-top:6px">'+esc(s.err)+'</div>';
    $('#shareStatus').innerHTML=html;
    $('#shareStart').style.display=s.running?'none':'inline-flex';
    $('#shareStop').style.display=s.running?'inline-flex':'none';
    const cp=$('#shareCopy');if(cp)cp.onclick=()=>copyText(s.url,'Tunnel URL copied');
  }catch(e){$('#shareStatus').textContent='Failed to load share status';}
}
async function startShare(){
  try{await api('/api/share/start',{method:'POST'});toast('tunnel starting…');setTimeout(loadShare,1500);loadShare();}
  catch(e){toast(e.message);loadShare();}
}
async function stopShare(){
  try{await api('/api/share/stop',{method:'POST'});toast('tunnel stopped');loadShare();}
  catch(e){toast(e.message);}
}
async function peerMerge(dir){
  const peerUrl=$('#peerUrl').value.trim(),key=$('#peerKey').value.trim(),label=$('#peerLabel').value.trim();
  if(!peerUrl||!key){toast('peer URL and key are required');return;}
  const verb=dir==='pull'?'Pull from':'Push to';
  if(!await uiConfirm(verb+' peer',verb+' <b>'+esc(peerUrl)+'</b>? '+(dir==='pull'?'Their flows &amp; findings will be merged into this project.':'This project will be merged into theirs.'),verb.split(' ')[0],'btn accent','var(--accent)'))return;
  $('#mergeResult').textContent=verb.toLowerCase()+'ing…';
  try{const r=await api('/api/merge/'+dir,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({peerUrl,key,label})});
    $('#mergeResult').innerHTML='<span style="color:var(--accent)">Done.</span> '+r.flowsAdded+' flows + '+r.findingsAdded+' findings added ('+r.flowsSkipped+'/'+r.findingsSkipped+' already present).';
    toast('sync complete');
  }catch(e){$('#mergeResult').innerHTML='<span style="color:var(--red)">'+esc(e.message)+'</span>';}
}
// Live tunnel URL arrival (SSE) refreshes the panel if it's open.
window.addEventListener('interceptor:tunnel',()=>{const p=$('#apiShare');if(p&&p.style.display!=='none')loadShare();});
const ss=$('#shareStart');if(ss)ss.onclick=startShare;
const sp=$('#shareStop');if(sp)sp.onclick=stopShare;
const pp=$('#peerPull');if(pp)pp.onclick=()=>peerMerge('pull');
const pu=$('#peerPush');if(pu)pu.onclick=()=>peerMerge('push');
export async function revokeKey(id,prefix,label){
  const who=(prefix?esc(prefix)+'…':'')+(label?' <b>'+esc(label)+'</b>':'');
  if(!await uiConfirm('Revoke API key',`Revoke key ${who||'#'+id}? Any client using it stops working immediately, and this can't be undone.`,'Revoke','btn danger','var(--red)'))return;
  try{await api('/api/keys/'+id,{method:'DELETE'});loadApiKeys();toast('key revoked');}catch(e){toast(e.message);}
}
$('#keyCreate').onclick=createApiKey;
export async function loadReference(){
  try{const d=await api('/api/reference');$('#apiBase').textContent='Base URL: '+d.baseUrl;
    $('#restList').innerHTML=(d.routes||[]).map(r=>`<tr>
      <td style="color:${methodColor(r.method)};font-weight:700;font-family:var(--mono)">${esc(r.method)}</td>
      <td style="font-family:var(--mono);color:var(--fg)">${esc(r.path)}</td>
      <td style="color:var(--fg2)">${esc(r.desc)}</td></tr>`).join('');
  }catch(e){}
}
export async function loadMCP(){
  try{const m=await api('/api/mcp');
    const httpCfg=JSON.stringify(m.clientConfig||{},null,2);
    const stdioCfg=JSON.stringify(m.stdioClientConfig||{},null,2);
    const cmd=`${(m.transport&&m.transport.command)||'interseptor'} ${((m.transport&&m.transport.args)||[]).join(' ')}`.trim();
    const tools=(m.tools||[]).map(t=>`<tr>
      <td style="font-family:var(--mono);color:var(--accent)">${esc(t.name)}</td>
      <td style="color:var(--fg2)">${esc(t.desc)}</td></tr>`).join('');
    $('#mcpBody').innerHTML=`
      <div class="row" style="gap:8px"><span class="sev ${m.status==='ready'?'Low':'Info'}">${esc(m.status)}</span>
        <span class="hint">Let your AI assistant drive Interseptor — same tools as the UI.</span></div>
      <p class="hint" style="margin:12px 0;line-height:1.6">${esc(m.note||'')}</p>
      <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--accent);margin:14px 0 6px">RECOMMENDED · CURSOR / STREAMABLE HTTP (auto-syncs on restart)</div>
      <div class="row" style="gap:10px;margin:0 0 6px"><span class="hint">Paste into <code>.cursor/mcp.json</code> — uses the running Interseptor, no stale stdio binary.</span><button class="btn accent" id="mcpCopyHttp" style="padding:3px 10px">Copy</button></div>
      <pre class="evidence" style="white-space:pre;overflow:auto;margin-top:0">${esc(httpCfg)}</pre>
      ${m.httpTransport?`<p class="hint" style="margin:8px 0 0;line-height:1.6">Endpoint: <code>${esc(m.httpTransport.url||'')}</code> · ${esc(m.httpTransport.note||'')}</p>`:''}
      <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:16px 0 6px">STDIO · Claude Desktop / separate MCP process</div>
      <div class="evidence" style="font-family:var(--mono);margin-bottom:8px">${esc(cmd)}</div>
      <p class="hint" style="margin:0 0 8px">Windows: <code>scripts/interceptor-mcp.cmd</code> resolves the latest <code>interseptor</code> on PATH after <code>go install</code> / <code>interseptor update</code>.</p>
      <div class="row" style="gap:10px;margin:0 0 6px"><span style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3)">STDIO CLIENT CONFIG</span><button class="btn" id="mcpCopyStdio" style="padding:3px 10px">Copy</button></div>
      <pre class="evidence" style="white-space:pre;overflow:auto;margin-top:0">${esc(stdioCfg)}</pre>
      <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:18px 0 6px">TOOLS · ${(m.tools||[]).length}</div>
      <table class="rules-tbl"><thead><tr><th style="width:160px">Tool</th><th>Description</th></tr></thead><tbody>${tools}</tbody></table>`;
    const cpH=document.getElementById('mcpCopyHttp'); if(cpH) cpH.onclick=()=>copyText(httpCfg,'Cursor MCP config copied');
    const cpS=document.getElementById('mcpCopyStdio'); if(cpS) cpS.onclick=()=>copyText(stdioCfg,'stdio MCP config copied');
  }catch(e){}
}
