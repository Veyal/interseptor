import { $, api, toast, renderMD, accordionize } from './core.js';

/* ---- project notes ---- */
export const notesState={loaded:'',mode:'edit'};
export async function loadNotes(){
  try{const d=await api('/api/notes');notesState.loaded=d.notes||'';
    if(document.activeElement!==$('#notesEdit'))$('#notesEdit').value=notesState.loaded;
    if(notesState.mode==='preview')showNotesPreview();
  }catch(e){}
}
export async function saveNotes(){
  const v=$('#notesEdit').value;
  if(v===notesState.loaded)return; // nothing changed
  try{await api('/api/notes',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({notes:v})});
    notesState.loaded=v;const s=$('#notesSaved');s.style.opacity='1';setTimeout(()=>s.style.opacity='0',1200);
  }catch(e){toast('notes: '+e.message);}
}
$('#notesSave')&&($('#notesSave').onclick=saveNotes);
$('#notesEdit')&&$('#notesEdit').addEventListener('blur',saveNotes);
$('#notesEdit')&&$('#notesEdit').addEventListener('paste',e=>{
  const img=[...((e.clipboardData||{}).items||[])].find(it=>it.type&&it.type.indexOf('image/')===0);
  if(!img)return;e.preventDefault();
  const file=img.getAsFile();if(!file)return;
  const rd=new FileReader();
  rd.onload=()=>{const ta=$('#notesEdit'),ins='\n![pasted image]('+rd.result+')\n',p=ta.selectionStart;
    ta.value=ta.value.slice(0,p)+ins+ta.value.slice(ta.selectionEnd);ta.selectionStart=ta.selectionEnd=p+ins.length;
    saveNotes();toast('image embedded');};
  rd.readAsDataURL(file);
});
$('#notesSeg')&&$('#notesSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  notesState.mode=b.dataset.m;$('#notesSeg').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));
  const edit=notesState.mode==='edit';
  $('#notesEdit').style.display=edit?'block':'none';$('#notesPreview').style.display=edit?'none':'block';
  if(!edit)showNotesPreview();
});
export function showNotesPreview(){const box=$('#notesPreview');box.innerHTML=renderMD($('#notesEdit').value);accordionize(box);}
