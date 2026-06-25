import { $, esc, api, toast, methodColor, copyText } from './core.js';

/* ---- api module ---- */
$('#apiSub').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  $('#apiSub').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));
  ['Keys','Rest','Mcp'].forEach(s=>$('#api'+s).style.display=(s.toLowerCase()===b.dataset.s)?'block':'none');
});
export async function loadApiKeys(){
  try{const d=await api('/api/keys');const keys=d.keys||[];
    $('#keyList').innerHTML=keys.length?keys.map(k=>`<tr>
      <td style="font-family:var(--mono);color:var(--accent)">${esc(k.prefix)}…</td>
      <td>${esc(k.label)}</td>
      <td style="color:var(--fg3)">${new Date(k.created).toLocaleString()}</td>
      <td><button class="btn danger" data-revoke="${k.id}">Revoke</button></td></tr>`).join('')
      :'<tr><td colspan="4" class="hint" style="padding:10px">No keys yet.</td></tr>';
    $('#keyList').querySelectorAll('[data-revoke]').forEach(b=>b.onclick=()=>revokeKey(Number(b.dataset.revoke)));
  }catch(e){}
}
export async function createApiKey(){
  const label=$('#keyLabel').value.trim()||'key';
  try{const d=await api('/api/keys',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({label})});
    $('#keyNew').style.display='block';
    $('#keyNew').innerHTML='New token — copy now, it is shown only once:<br><b style="color:var(--accent);user-select:all">'+esc(d.token)+'</b>';
    $('#keyLabel').value='';loadApiKeys();
  }catch(e){toast(e.message);}
}
export async function revokeKey(id){try{await api('/api/keys/'+id,{method:'DELETE'});loadApiKeys();}catch(e){toast(e.message);}}
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
    const cfg=JSON.stringify(m.clientConfig||{},null,2);
    const cmd=`${(m.transport&&m.transport.command)||'interceptor'} ${((m.transport&&m.transport.args)||[]).join(' ')}`.trim();
    const tools=(m.tools||[]).map(t=>`<tr>
      <td style="font-family:var(--mono);color:var(--accent)">${esc(t.name)}</td>
      <td style="color:var(--fg2)">${esc(t.desc)}</td></tr>`).join('');
    $('#mcpBody').innerHTML=`
      <div class="row" style="gap:8px"><span class="sev ${m.status==='ready'?'Low':'Info'}">${esc(m.status)}</span>
        <span class="hint">Let your AI assistant drive Interceptor — it gets the same capabilities as the UI.</span></div>
      <p class="hint" style="margin:12px 0;line-height:1.6">${esc(m.note||'')}</p>
      <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:14px 0 6px">CONNECT (stdio · Claude Desktop / Code)</div>
      <div class="evidence" style="font-family:var(--mono)">${esc(cmd)}</div>
      ${m.httpTransport?`<div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:16px 0 6px">OR CONNECT (HTTP · remote / hosted agents)</div>
      <div class="evidence" style="font-family:var(--mono)">POST ${esc(location.origin)}${esc(m.httpTransport.url)}</div>
      <p class="hint" style="margin:8px 0 0;line-height:1.6">${esc(m.httpTransport.note||'')}</p>`:''}
      <div class="row" style="gap:10px;margin:16px 0 6px"><span style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3)">CLIENT CONFIG · paste into your MCP client (e.g. Claude)</span><button class="btn accent" id="mcpCopy" style="padding:3px 10px">Copy</button></div>
      <pre class="evidence" style="white-space:pre;overflow:auto;margin-top:0">${esc(cfg)}</pre>
      <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:18px 0 6px">TOOLS · ${(m.tools||[]).length}</div>
      <table class="rules-tbl"><thead><tr><th style="width:160px">Tool</th><th>Description</th></tr></thead><tbody>${tools}</tbody></table>`;
    const cp=document.getElementById('mcpCopy'); if(cp) cp.onclick=()=>copyText(cfg,'MCP config copied');
  }catch(e){}
}
