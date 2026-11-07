// decoder.js — the Decoder modal (base64/url/hex/JWT, one layer at a time).
// It is a plain tool: it never touches a captured flow, so it stays independent
// of the panels that open it.
import { $, esc, toast, api, openModal, closeModal, pickTextFile, normalizeListText, DEC_OPS, copyText } from './core.js';

export { DEC_OPS };
let decRequestEpoch=0;
let decModalEpoch=0;
function decSetPending(on,op){
  const ops=$('#decOps'),err=$('#decErr');
  if(ops)ops.setAttribute('aria-busy',on?'true':'false');
  if(!err)return;
  if(on){err.style.color='var(--fg3)';err.textContent=(op||'Operation')+'…';}
  else if(err.textContent.endsWith('…')){err.textContent='';}
}
function decInvalidatePending(){decRequestEpoch++;decSetPending(false);}
function decCurrent(epoch,modalEpoch,input){
  const modal=$('#decModal'),field=$('#decIn');
  return epoch===decRequestEpoch&&modalEpoch===decModalEpoch&&modal?.style.display==='flex'&&field?.value===input;
}
export function decBuildOps(){const box=$('#decOps');if(!box||box._built)return;box._built=1;
  box.innerHTML=DEC_OPS.map(([op,label])=>`<button class="btn" data-op="${op}">${esc(label)}</button>`).join('');
  box.querySelectorAll('[data-op]').forEach(b=>b.onclick=()=>decApply(b.dataset.op));}
export async function decApply(op){
  const epoch=++decRequestEpoch;
  const modalEpoch=decModalEpoch;
  const input=$('#decIn').value;
  const err=$('#decErr');
  decSetPending(true,op);
  try{const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input})});
    if(!decCurrent(epoch,modalEpoch,input))return;
    if(r.error){err.style.color='var(--red)';err.textContent=r.error;decSetPending(false);return;}
    $('#decOut').value=r.output;decSetPending(false);}
  catch(e){if(!decCurrent(epoch,modalEpoch,input))return;err.style.color='var(--red)';err.textContent=e.message;decSetPending(false);}
}
export function openDecoder(seed){decBuildOps();decModalEpoch++;decInvalidatePending();openModal($('#decModal'));if(seed)$('#decIn').value=seed;$('#decOut').value='';$('#decErr').textContent='';setTimeout(()=>$('#decIn').focus(),0);}
async function decLoadFile(){
  decInvalidatePending();
  try{
    const got=await pickTextFile();
    if(!got) return;
    // The native picker yields to the event loop; invalidate again in case a
    // decoder request was started while it was open.
    decInvalidatePending();
    $('#decIn').value=normalizeListText(got.text);
    $('#decOut').value='';$('#decErr').textContent='';
    toast('loaded from '+got.name);
  }catch(e){toast(e.message);}
}
if($('#decLoad'))$('#decLoad').onclick=decLoadFile;
if($('#decClose'))$('#decClose').onclick=()=>{decModalEpoch++;decInvalidatePending();closeModal($('#decModal'));};
if($('#decIn'))$('#decIn').addEventListener('input',()=>decInvalidatePending());
if($('#decUp'))$('#decUp').onclick=()=>{decInvalidatePending();$('#decIn').value=$('#decOut').value;$('#decOut').value='';$('#decIn').focus();};
if($('#decCopy'))$('#decCopy').onclick=()=>copyText($('#decOut').value,'output copied');
