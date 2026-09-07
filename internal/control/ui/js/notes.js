import { $, registerProjectSwitchGuard, api, toast, renderMD, accordionize, createAutosave } from './core.js';
registerProjectSwitchGuard(()=>notesAutosave.isDirty()?'Save or retry Notes before switching projects.':'');

/* ---- project notes (auto-saved markdown notebook) ---- */
export const notesState={loaded:'',mode:'edit'};
let notesEditGeneration=0;
let notesLoadGeneration=0;

function notesLoadState() {
  let el=$('#notesLoadState');
  if(el)return el;
  const panel=$('#panel-notes'), edit=$('#notesEdit');
  if(!panel||!edit)return null;
  el=document.createElement('div');
  el.id='notesLoadState';
  el.className='tls-diag-banner';
  el.setAttribute('role','status');
  el.setAttribute('aria-live','polite');
  panel.insertBefore(el,edit);
  return el;
}

function showNotesLoadError(err) {
  const el=notesLoadState();
  if(!el)return;
  el.innerHTML='<span class="state-error-msg">Couldn\'t load notes: '+
    String(err?.message||'request failed').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))+
    '</span> <button type="button" class="btn xs" data-notes-retry>Retry</button>';
  el.querySelector('[data-notes-retry]')?.addEventListener('click',loadNotes);
}

let notesSaveError='';
function setNotesStatus(kind){
  const s=$('#notesStatus');
  if(!s)return;
  if(kind==='saving'||kind==='saved'){notesSaveError='';delete s.dataset.tooltip;}
  if(kind==='dirty'&&notesSaveError)kind='error';
  s.dataset.state=kind;
  const retry=$('#notesSaveRetry');if(retry)retry.hidden=kind!=='error';
  if(kind==='saving'){
    s.textContent='Saving…';s.style.opacity='1';s.style.color='var(--fg3)';
  }else if(kind==='saved'){
    s.textContent='Saved';s.style.opacity='1';s.style.color='var(--fg3)';
  }else if(kind==='dirty'){
    s.textContent='Unsaved changes';s.style.opacity='1';s.style.color='var(--fg3)';
  }else if(kind==='error'){
    s.textContent='Save failed'+(notesSaveError?': '+notesSaveError:'');s.style.opacity='1';s.style.color='var(--red)';
    s.dataset.tooltip=notesSaveError;
  }else{
    s.textContent='';s.style.opacity='0';
  }
}

const notesAutosave=createAutosave({
  delay:800,
  onStatus:setNotesStatus,
  save:async v=>{
    try{await api('/api/notes',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({notes:v})});}
    catch(e){notesSaveError=e.message||'Could not save notes';throw e;}
    notesState.loaded=v;
  },
});

export async function loadNotes(){
  const loadGeneration=++notesLoadGeneration;
  const loadState=notesLoadState();
  if(loadState){loadState.textContent='Loading notes…';loadState.style.display='block';}
  const ta=$('#notesEdit');
  const current=ta?.value||'';
  const generation=notesEditGeneration;
  const dirty=notesAutosave.isDirty()||current!==notesState.loaded;
  try{
    const d=await api('/api/notes');
    if(loadGeneration!==notesLoadGeneration)return;
    const loaded=d.notes||'';
    const preserveLocal=dirty||notesEditGeneration!==generation||(ta&&ta.value!==current);
    notesState.loaded=loaded;
    notesAutosave.setBaseline(loaded,{preserveCurrent:preserveLocal});
    if(preserveLocal){if(ta)notesAutosave.schedule(ta.value);}
    else{if(ta)ta.value=loaded;setNotesStatus('');}
    if(loadState){loadState.textContent='';loadState.style.display='none';}
    if(notesState.mode==='preview')showNotesPreview();
  }catch(e){
    if(loadGeneration!==notesLoadGeneration)return;
    // Keep the current notebook visible; an error must never look like an empty
    // notebook or overwrite unsaved notes. The Retry action is intentionally local.
    showNotesLoadError(e);
  }
}

export function scheduleNotesSave(){
  notesEditGeneration++;
  const v=$('#notesEdit').value;
  if(v!==notesState.loaded)notesPreviewCache={src:'',html:''};
  notesAutosave.schedule(v);
}

export async function flushNotesSave(){
  try{await notesAutosave.flush();}
  catch(e){toast('notes: '+e.message);}
}

// saveNotes reads the textarea directly (rather than relying on whatever value
// was last passed to scheduleNotesSave) so callers that set ta.value programmatically
// and then immediately save (applyOrganizedNotes) don't flush a stale value.
export async function saveNotes(){
  notesEditGeneration++;
  notesAutosave.schedule($('#notesEdit').value);
  await flushNotesSave();
}

export function focusNotes(){
  const ta=$('#notesEdit');
  if(ta&&notesState.mode==='edit')ta.focus();
}

$('#notesEdit')&&$('#notesEdit').addEventListener('input',scheduleNotesSave);
$('#notesSaveRetry')&&($('#notesSaveRetry').onclick=()=>flushNotesSave());
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
$('#notesSeg')&&$('#notesSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  notesState.mode=b.dataset.m;$('#notesSeg').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  const edit=notesState.mode==='edit';
  // Rendering uses the local draft; a slow save must not own the selected view.
  if(!edit)void flushNotesSave();
  $('#notesEdit').style.display=edit?'block':'none';$('#notesPreview').style.display=edit?'none':'block';
  if(!edit)showNotesPreview();
});
let notesPreviewCache={src:'',html:''};

export function showNotesPreview(){
  const src=$('#notesEdit').value;
  const box=$('#notesPreview');
  if(notesPreviewCache.src===src){box.innerHTML=notesPreviewCache.html;return;}
  box.innerHTML=renderMD(src);
  accordionize(box);
  notesPreviewCache={src,html:box.innerHTML};
}
