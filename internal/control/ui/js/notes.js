import { $, requireFindingsEditing, registerProjectSwitchGuard, api, toast, renderMD, accordionize, createAutosave, renderLoadError, openFlow, icon } from './core.js';
import { parseNoteRefs, collectRefIds, resolveRefs, chipLabel, promoteRequest } from './notes-model.js';
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
  // Shared load-failure component (alert message + Retry) rather than a local
  // re-implementation with its own HTML escaper.
  renderLoadError(el,'Notes',err,loadNotes,false);
}

let notesSaveError='';
function setNotesStatus(kind){
  const s=$('#notesStatus');
  if(!s)return;
  if(kind==='saving'||kind==='saved'){notesSaveError='';delete s.dataset.tooltip;}
  if(kind==='dirty'&&notesSaveError)kind='error';
  s.dataset.state=kind;
  const retry=$('#notesSaveRetry');if(retry)retry.hidden=kind!=='error';
  // Colour and emphasis come from [data-state] in panel-misc.css, not inline styles.
  if(kind==='saving')s.textContent='Saving…';
  else if(kind==='saved')s.textContent='Saved';
  else if(kind==='dirty')s.textContent='Unsaved changes';
  else if(kind==='error'){s.textContent='Save failed'+(notesSaveError?': '+notesSaveError:'');s.dataset.tooltip=notesSaveError;}
  else s.textContent='';
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
    if(notesState.mode==='preview'||notesPanelWide())showNotesPreview();
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
  if(notesPreviewCache.src===src){box.innerHTML=notesPreviewCache.html;void decorateNoteRefs(box);return;}
  box.innerHTML=renderMD(src);
  accordionize(box);
  notesPreviewCache={src,html:box.innerHTML};
  void decorateNoteRefs(box);
}

/* ---- structured references: #F-2 and flow:123 become chips ----
   A reference becomes a chip only when the finding or flow exists; anything else
   stays plain text. The post-pass is local to the preview and never edits the
   saved note text. */
const REF_TTL_MS=30000;
const refCache={findings:null,findingsAt:0,flows:new Map()};
async function knownRefs(ids){
  const now=Date.now();
  const findingIds=new Set(),flowIds=new Set();
  if(ids.findings.size){
    if(!refCache.findings||now-refCache.findingsAt>REF_TTL_MS){
      try{const d=await api('/api/findings');refCache.findings=new Set((d.findings||[]).map(f=>Number(f.id)));refCache.findingsAt=Date.now();}
      catch(e){refCache.findings=null;}
    }
    ids.findings.forEach(id=>{if(refCache.findings&&refCache.findings.has(id))findingIds.add(id);});
  }
  await Promise.all([...ids.flows].map(async id=>{
    const hit=refCache.flows.get(id);
    if(!hit||now-hit.at>REF_TTL_MS){
      let ok=false;
      try{await api('/api/flows/'+id);ok=true;}catch(e){ok=false;}
      refCache.flows.set(id,{ok,at:Date.now()});
    }
    if(refCache.flows.get(id).ok)flowIds.add(id);
  }));
  return {findingIds,flowIds};
}
function noteChip(t){
  const b=document.createElement('button');
  b.type='button';b.className='note-chip';b.dataset.kind=t.type;b.dataset.id=String(t.id);
  b.setAttribute('aria-label','Open '+chipLabel(t));
  b.insertAdjacentHTML('beforeend',icon(t.type==='finding'?'evidence':'link'));
  b.append(document.createTextNode(t.text));
  return b;
}
function refTextNodes(box){
  const walker=document.createTreeWalker(box,NodeFilter.SHOW_TEXT,{acceptNode:n=>n.parentElement&&n.parentElement.closest('code,pre,a,button')?NodeFilter.FILTER_REJECT:NodeFilter.FILTER_ACCEPT});
  const nodes=[];
  while(walker.nextNode())nodes.push(walker.currentNode);
  return nodes;
}
export async function decorateNoteRefs(box){
  if(!box)return;
  const ids=collectRefIds(box.textContent||'');
  if(!ids.findings.size&&!ids.flows.size)return;
  const known=await knownRefs(ids);
  if(!box.isConnected)return;
  refTextNodes(box).forEach(node=>{
    const tokens=resolveRefs(parseNoteRefs(node.nodeValue),known);
    if(!tokens.some(t=>t.type!=='text'))return;
    const frag=document.createDocumentFragment();
    tokens.forEach(t=>frag.append(t.type==='text'?document.createTextNode(t.text):noteChip(t)));
    node.replaceWith(frag);
  });
}
$('#notesPreview')&&$('#notesPreview').addEventListener('click',e=>{
  const chip=e.target.closest&&e.target.closest('.note-chip');
  if(!chip)return;
  const id=Number(chip.dataset.id);
  if(chip.dataset.kind==='finding'){location.hash='#finding-'+id+'/overview';return;}
  if(!openFlow(id,{source:'notes'}))toast('The flow view is not available yet');
});

/* ---- promote a note selection (or the current line) to a finding draft ---- */
function noteSelectionText(){
  const ta=$('#notesEdit');if(!ta)return '';
  if(ta.selectionEnd>ta.selectionStart)return ta.value.slice(ta.selectionStart,ta.selectionEnd);
  const at=ta.selectionStart||0;
  const from=ta.value.lastIndexOf('\n',at-1)+1;
  const to=ta.value.indexOf('\n',at);
  return ta.value.slice(from,to<0?ta.value.length:to);
}
export async function promoteNoteSelection(){
  if(!requireFindingsEditing())return;
  const req=promoteRequest(noteSelectionText());
  if(!req){toast('Select some text in the note to promote');return;}
  const btn=$('#notesPromote');
  if(btn){btn.disabled=true;btn.setAttribute('aria-busy','true');}
  try{
    const f=await api(req.path,{method:req.method,headers:{'content-type':'application/json'},body:JSON.stringify(req.body)});
    toast('Draft finding F-'+(f&&f.id!=null?f.id:'')+' created from the note');
    import('./project-state.js').then(m=>m.projectState.refresh({reason:'note-promote'})).catch(()=>{});
  }catch(e){toast('promote failed: '+e.message,'error');}
  finally{if(btn){btn.disabled=false;btn.removeAttribute('aria-busy');}}
}
$('#notesPromote')&&($('#notesPromote').onclick=promoteNoteSelection);

/* ---- wide layout: editor and live preview side by side ---- */
const notesWide=typeof matchMedia==='function'?matchMedia('(min-width: 1101px)'):null;
let notesWideTimer=null;
function notesPanelWide(){return !!(notesWide&&notesWide.matches);}
function syncNotesWide(){
  const on=!!(notesWide&&notesWide.matches);
  $('#panel-notes')?.classList.toggle('notes-wide',on);
  if(on)showNotesPreview();
}
if(notesWide){
  notesWide.addEventListener?.('change',syncNotesWide);
  $('#notesEdit')&&$('#notesEdit').addEventListener('input',()=>{
    if(!notesWide.matches)return;
    clearTimeout(notesWideTimer);
    notesWideTimer=setTimeout(showNotesPreview,300);
  });
  syncNotesWide();
}
