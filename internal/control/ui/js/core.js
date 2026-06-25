// core.js — shared foundation imported by every feature module.
// DOM helpers, the global `state` object, the REST helper, formatters, HTTP
// syntax highlighters, copy helpers, the modal system (open/close + focus trap),
// uiPrompt / uiConfirm, and the safe markdown renderer all live here.
//
// Everything is `export`ed; feature modules import what they reference. Mutations
// to the shared `state` object are visible across modules (live object binding).

export const $=s=>document.querySelector(s);
export const $$=s=>Array.from(document.querySelectorAll(s));
export const esc=s=>String(s).replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
// escAttr also escapes quotes — use it for a value interpolated INTO an attribute
// ("${escAttr(x)}"), where a raw " or ' in user-authored rules or captured traffic
// (hosts, paths) would otherwise break out of the attribute. Plain esc() (which
// deliberately keeps quotes) stays for text and the JSON/HTTP highlighters.
export const escAttr=s=>esc(s).replace(/"/g,'&quot;').replace(/'/g,'&#39;');

export const state={flows:[],selId:null,detail:null,intercept:{enabled:false,queue:[]},selHeld:null,selRespHeld:null,
  rules:[],scope:[],views:[],inScopeOnly:false,showAI:true,selected:new Set(),lastSelIdx:-1,aiIds:[],view:{req:'raw',res:'raw'},sort:{key:'id',dir:-1},proxyAddr:'127.0.0.1:8080',
  filters:{scheme:'',search:'',method:'',status:'',host:'',exclude:[]},activity:[],actUnseen:0};

export function toast(m){const t=$('#toast');t.textContent=m;t.classList.add('show');clearTimeout(toast._t);toast._t=setTimeout(()=>t.classList.remove('show'),2200);}
export async function api(path,opts){const r=await fetch(path,opts);if(!r.ok){let m=r.statusText;try{m=(await r.json()).error||m}catch(e){}throw new Error(m);}const ct=r.headers.get('content-type')||'';return ct.includes('json')?r.json():r.text();}

export const methodColor=m=>({GET:'var(--blue)',POST:'var(--accent)',PUT:'var(--amber)',PATCH:'var(--violet)',DELETE:'var(--red)'}[m]||'var(--fg2)');
export const statusColor=s=>!s?'var(--fg3)':s<300?'var(--accent)':s<400?'var(--blue)':s<500?'var(--amber)':'var(--red)';
export const statusText=s=>({200:'OK',201:'Created',204:'No Content',301:'Moved',302:'Found',304:'Not Modified',400:'Bad Request',401:'Unauthorized',403:'Forbidden',404:'Not Found',500:'Internal Server Error',502:'Bad Gateway'}[s]||'');
export const mimeLabel=m=>{if(!m)return'—';if(m.includes('json'))return'json';if(m.includes('html'))return'html';if(m.includes('javascript'))return'script';if(m.includes('css'))return'css';if(m.includes('font')||m.includes('woff'))return'font';if(m.includes('image'))return'image';return(m.split('/')[1]||m).split(';')[0];};
export const fmtSize=n=>!n?'0 B':n<1024?n+' B':n<1048576?(n/1024).toFixed(n<10240?1:0)+' KB':(n/1048576).toFixed(1)+' MB';
// fmtBytes matches the backend/MCP byte-formatting convention exactly:
// < 1 KB → "N B";  < 1 MB → "N.N KB";  < 1 GB → "N.N MB";  else "N.N GB"
export const fmtBytes=n=>{if(!n||n<0)return '0 B';if(n<1024)return n+' B';if(n<1048576)return (n/1024).toFixed(1)+' KB';if(n<1073741824)return (n/1048576).toFixed(1)+' MB';return (n/1073741824).toFixed(1)+' GB';};
export const fmtTime=ms=>{const d=new Date(ms);return d.toLocaleTimeString('en-GB',{hour12:false});};
export const fmtDur=ms=>ms<1000?ms+' ms':(ms/1000).toFixed(ms<10000?2:1)+' s';

export const FLAG_WS=32;
export const FLAG_AI=1024;
export const PRETTY_MAX=256*1024; // only beautify smallish bodies, to stay light
export const RENDER_CAP=2*1024*1024; // bodies larger than this aren't rendered (they lag the browser)

export function beautifyBody(body){
  const t=body.replace(/^﻿/,'').trim();
  if(t&&(t[0]==='{'||t[0]==='[')){try{return JSON.stringify(JSON.parse(t),null,2);}catch(e){}}
  if(t&&t[0]==='<'){try{
    let depth=0,out='';
    t.replace(/>\s*</g,'>\n<').split('\n').forEach(ln=>{
      ln=ln.trim();if(!ln)return;
      if(/^<\//.test(ln))depth=Math.max(0,depth-1);
      out+='  '.repeat(depth)+ln+'\n';
      if(/^<[^!?\/][^>]*[^\/]>$/.test(ln)&&!/^<(area|base|br|col|embed|hr|img|input|link|meta|param|source|track|wbr)\b/i.test(ln))depth++;
    });
    return out.replace(/\s+$/,'');
  }catch(e){}}
  return body;
}
export function prettify(raw){
  const i=raw.indexOf('\r\n\r\n');const head=i>=0?raw.slice(0,i):raw;const body=i>=0?raw.slice(i+4):'';
  if(!body)return raw;
  if(body.length>PRETTY_MAX)return head+'\n\n'+body+'\n\n— body too large to beautify ('+fmtSize(body.length)+'); showing raw —';
  return head+'\n\n'+beautifyBody(body);
}
// Colorize a read-only HTTP message: start line, header names/values, status code.
// The text is escaped first (these are arbitrary captured bytes) so the result is
// safe for innerHTML. Only the header block is tokenized; the body is escaped
// verbatim to stay fast on large payloads.
export function highlightHTTP(text,pretty){
  if(!text)return '';
  text=String(text).replace(/\r\n/g,'\n');
  const sep=text.indexOf('\n\n');
  const head=sep>=0?text.slice(0,sep):text;
  const body=sep>=0?text.slice(sep+2):'';
  const lines=head.split('\n');
  let html=hlStartLine(lines[0]||'');
  for(let i=1;i<lines.length;i++){
    const ln=lines[i];const c=ln.indexOf(':');
    html+='\n'+(c>0
      ?'<span class="hl-hname">'+esc(ln.slice(0,c))+'</span>:<span class="hl-hval">'+esc(ln.slice(c+1))+'</span>'
      :esc(ln));
  }
  if(sep>=0){
    const tb=body.replace(/^\s+/,'');
    const json=pretty&&body.length<=PRETTY_MAX&&(tb[0]==='{'||tb[0]==='[');
    html+='\n\n'+(json?highlightJSON(body):esc(body));
  }
  return html;
}
// Color a (pretty-printed) JSON body: keys, string / number / literal values.
// Escapes first, then tokenizes the escaped text, so the result is safe for
// innerHTML even though the body is arbitrary captured bytes.
export function highlightJSON(s){
  return esc(s).replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,(m,str,colon,lit,num)=>{
    if(str!==undefined)return colon!=null?'<span class="js-key">'+str+'</span>'+colon:'<span class="js-str">'+str+'</span>';
    if(lit!==undefined)return '<span class="js-lit">'+lit+'</span>';
    return '<span class="js-num">'+num+'</span>';
  });
}
export function hlStartLine(ln){
  let m=ln.match(/^(HTTP\/[\d.]+)\s+(\d{3})(.*)$/);            // response status line
  if(m)return '<span class="hl-proto">'+esc(m[1])+'</span> <span class="hl-st'+m[2][0]+'">'+esc(m[2]+m[3])+'</span>';
  m=ln.match(/^([A-Z]+)\s+(\S+)\s+(HTTP\/[\d.]+)$/);           // request line
  if(m)return '<span class="hl-method">'+esc(m[1])+'</span> <span class="hl-url">'+esc(m[2])+'</span> <span class="hl-proto">'+esc(m[3])+'</span>';
  return esc(ln);
}

/* ---- copy helpers ---- */
export function copyText(t,msg){
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(t).then(()=>toast(msg||'copied')).catch(()=>fallbackCopy(t,msg));}
  else fallbackCopy(t,msg);
}
export function fallbackCopy(t,msg){const ta=document.createElement('textarea');ta.value=t;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');toast(msg||'copied');}catch(e){toast('copy failed');}document.body.removeChild(ta);}

/* ---- modal focus-trap helper ---- */
// Tracks which element had focus before a modal opened so we can restore it.
const _modalFocusStack=[];
export const FOCUSABLE='a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';
export function openModal(modalEl){
  _modalFocusStack.push(document.activeElement);
  modalEl.style.display='flex';
  // Move focus into the dialog — prefer its first close button, else first focusable.
  const inner=modalEl.querySelector('[role="dialog"]');
  if(!inner)return;
  const first=inner.querySelector('button.btn')||inner.querySelector(FOCUSABLE);
  if(first)setTimeout(()=>first.focus(),0);
}
export function closeModal(modalEl){
  modalEl.style.display='none';
  const prev=_modalFocusStack.pop();
  if(prev&&typeof prev.focus==='function')prev.focus();
}
// Tab / Shift+Tab cycling within the open modal (focus trap).
document.addEventListener('keydown',e=>{
  if(e.key!=='Tab')return;
  // Find the topmost open modal with role="dialog"
  const openDialog=$$('[role="dialog"]').find(d=>d.closest('[id]')&&d.closest('[id]').style.display==='flex');
  if(!openDialog)return;
  const focusable=Array.from(openDialog.querySelectorAll(FOCUSABLE)).filter(el=>!el.disabled&&el.offsetParent!==null);
  if(!focusable.length){e.preventDefault();return;}
  const first=focusable[0],last=focusable[focusable.length-1];
  if(e.shiftKey){if(document.activeElement===first){e.preventDefault();last.focus();}}
  else{if(document.activeElement===last){e.preventDefault();first.focus();}}
});

/* ---- modals: close on Escape and on backdrop click (consistent across all) ---- */
export const MODAL_IDS=['aiModal','checksModal','activeModal','oobModal','authzModal','decModal','flowModal','confirmModal','shortcutsModal'];
export function closeModals(){let n=0;MODAL_IDS.forEach(id=>{const m=$('#'+id);if(m&&m.style.display&&m.style.display!=='none'){closeModal(m);n++;}});return n>0;}
MODAL_IDS.forEach(id=>{const m=$('#'+id);if(m)m.addEventListener('mousedown',e=>{if(e.target===m)closeModal(m);});});

// uiPrompt: an in-app replacement for the browser's prompt() — themed, consistent,
// resolves to the entered string or null (Cancel / Escape / backdrop / empty).
export function uiPrompt(opts){
  opts=opts||{};
  return new Promise(resolve=>{
    const m=$('#promptModal'),inp=$('#promptInput');
    $('#promptTitle').textContent=opts.title||'Enter a value';
    inp.placeholder=opts.placeholder||'';inp.value=opts.value||'';
    openModal(m);
    setTimeout(()=>{inp.focus();inp.select();},0);
    let done=false;
    const finish=v=>{if(done)return;done=true;closeModal(m);inp.onkeydown=null;m.onmousedown=null;resolve(v);};
    $('#promptOk').onclick=()=>finish(inp.value.trim()||null);
    $('#promptCancel').onclick=()=>finish(null);
    inp.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();finish(inp.value.trim()||null);}else if(e.key==='Escape'){e.preventDefault();finish(null);}};
    m.onmousedown=e=>{if(e.target===m)finish(null);};
  });
}

// uiConfirm: themed confirm dialog reusing the promptModal structure.
// Returns a Promise<boolean>.
export function uiConfirm(title,htmlMsg,okLabel,okClass,okColor){
  return new Promise(resolve=>{
    const m=$('#confirmModal');
    if(!m){resolve(window.confirm(title+'\n\n'+htmlMsg.replace(/<[^>]+>/g,'')));return;}
    $('#confirmTitle').textContent=title;
    $('#confirmMsg').innerHTML=htmlMsg;
    const ok=$('#confirmOk');
    ok.textContent=okLabel||'OK';
    ok.className=(okClass||'btn accent');
    if(okColor)ok.style.color=okColor; else ok.style.color='';
    openModal(m);
    let done=false;
    const finish=v=>{if(done)return;done=true;closeModal(m);ok.onclick=null;$('#confirmCancel').onclick=null;m.onmousedown=null;m.onkeydown=null;resolve(v);};
    ok.onclick=()=>finish(true);
    $('#confirmCancel').onclick=()=>finish(false);
    m.onmousedown=e=>{if(e.target===m)finish(false);};
    // Escape key resolves false (cancel) so the promise doesn't hang when the
    // user presses Escape — the global Escape handler calls closeModal() but
    // that alone doesn't resolve our Promise.
    m.onkeydown=e=>{if(e.key==='Escape'){e.stopPropagation();finish(false);}};
  });
}

// Minimal, safe markdown → HTML: escape first, then format a useful subset.
export function renderMD(src){
  if(!src||!src.trim())return '<p class="hint">Empty — switch to Edit and jot down creds, findings, scope…</p>';
  let s=esc(src),blocks=[];
  s=s.replace(/```([\s\S]*?)```/g,(m,c)=>{blocks.push(c.replace(/^\n|\n$/g,''));return ' '+(blocks.length-1)+' ';});
  s=s.replace(/^######\s?(.*)$/gm,'<h6>$1</h6>').replace(/^#####\s?(.*)$/gm,'<h5>$1</h5>').replace(/^####\s?(.*)$/gm,'<h4>$1</h4>').replace(/^###\s?(.*)$/gm,'<h3>$1</h3>').replace(/^##\s?(.*)$/gm,'<h2>$1</h2>').replace(/^#\s?(.*)$/gm,'<h1>$1</h1>');
  s=s.replace(/(?:^|\n)((?:[-*] .*(?:\n|$))+)/g,(m,list)=>'\n<ul>'+list.trim().split('\n').map(l=>'<li>'+l.replace(/^[-*]\s?/,'')+'</li>').join('')+'</ul>');
  s=s.replace(/!\[([^\]]*)\]\((https?:\/\/[^\s")]+|data:image\/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]+)\)/g,(m,alt,src)=>'<img class="md-img" alt="'+alt.replace(/"/g,'&quot;')+'" src="'+src.replace(/"/g,'&quot;')+'">');
  s=s.replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>').replace(/`([^`\n]+)`/g,'<code>$1</code>');
  s=s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|mailto:[^\s)]+)\)/g,'<a href="$2" target="_blank" rel="noopener">$1</a>');
  s='<p>'+s.replace(/\n{2,}/g,'</p><p>').replace(/\n/g,'<br>')+'</p>';
  s=s.replace(/ (\d+) /g,(m,i)=>'<pre class="md-code">'+blocks[Number(i)]+'</pre>');
  return s.replace(/<p>\s*<\/p>/g,'').replace(/<p>(<(?:h\d|ul|pre))/g,'$1').replace(/(<\/(?:h\d|ul|pre)>)<\/p>/g,'$1');
}
// ---- binary-body detection (inspector shows headers only for non-text bodies) ----

// bodyMime returns the body's content type for a side: the response MIME for "res",
// or the request's Content-Type header for "req".
export function bodyMime(detail, side){
  if(!detail) return '';
  if(side==='res') return detail.mime||'';
  const h=detail.reqHeaders||{};
  for(const k in h){ if(k.toLowerCase()==='content-type') return (h[k]&&h[k][0])||''; }
  return '';
}
// isBinaryMime is true for content that isn't human-readable as text (images, media,
// fonts, archives, office/binary docs). SVG and the text/* family stay printable.
export function isBinaryMime(m){
  if(!m) return false;
  m=m.toLowerCase();
  if(m.indexOf('svg')>=0) return false;
  if(m.startsWith('image/')||m.startsWith('audio/')||m.startsWith('video/')||m.startsWith('font/')) return true;
  return /(octet-stream|pdf|zip|gzip|wasm|protobuf|msword|ms-excel|ms-powerpoint|officedocument|vnd\.|woff|x-font|x-7z|x-rar|x-tar|x-msdownload|dmg|iso)/.test(m);
}
function headerLines(h){const out=[];if(h)Object.keys(h).forEach(k=>(h[k]||[]).forEach(v=>out.push(k+': '+v)));return out.join('\n');}
// headerBlockText reconstructs just the HTTP header block (start line + headers) from
// a flow-detail DTO — no body, no network fetch — for rendering binary flows.
export function headerBlockText(detail, side){
  if(!detail) return '';
  if(side==='req') return `${detail.method||'GET'} ${detail.path||'/'} ${detail.httpVersion||'HTTP/1.1'}\n`+headerLines(detail.reqHeaders);
  const line=`${detail.httpVersion||'HTTP/1.1'} ${detail.status||''} ${statusText(detail.status)}`.replace(/\s+$/,'');
  return line+'\n'+headerLines(detail.resHeaders);
}

// Wrap each heading + the content beneath it in a collapsible <details>, so long
// notes fold into title/subtitle sections you can open and close.
export function accordionize(box){
  const kids=[...box.childNodes],frag=document.createDocumentFragment();let i=0;
  while(i<kids.length){
    const n=kids[i];
    if(n.nodeType===1&&/^H[1-6]$/.test(n.tagName)){
      const det=document.createElement('details');det.className='acc';det.open=true;
      const sum=document.createElement('summary');sum.className='acc-h';sum.appendChild(n);det.appendChild(sum);
      const body=document.createElement('div');body.className='acc-body';i++;
      while(i<kids.length&&!(kids[i].nodeType===1&&/^H[1-6]$/.test(kids[i].tagName))){body.appendChild(kids[i]);i++;}
      det.appendChild(body);frag.appendChild(det);
    }else{frag.appendChild(n);i++;}
  }
  box.appendChild(frag);
}
