// Optional hints share one app-rendered surface. Native title text is consumed
// before it can open an OS tooltip; labels and existing descriptions are kept.
export function initUiHints(root=document){
  const tip=document.createElement('div');
  tip.id='uiTooltip';tip.className='ui-tooltip';tip.hidden=true;
  tip.setAttribute('role','tooltip');document.body.appendChild(tip);
  let owner=null,timer=null,pointer=null;
  function adopt(el){
    if(!el.hasAttribute('title'))return;
    const text=el.getAttribute('title')||'';
    el.dataset.tooltip=text;el.removeAttribute('title');
    if(text&&!el.getAttribute('aria-label')&&!el.getAttribute('aria-labelledby')&&el.matches('button,a')&&!el.textContent.trim())el.setAttribute('aria-label',text);
    if(owner===el){if(text)tip.textContent=text;else hide();}
  }
  function scan(node){
    if(node.nodeType===1)adopt(node);
    node.querySelectorAll?.('[title]').forEach(adopt);
  }
  function hide(){
    clearTimeout(timer);
    if(owner){
      const ids=(owner.getAttribute('aria-describedby')||'').split(/\s+/).filter(id=>id&&id!==tip.id);
      if(ids.length)owner.setAttribute('aria-describedby',ids.join(' '));else owner.removeAttribute('aria-describedby');
    }
    owner=null;tip.hidden=true;
  }
  function show(el){
    if(!el?.dataset.tooltip?.trim()||el.getAttribute('aria-expanded')==='true')return;
    clearTimeout(timer);if(owner===el)return;hide();owner=el;
    tip.textContent=el.dataset.tooltip;tip.hidden=false;
    const ids=new Set((el.getAttribute('aria-describedby')||'').split(/\s+/).filter(Boolean));
    ids.add(tip.id);el.setAttribute('aria-describedby',[...ids].join(' '));
    const r=el.getBoundingClientRect(),t=tip.getBoundingClientRect();
    tip.style.left=Math.max(8,Math.min(r.left,innerWidth-t.width-8))+'px';
    tip.style.top=Math.max(8,r.bottom+t.height+8<=innerHeight?r.bottom+6:r.top-t.height-6)+'px';
  }
  function pointerInHint(){
    if(!owner||tip.hidden||!pointer)return false;
    const r=tip.getBoundingClientRect();
    return pointer.x>=r.left&&pointer.x<=r.right&&pointer.y>=r.top&&pointer.y<=r.bottom;
  }
  function leave(){
    clearTimeout(timer);timer=setTimeout(()=>{
      if(owner&&(owner.matches(':hover')||owner.contains(document.activeElement)||pointerInHint()))return;
      hide();
    },120);
  }
  scan(root);
  new MutationObserver(mutations=>{
    for(const m of mutations){
      if(m.type==='attributes')adopt(m.target);
      else m.addedNodes.forEach(scan);
    }
    if(owner&&(!owner.isConnected||owner.getAttribute('aria-expanded')==='true'))hide();
  }).observe(root.documentElement||root,{childList:true,subtree:true,attributes:true,attributeFilter:['title','aria-expanded']});
  document.addEventListener('pointerover',e=>{
    if(e.pointerType==='touch')return;
    pointer={x:e.clientX,y:e.clientY};
    if(!pointerInHint())show(e.target.closest?.('[data-tooltip]'));
  });
  document.addEventListener('pointermove',e=>{
    if(e.pointerType==='touch')return;
    pointer={x:e.clientX,y:e.clientY};
    if(!owner)return;
    if(pointerInHint())clearTimeout(timer);else leave();
  });
  document.addEventListener('pointerleave',()=>{pointer=null;leave();});
  document.addEventListener('focusin',e=>show(e.target.closest?.('[data-tooltip]')));
  document.addEventListener('pointerout',leave);
  document.addEventListener('focusout',leave);
  document.addEventListener('keydown',e=>{if(e.key==='Escape')hide();});
  document.addEventListener('pointerdown',hide);
  window.addEventListener('resize',hide);
  window.addEventListener('scroll',e=>{if(!tip.contains(e.target))hide();},true);
}
