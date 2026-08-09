import { $, api, toast, renderMD, accordionize, createAutosave } from './core.js';

/* ---- project notes (auto-saved markdown notebook) ---- */
export const notesState={loaded:'',mode:'edit'};

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

const notesAutosave=createAutosave({
  delay:800,
  onStatus:setNotesStatus,
  save:async v=>{
    await api('/api/notes',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({notes:v})});
    notesState.loaded=v;
  },
});

export async function loadNotes(){
  try{
    const d=await api('/api/notes');
    notesState.loaded=d.notes||'';
    notesAutosave.setBaseline(notesState.loaded);
    if(document.activeElement!==$('#notesEdit'))$('#notesEdit').value=notesState.loaded;
    setNotesStatus('');
    if(notesState.mode==='preview')showNotesPreview();
  }catch(e){}
}

export function scheduleNotesSave(){
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
  notesAutosave.schedule($('#notesEdit').value);
  await flushNotesSave();
}

export function focusNotes(){
  const ta=$('#notesEdit');
  if(ta&&notesState.mode==='edit')ta.focus();
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
let notesPreviewCache={src:'',html:''};

export function showNotesPreview(){
  const src=$('#notesEdit').value;
  const box=$('#notesPreview');
  if(notesPreviewCache.src===src){box.innerHTML=notesPreviewCache.html;return;}
  box.innerHTML=renderMD(src);
  accordionize(box);
  notesPreviewCache={src,html:box.innerHTML};
}
