import { $, api, toast, renderMD, accordionize, openModal, closeModal, state, copyText, uiConfirm } from './core.js';

/* ---- project notes (auto-saved markdown notebook) ---- */
export const notesState={loaded:'',mode:'edit'};
let notesSaveTimer=null;
let notesSaving=false;
let notesOrganizedText='';
let notesOrganizeAbort=null;
let notesOrganizeSeq=0;

function setNotesStatus(kind){
  const s=$('#notesStatus');
  if(!s)return;
  s.dataset.state=kind;
  if(kind==='saving'){
    s.textContent='Saving…';s.style.opacity='1';s.style.color='var(--fg3)';
  }else if(kind==='saved'){
    s.textContent='✓ saved';s.style.opacity='1';s.style.color='var(--accent)';
    setTimeout(()=>{if(s.dataset.state==='saved')s.style.opacity='0.55';},1400);
  }else if(kind==='dirty'){
    s.textContent='…';s.style.opacity='0.75';s.style.color='var(--fg3)';
  }else{
    s.textContent='';s.style.opacity='0';
  }
}

export async function loadNotes(){
  try{
    const d=await api('/api/notes');
    notesState.loaded=d.notes||'';
    if(document.activeElement!==$('#notesEdit'))$('#notesEdit').value=notesState.loaded;
    setNotesStatus('');
    if(notesState.mode==='preview')showNotesPreview();
  }catch(e){}
}

export function scheduleNotesSave(){
  const v=$('#notesEdit').value;
  if(v===notesState.loaded){
    clearTimeout(notesSaveTimer);
    notesSaveTimer=null;
    return;
  }
  notesPreviewCache={src:'',html:''};
  setNotesStatus('dirty');
  clearTimeout(notesSaveTimer);
  notesSaveTimer=setTimeout(()=>{saveNotes();},800);
}

export async function flushNotesSave(){
  clearTimeout(notesSaveTimer);
  notesSaveTimer=null;
  await saveNotes();
}

export async function saveNotes(){
  const v=$('#notesEdit').value;
  if(v===notesState.loaded)return;
  if(notesSaving)return;
  notesSaving=true;
  setNotesStatus('saving');
  try{
    await api('/api/notes',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({notes:v})});
    notesState.loaded=v;
    setNotesStatus('saved');
  }catch(e){
    setNotesStatus('dirty');
    toast('notes: '+e.message);
  }finally{
    notesSaving=false;
  }
}

export function focusNotes(){
  const ta=$('#notesEdit');
  if(ta&&notesState.mode==='edit')ta.focus();
}

function setNotesAiStatus(msg){
  const s=$('#notesAiStatus');if(s)s.textContent=msg||'';
}

function abortNotesOrganize(){
  if(notesOrganizeAbort){try{notesOrganizeAbort.abort();}catch(e){}notesOrganizeAbort=null;}
  const stop=$('#notesAiStop');if(stop)stop.style.display='none';
}

function handleNotesSSE(chunk,onText,onErr){
  let ev='message',data='';
  chunk.split('\n').forEach(line=>{
    if(line.startsWith('event:'))ev=line.slice(6).trim();
    else if(line.startsWith('data:'))data+=line.slice(5).trim();
  });
  if(!data)return;
  if(ev==='error'){let m=data;try{m=JSON.parse(data);}catch(e){}onErr(m);return;}
  if(ev==='done')return;
  try{const t=JSON.parse(data);if(typeof t==='string')onText(t);}catch(e){}
}

let notesAiRenderTimer=null,notesAiPending='';
function scheduleNotesAiRender(seq,text){
  notesAiPending=text;
  clearTimeout(notesAiRenderTimer);
  notesAiRenderTimer=setTimeout(()=>{
    if(seq!==notesOrganizeSeq)return;
    const out=$('#notesAiOut');
    if(out){out.innerHTML=renderMD(notesAiPending);accordionize(out);}
  },90);
}

export async function organizeNotes(){
  if(state.aiDisabled){toast('AI features are disabled — enable in Settings → AI assist');return;}
  await flushNotesSave();
  const src=($('#notesEdit')||{}).value||'';
  if(!src.trim()){toast('write some notes first');return;}
  const seq=++notesOrganizeSeq;
  abortNotesOrganize();
  notesOrganizedText='';
  const apply=$('#notesAiApply');if(apply)apply.disabled=true;
  const before=$('#notesAiBefore');if(before)before.textContent=src;
  const out=$('#notesAiOut');if(out)out.innerHTML='<div class="hint">Organizing…</div>';
  setNotesAiStatus('Sending to your AI provider…');
  openModal($('#notesAiModal'));
  const ctrl=new AbortController();notesOrganizeAbort=ctrl;
  const stop=$('#notesAiStop');if(stop)stop.style.display='';
  let acc='';
  try{
    const r=await fetch('/api/ai/notes/organize/stream',{
      method:'POST',headers:{'content-type':'application/json'},
      body:JSON.stringify({notes:src}),signal:ctrl.signal,
    });
    if(!r.ok||!r.body)throw new Error('stream-unavailable');
    const reader=r.body.getReader(),dec=new TextDecoder();
    let buf='',streaming=false;
    for(;;){
      const {value,done}=await reader.read();
      if(done)break;
      if(seq!==notesOrganizeSeq)return;
      buf+=dec.decode(value,{stream:true});
      let idx;
      while((idx=buf.indexOf('\n\n'))>=0){
        const chunk=buf.slice(0,idx);buf=buf.slice(idx+2);
        handleNotesSSE(chunk,
          t=>{if(seq!==notesOrganizeSeq)return;if(!streaming){streaming=true;setNotesAiStatus('Streaming organized draft…');}
            acc+=t;scheduleNotesAiRender(seq,acc);},
          msg=>{throw new Error(msg);});
      }
    }
    if(seq!==notesOrganizeSeq)return;
    notesOrganizedText=acc.trim();
    if(out){out.innerHTML=renderMD(notesOrganizedText||'_(empty response)_');accordionize(out);}
    if(apply)apply.disabled=!notesOrganizedText;
    setNotesAiStatus(notesOrganizedText?'Ready — review and Apply':'No output');
  }catch(e){
    if(seq!==notesOrganizeSeq)return;
    if(ctrl.signal.aborted){setNotesAiStatus('stopped');}
    else if(e.message==='stream-unavailable'){await organizeNotesNonStream(seq,src);}
    else{
      setNotesAiStatus('');
      if(out)out.innerHTML='<div class="hint" style="color:var(--red)">Error: '+e.message+'</div>';
      toast('organize: '+e.message);
    }
  }finally{
    if(seq===notesOrganizeSeq){
      if(notesOrganizeAbort===ctrl)notesOrganizeAbort=null;
      if(stop)stop.style.display='none';
    }
  }
}

async function organizeNotesNonStream(seq,src){
  setNotesAiStatus('Thinking…');
  try{
    const r=await api('/api/ai/notes/organize',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({notes:src})});
    if(seq!==notesOrganizeSeq)return;
    notesOrganizedText=(r.text||'').trim();
    const out=$('#notesAiOut');
    if(out){out.innerHTML=renderMD(notesOrganizedText||'_(empty response)_');accordionize(out);}
    const apply=$('#notesAiApply');if(apply)apply.disabled=!notesOrganizedText;
    setNotesAiStatus(notesOrganizedText?'Ready — review and Apply':'No output');
  }catch(e){
    if(seq!==notesOrganizeSeq)return;
    const out=$('#notesAiOut');
    if(out)out.innerHTML='<div class="hint" style="color:var(--red)">Error: '+e.message+'</div>';
    setNotesAiStatus('');
    toast('organize: '+e.message);
  }
}

async function applyOrganizedNotes(){
  if(!notesOrganizedText){toast('nothing to apply');return;}
  if(!await uiConfirm('Replace your project notes with the organized draft?','Apply organized notes'))return;
  const ta=$('#notesEdit');
  if(!ta)return;
  ta.value=notesOrganizedText;
  notesState.mode='edit';
  $('#notesSeg')?.querySelectorAll('button').forEach(x=>{const on=x.dataset.m==='edit';x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  ta.style.display='block';
  const prev=$('#notesPreview');if(prev)prev.style.display='none';
  notesPreviewCache={src:'',html:''};
  await saveNotes();
  closeModal($('#notesAiModal'));
  abortNotesOrganize();
  focusNotes();
  toast('notes updated');
}

$('#notesEdit')&&$('#notesEdit').addEventListener('input',scheduleNotesSave);
$('#notesEdit')&&$('#notesEdit').addEventListener('blur',()=>{flushNotesSave();});
$('#notesEdit')&&$('#notesEdit').addEventListener('paste',e=>{
  const img=[...((e.clipboardData||{}).items||[])].find(it=>it.type&&it.type.indexOf('image/')===0);
  if(!img)return;e.preventDefault();
  const file=img.getAsFile();if(!file)return;
  const rd=new FileReader();
  rd.onload=async()=>{
    const ta=$('#notesEdit');
    try{
      const r=await api('/api/notes/images',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({mime:file.type||'image/png',data:rd.result})});
      const ins='\n![pasted image](/api/notes/images/'+r.id+')\n',p=ta.selectionStart;
      ta.value=ta.value.slice(0,p)+ins+ta.value.slice(ta.selectionEnd);
      ta.selectionStart=ta.selectionEnd=p+ins.length;
      scheduleNotesSave();
      toast('image embedded');
    }catch(err){toast('image: '+err.message);}
  };
  rd.readAsDataURL(file);
});
$('#notesSeg')&&$('#notesSeg').querySelectorAll('button').forEach(b=>b.onclick=async()=>{
  notesState.mode=b.dataset.m;$('#notesSeg').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  const edit=notesState.mode==='edit';
  if(!edit)await flushNotesSave();
  $('#notesEdit').style.display=edit?'block':'none';$('#notesPreview').style.display=edit?'none':'block';
  if(!edit)showNotesPreview();
});
$('#notesOrganizeBtn')&&($('#notesOrganizeBtn').onclick=()=>organizeNotes());
$('#notesAiClose')&&($('#notesAiClose').onclick=()=>{abortNotesOrganize();closeModal($('#notesAiModal'));});
$('#notesAiStop')&&($('#notesAiStop').onclick=abortNotesOrganize);
$('#notesAiApply')&&($('#notesAiApply').onclick=()=>applyOrganizedNotes());
$('#notesAiCopy')&&($('#notesAiCopy').onclick=()=>{if(notesOrganizedText)copyText(notesOrganizedText,'organized notes copied');else toast('nothing to copy');});

let notesPreviewCache={src:'',html:''};

export function showNotesPreview(){
  const src=$('#notesEdit').value;
  const box=$('#notesPreview');
  if(notesPreviewCache.src===src){box.innerHTML=notesPreviewCache.html;return;}
  box.innerHTML=renderMD(src);
  accordionize(box);
  notesPreviewCache={src,html:box.innerHTML};
}
