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

export const state={flows:[],selId:null,detail:null,intercept:{enabled:false,queue:[]},
  rules:[],scope:[],views:[],inScopeOnly:false,discoveryOnly:false,showAI:true,aiDisabled:false,flowTruncated:false,selected:new Set(),lastSelIdx:-1,aiIds:[],view:{req:'pretty',res:'pretty'},sort:{key:'id',dir:-1},proxyAddr:'127.0.0.1:8080',
  filters:{scheme:'',search:'',searchScope:'path',method:'',status:'',host:'',tag:'',exclude:[]},notesOnly:false,activity:[],actUnseen:0,flowCols:['id','method','host','path','status','size','time'],oobEnabled:false};

export function toast(m){const t=$('#toast');t.textContent=m;t.classList.add('show');clearTimeout(toast._t);toast._t=setTimeout(()=>t.classList.remove('show'),2200);}

// wireRowKey makes a clickable <div> row keyboard-operable: it becomes a focusable
// button to assistive tech and Enter/Space activate it. `onActivate` defaults to a
// synthetic click so existing onclick handlers keep working unchanged. Idempotent —
// re-wiring the same element (rows are recycled on re-render) won't stack listeners.
export function wireRowKey(el, onActivate){
  if(!el||el._rowKeyed) return el;
  el._rowKeyed=true;
  if(!el.hasAttribute('role')) el.setAttribute('role','button');
  if(!el.hasAttribute('tabindex')) el.tabIndex=0;
  el.addEventListener('keydown',e=>{
    if(e.key!=='Enter'&&e.key!==' ') return;
    // Let Enter/Space fall through when focus is on a real control inside the row
    // (button/input/textarea/select/link) — those have their own activation.
    const t=e.target;
    if(t!==el&&t.closest&&t.closest('button,input,textarea,select,a')) return;
    e.preventDefault();
    if(onActivate) onActivate(e); else el.click();
  });
  return el;
}

// setSeg toggles a segmented-control button's pressed state, keeping the visual
// `.on` class and the ARIA `aria-pressed` attribute in sync for screen readers.
export function setSeg(btn,on){
  if(!btn) return;
  btn.classList.toggle('on',!!on);
  btn.setAttribute('aria-pressed',on?'true':'false');
}
export async function api(path,opts){const r=await fetch(path,opts);if(!r.ok){let m=r.statusText;try{m=(await r.json()).error||m}catch(e){}throw new Error(m);}const ct=r.headers.get('content-type')||'';return ct.includes('json')?r.json():r.text();}

export const methodColor=m=>({GET:'var(--blue)',POST:'var(--accent)',PUT:'var(--amber)',PATCH:'var(--violet)',DELETE:'var(--red)'}[m]||'var(--fg2)');
export const statusColor=s=>!s?'var(--fg3)':s<300?'var(--accent)':s<400?'var(--blue)':s<500?'var(--amber)':'var(--red)';
export const statusText=s=>({200:'OK',201:'Created',204:'No Content',301:'Moved',302:'Found',304:'Not Modified',400:'Bad Request',401:'Unauthorized',403:'Forbidden',404:'Not Found',500:'Internal Server Error',502:'Bad Gateway'}[s]||'');
export const mimeLabel=m=>{if(!m)return'—';if(m.includes('json'))return'json';if(m.includes('html'))return'html';if(m.includes('javascript'))return'script';if(m.includes('css'))return'css';if(m.includes('font')||m.includes('woff'))return'font';if(m.includes('image'))return'image';return(m.split('/')[1]||m).split(';')[0];};
// mimeExt maps a Content-Type to a file extension for body downloads.
export function mimeExt(mime){
  if(!mime) return 'bin';
  const m=mime.toLowerCase().split(';')[0].trim();
  const map={
    'application/json':'json','text/html':'html','text/css':'css','text/plain':'txt',
    'application/javascript':'js','text/javascript':'js','application/x-javascript':'js',
    'application/xml':'xml','text/xml':'xml','application/xhtml+xml':'html',
    'application/pdf':'pdf','image/png':'png','image/jpeg':'jpg','image/gif':'gif',
    'image/webp':'webp','image/svg+xml':'svg','image/x-icon':'ico',
    'application/zip':'zip','application/gzip':'gz','application/x-gzip':'gz',
    'application/octet-stream':'bin','application/wasm':'wasm',
    'video/mp4':'mp4','video/webm':'webm','audio/mpeg':'mp3','audio/ogg':'ogg',
    'font/woff':'woff','font/woff2':'woff2',
  };
  if(map[m]) return map[m];
  if(m.startsWith('text/')){const sub=m.slice(5);return sub==='plain'||!sub?'txt':sub.split('+')[0];}
  if(m.startsWith('image/')||m.startsWith('audio/')||m.startsWith('video/')){
    return m.split('/')[1].split('+')[0].replace(/^x-/,'');
  }
  if(m.includes('json')) return 'json';
  if(m.includes('xml')) return 'xml';
  if(m.includes('html')) return 'html';
  return 'bin';
}
export function flowBodyDownloadName(flowId,side,mime){return `flow-${flowId}-${side}.${mimeExt(mime)}`;}
export function flowBodyDownloadHref(flowId,side){return `/api/flows/${flowId}/body?side=${side}`;}
export const fmtSize=n=>!n?'0 B':n<1024?n+' B':n<1048576?(n/1024).toFixed(n<10240?1:0)+' KB':(n/1048576).toFixed(1)+' MB';
// fmtBytes matches the backend/MCP byte-formatting convention exactly:
// < 1 KB → "N B";  < 1 MB → "N.N KB";  < 1 GB → "N.N MB";  else "N.N GB"
export const fmtBytes=n=>{if(!n||n<0)return '0 B';if(n<1024)return n+' B';if(n<1048576)return (n/1024).toFixed(1)+' KB';if(n<1073741824)return (n/1048576).toFixed(1)+' MB';return (n/1073741824).toFixed(1)+' GB';};
export const fmtTime=ms=>{const d=new Date(ms);return d.toLocaleTimeString('en-GB',{hour12:false});};
export const fmtDur=ms=>ms<1000?ms+' ms':(ms/1000).toFixed(ms<10000?2:1)+' s';

export const FLAG_WS=32;
export const FLAG_AI=1024;
export const FLAG_DISCOVERY=4096;
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
// bodyHighlightKind picks a syntax highlighter from Content-Type and a quick sniff.
export function bodyHighlightKind(body,mime){
  const m=(mime||'').toLowerCase().split(';')[0].trim();
  const t=String(body).replace(/^\uFEFF/,'').replace(/^\s+/,'');
  if(m.includes('json')||t[0]==='{'||t[0]==='[') return 'json';
  if(m.includes('html')||m.includes('xml')||m.includes('svg')||/^<\?xml\b/i.test(t)||/^<!DOCTYPE/i.test(t)||(/^</.test(t)&&/>/.test(t))) return 'markup';
  if(m==='text/css') return 'css';
  return '';
}
// contentTypeFromRaw reads Content-Type from a raw HTTP message (req or res).
export function contentTypeFromRaw(raw){
  const s=String(raw).replace(/\r\n/g,'\n');
  const sep=s.indexOf('\n\n');
  const head=sep>=0?s.slice(0,sep):s;
  const m=head.match(/^content-type:\s*(\S.*?)(?:\s*;|\s*$)/im);
  return m?m[1].trim():'';
}
function highlightBody(body,pretty,mime){
  if(!pretty||body.length>PRETTY_MAX) return esc(body);
  const kind=bodyHighlightKind(body,mime);
  if(kind==='json') return highlightJSON(body);
  if(kind==='markup') return highlightMarkup(body);
  if(kind==='css') return highlightCSS(body);
  return esc(body);
}
// Colorize a read-only HTTP message: start line, header names/values, status code.
// The text is escaped first (these are arbitrary captured bytes) so the result is
// safe for innerHTML. Only the header block is tokenized; the body is escaped
// verbatim to stay fast on large payloads.
export function highlightHTTP(text,pretty,mime){
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
  if(sep>=0) html+='\n\n'+highlightBody(body,pretty,mime||contentTypeFromRaw(text));
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
// Color HTML / XML markup (tags, attributes, comments).
export function highlightMarkup(s){
  const e=esc(s);
  return e.replace(/&lt;!--[\s\S]*?--&gt;/g,m=>'<span class="hl-cmt">'+m+'</span>')
    .replace(/&lt;(\/?)([\w:.-]+)([\s\S]*?)&gt;/g,(m,slash,tag,rest)=>{
      const attrs=rest.replace(/(\s+)([\w:.-]+)(\s*=\s*)(&quot;[^&]*?&quot;|&#39;[^&]*?&#39;|[^\s&gt;]+)/g,
        '$1<span class="hl-attr">$2</span>$3<span class="hl-str">$4</span>');
      return '&lt;'+slash+'<span class="hl-tag">'+tag+'</span>'+attrs+'&gt;';
    });
}
// Color CSS (comments, at-rules, selectors, properties, values).
export function highlightCSS(s){
  const e=esc(s);
  return e.replace(/\/\*[\s\S]*?\*\//g,m=>'<span class="hl-cmt">'+m+'</span>')
    .replace(/@[\w-]+/g,m=>'<span class="hl-at">'+m+'</span>')
    .replace(/([\w#.: \[\]()>+~*,\\-]+)(\s*\{)/g,'<span class="hl-sel">$1</span>$2')
    .replace(/([\w-]+)(\s*:)/g,'<span class="hl-prop">$1</span>$2')
    .replace(/(:\s*)(&quot;[^&]*?&quot;|#[0-9a-fA-F]{3,8}|\b\d+(?:\.\d+)?(?:px|em|rem|%|s|ms|deg|vh|vw|ch|ex)?\b)/g,'$1<span class="hl-val">$2</span>');
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

// saveFile opens the native Save dialog when available (File System Access API),
// otherwise triggers a download with the suggested filename.
export async function saveFile(blob,suggestedName,mimeType){
  const name=suggestedName||'download';
  const type=mimeType||blob.type||'application/octet-stream';
  if(window.showSaveFilePicker){
    try{
      const ext=name.includes('.')?name.slice(name.lastIndexOf('.')).toLowerCase():'';
      const types=[];
      if(ext==='.har')types.push({description:'HTTP Archive',accept:{'application/json':['.har']}});
      const handle=await window.showSaveFilePicker({suggestedName:name,types:types.length?types:undefined});
      const writable=await handle.createWritable();
      await writable.write(blob instanceof Blob?blob:new Blob([blob],{type}));
      await writable.close();
      return handle.name;
    }catch(e){
      if(e&&e.name==='AbortError')throw e;
    }
  }
  const url=URL.createObjectURL(blob instanceof Blob?blob:new Blob([blob],{type}));
  const a=document.createElement('a');
  a.href=url;a.download=name;a.click();
  URL.revokeObjectURL(url);
  return name;
}

const MAX_LIST_FILE = 16 * 1024 * 1024; // SecLists-scale wordlists

// normalizeListText converts CRLF/CR to LF for pasted or loaded list files.
export function normalizeListText(text){
  return String(text||'').replace(/\r\n/g,'\n').replace(/\r/g,'\n');
}

// applyTextList writes text into a textarea/input; append joins with a newline.
export function applyTextList(el, text, {append=false}={}){
  if(!el) return;
  const t=normalizeListText(text);
  if(append&&el.value.trim()) el.value=el.value.trimEnd()+'\n'+t;
  else el.value=t;
}

// pickTextFile opens a native file picker and reads the chosen file as UTF-8 text.
export function pickTextFile({accept='.txt,.lst,.csv,.words,text/plain',maxBytes=MAX_LIST_FILE}={}){
  return new Promise((resolve,reject)=>{
    const input=document.createElement('input');
    input.type='file';
    input.accept=accept;
    input.style.display='none';
    input.onchange=()=>{
      const file=input.files&&input.files[0];
      input.remove();
      if(!file){resolve(null);return;}
      if(file.size>maxBytes){
        reject(new Error('file too large ('+fmtSize(file.size)+' — max '+fmtSize(maxBytes)+')'));
        return;
      }
      const reader=new FileReader();
      reader.onload=()=>resolve({text:String(reader.result||''),name:file.name,size:file.size});
      reader.onerror=()=>reject(new Error('could not read file'));
      reader.readAsText(file);
    };
    document.body.appendChild(input);
    input.click();
  });
}

export function countListLines(text, ignoreComments=false){
  return normalizeListText(text).split('\n').filter(l=>{
    const t=l.trim();
    return t&&(ignoreComments?!t.startsWith('#'):true);
  }).length;
}

/* ---- shared right-click context menu (#ctxmenu) ---- */
export function hideCtxMenu(){
  const ctx=$('#ctxmenu');if(!ctx)return;
  if(ctx._keyHandler){document.removeEventListener('keydown',ctx._keyHandler);ctx._keyHandler=null;}
  ctx.classList.remove('show');ctx._acts=null;
}

// openCtxMenu renders sectioned items on #ctxmenu and positions at (x,y).
export function openCtxMenu(x,y,sections){
  const ctx=$('#ctxmenu');if(!ctx)return;
  ctx.setAttribute('role','menu');
  const acts=[];let html='';
  sections.forEach(sec=>{
    const items=(sec.items||[]).filter(Boolean);
    if(!items.length)return;
    if(sec.head)html+=`<div class="ctx-head">${esc(sec.head)}</div>`;
    items.forEach(it=>{
      if(it.sep){html+='<div class="ctx-sep"></div>';return;}
      const dStyle=it.danger?' style="color:var(--red)"':'';
      const right=it.val!=null?`<span class="mono"${dStyle}>${esc(it.val)}</span>`:'';
      html+=`<div class="ctx-item${it.on?' on':''}" role="menuitem" data-i="${acts.length}"${it.danger&&it.val==null?dStyle:''}><span class="lbl"${dStyle}>${esc(it.label)}</span>${right}</div>`;
      acts.push(it.act);
    });
  });
  if(!acts.length)return;
  ctx.innerHTML=html;ctx._acts=acts;ctx._sel=0;
  const items=ctx.querySelectorAll('.ctx-item');
  items.forEach((el,i)=>el.classList.toggle('on',i===0));
  ctx.querySelectorAll('[data-i]').forEach(el=>el.onclick=()=>{const fn=ctx._acts[Number(el.dataset.i)];hideCtxMenu();if(fn)fn();});
  ctx.style.left=x+'px';ctx.style.top=y+'px';ctx.classList.add('show');
  const r=ctx.getBoundingClientRect();
  if(r.right>innerWidth)ctx.style.left=Math.max(4,x-r.width)+'px';
  if(r.bottom>innerHeight)ctx.style.top=Math.max(4,y-r.height)+'px';
  const paintSel=()=>{items.forEach((el,i)=>el.classList.toggle('on',i===ctx._sel));const cur=items[ctx._sel];if(cur)cur.scrollIntoView({block:'nearest'});};
  ctx._keyHandler=e=>{
    if(!ctx.classList.contains('show'))return;
    if(e.key==='ArrowDown'){e.preventDefault();ctx._sel=Math.min(items.length-1,ctx._sel+1);paintSel();}
    else if(e.key==='ArrowUp'){e.preventDefault();ctx._sel=Math.max(0,ctx._sel-1);paintSel();}
    else if(e.key==='Enter'){e.preventDefault();const fn=ctx._acts[ctx._sel];hideCtxMenu();if(fn)fn();}
    else if(e.key==='Escape'){e.preventDefault();hideCtxMenu();}
  };
  document.addEventListener('keydown',ctx._keyHandler);
}

export const DEC_OPS=[['base64decode','Base64 ↓'],['base64encode','Base64 ↑'],['urldecode','URL ↓'],['urlencode','URL ↑'],['hexdecode','Hex ↓'],['hexencode','Hex ↑'],['htmldecode','HTML ↓'],['htmlencode','HTML ↑'],['jwtdecode','JWT'],['smart','✨ Smart']];

// selectionFromRaw maps a DOM selection to the underlying source text when the
// view is pretty-printed/highlighted (span boundaries can split tokens).
export function selectionFromRaw(viewEl, sel){
  if(!sel||!viewEl?._rawText)return sel;
  const raw=viewEl._rawText;
  if(raw.includes(sel))return sel;
  const compact=sel.replace(/\s+/g,'');
  if(compact.length>=4){
    const re=new RegExp(compact.split('').map(c=>c.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('\\s*'),'i');
    const m=raw.match(re);
    if(m)return m[0];
  }
  const off=selectionOffset(viewEl);
  if(off>=0&&off+sel.length<=raw.length){
    const slice=raw.slice(off,off+sel.length);
    if(slice.replace(/\s+/g,'')===compact)return slice;
  }
  return sel;
}

function selectionOffset(root){
  const s=window.getSelection&&window.getSelection();
  if(!s||!s.rangeCount||!root.contains(s.anchorNode))return -1;
  const range=s.getRangeAt(0);
  const pre=document.createRange();
  pre.selectNodeContents(root);
  pre.setEnd(range.startContainer,range.startOffset);
  return pre.toString().length;
}

export function selectionWithin(el){
  const s=window.getSelection&&window.getSelection();
  if(!s||!s.rangeCount)return '';
  if(el&&s.anchorNode&&!el.contains(s.anchorNode))return '';
  let t=String(s);
  if(!t)t=s.getRangeAt(0).toString();
  return t.trim();
}

export function encodeKindLabel(s){
  const t=s.trim();
  if(/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$/.test(t))return 'JWT';
  if(t.includes('%'))return 'URL';
  if(t.length>=4&&t.length%2===0&&/^[0-9a-fA-F]+$/.test(t))return 'Hex';
  if(t.length>=8&&/^[A-Za-z0-9+/=_-]+$/.test(t))return 'Base64';
  return 'Decoded';
}

// wireSelectionDecode shows a slim decode strip when highlighted text looks encoded.
export function wireSelectionDecode(viewEl, barEl, {onDecoder}={}){
  if(!viewEl||!barEl)return;
  let timer=null,lastSel='',req=0;
  const hide=()=>{barEl.hidden=true;lastSel='';};
  const run=async()=>{
    const sel=selectionFromRaw(viewEl,selectionWithin(viewEl));
    if(!sel||sel.length<4||sel.length>8192){hide();return;}
    if(sel===lastSel)return;
    const id=++req;
    try{
      const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op:'smart',input:sel})});
      if(id!==req)return;
      if(r.error||!r.output||r.output===sel){hide();return;}
      lastSel=sel;
      barEl.hidden=false;
      barEl.querySelector('.sel-decode-kind').textContent=encodeKindLabel(sel);
      const outEl=barEl.querySelector('.sel-decode-out');
      const full=r.output;
      outEl.textContent=full.length>500?full.slice(0,500)+'…':full;
      outEl.title=full.length>500?full:'';
      barEl._full=full;barEl._input=sel;
    }catch(e){if(id===req)hide();}
  };
  const schedule=()=>{clearTimeout(timer);timer=setTimeout(run,200);};
  viewEl.addEventListener('mouseup',schedule);
  viewEl.addEventListener('keyup',schedule);
  document.addEventListener('selectionchange',()=>{
    const s=window.getSelection();
    if(!s||!s.anchorNode||!viewEl.contains(s.anchorNode)){if(lastSel)hide();return;}
    schedule();
  });
  barEl.querySelector('.sel-decode-copy')?.addEventListener('click',()=>copyText(barEl._full||'','decoded copied'));
  barEl.querySelector('.sel-decode-open')?.addEventListener('click',()=>{if(onDecoder){const r=onDecoder(barEl._input||'');if(r&&typeof r.then==='function')r.catch(()=>{});}});
  barEl.querySelector('.sel-decode-close')?.addEventListener('click',hide);
  return hide;
}

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
export const MODAL_IDS=['aiModal','notesAiModal','checksModal','activeModal','oobModal','authzModal','decModal','flowModal','confirmModal','shortcutsModal','projModal','compareModal','findCreateModal','findPickModal'];
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
  s=s.replace(/```(\w*)\r?\n?([\s\S]*?)```/g,(m,lang,code)=>{
    const i=blocks.length;
    blocks.push({code:code.replace(/^\n|\n$/g,''),lang:(lang||'').trim().toLowerCase()});
    return '\x00'+i+'\x00';
  });
  s=s.replace(/^---+\s*$/gm,'<hr class="md-hr">');
  s=s.replace(/^######\s?(.*)$/gm,'<h6>$1</h6>').replace(/^#####\s?(.*)$/gm,'<h5>$1</h5>').replace(/^####\s?(.*)$/gm,'<h4>$1</h4>').replace(/^###\s?(.*)$/gm,'<h3>$1</h3>').replace(/^##\s?(.*)$/gm,'<h2>$1</h2>').replace(/^#\s?(.*)$/gm,'<h1>$1</h1>');
  s=s.replace(/(?:^|\n)((?:\|[^\n]+\|\r?\n)+)/g,(m,block)=>{
    const lines=block.trim().split(/\r?\n/).filter(l=>l.trim());
    if(lines.length<2||!/^\|[\s:|-]+\|$/.test(lines[1]))return m;
    const split=row=>row.split('|').slice(1,-1).map(c=>c.trim());
    const hdr=split(lines[0]);
    const body=lines.slice(2).map(split);
    let html='<table class="md-table"><thead><tr>'+hdr.map(h=>'<th>'+h+'</th>').join('')+'</tr></thead><tbody>';
    body.forEach(r=>{html+='<tr>'+r.map(c=>'<td>'+c+'</td>').join('')+'</tr>';});
    return html+'</tbody></table>';
  });
  s=s.replace(/(?:^|\n)((?:>\s?.*(?:\r?\n|$))+)/g,(m,block)=>{
    const inner=block.trim().split(/\r?\n/).map(l=>l.replace(/^>\s?/,'')).join('\n');
    return '<blockquote class="md-quote">'+inner.replace(/\n/g,'<br>')+'</blockquote>';
  });
  s=s.replace(/(?:^|\n)((?:[-*] \[[ xX]\] .*(?:\r?\n|$))+)/g,(m,list)=>{
    const items=list.trim().split(/\r?\n/).map(l=>{
      const t=l.match(/^[-*] \[([ xX])\] (.*)$/);
      const done=t&&t[1]!==' ';
      return '<li class="md-task'+(done?' done':'')+'"><input type="checkbox" disabled'+(done?' checked':'')+'> '+((t&&t[2])||l)+'</li>';
    });
    return '\n<ul class="md-tasks">'+items.join('')+'</ul>';
  });
  s=s.replace(/(?:^|\n)((?:[-*] (?!\[[ xX]\] ).*(?:\r?\n|$))+)/g,(m,list)=>'\n<ul>'+list.trim().split(/\r?\n/).map(l=>'<li>'+l.replace(/^[-*]\s?/,'')+'</li>').join('')+'</ul>');
  s=s.replace(/(?:^|\n)((?:\d+\. .*(?:\r?\n|$))+)/g,(m,list)=>'\n<ol>'+list.trim().split(/\r?\n/).map(l=>'<li>'+l.replace(/^\d+\.\s?/,'')+'</li>').join('')+'</ol>');
  s=s.replace(/!\[([^\]]*)\]\((https?:\/\/[^\s")]+|\/api\/notes\/images\/\d+|data:image\/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]+)\)/g,(m,alt,src)=>'<img class="md-img" alt="'+alt.replace(/"/g,'&quot;')+'" src="'+src.replace(/"/g,'&quot;')+'">');
  s=s.replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>');
  s=s.replace(/(?<!\*)\*([^*\n]+)\*(?!\*)/g,'<em>$1</em>');
  s=s.replace(/`([^`\n]+)`/g,'<code>$1</code>');
  s=s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|mailto:[^\s)]+)\)/g,(m,txt,href)=>'<a href="'+href.replace(/"/g,'&quot;')+'" target="_blank" rel="noopener">'+txt+'</a>');
  s='<p>'+s.replace(/\n{2,}/g,'</p><p>').replace(/\n/g,'<br>')+'</p>';
  s=s.replace(/\x00(\d+)\x00/g,(m,i)=>{
    const b=blocks[Number(i)]||{code:'',lang:''};
    const lang=b.lang?(' data-lang="'+escAttr(b.lang)+'"'):'';
    return '<pre class="md-code"'+lang+'>'+b.code+'</pre>';
  });
  return s.replace(/<p>\s*<\/p>/g,'').replace(/<p>(<(?:h\d|ul|ol|table|pre|blockquote|hr))/g,'$1').replace(/(<\/(?:h\d|ul|ol|table|pre|blockquote)>)<\/p>/g,'$1');
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
