const input=document.querySelector('#search');
const results=document.querySelector('#search-results');
const status=document.querySelector('#search-status');
const sidebar=document.querySelector('.sidebar');
const base=new URL(document.body.dataset.baseurl||'/',document.baseURI).href;
let index=[];
let active=-1;

if(sidebar&&window.matchMedia('(max-width:800px)').matches)sidebar.removeAttribute('open');

function announce(message){if(status)status.textContent=message}
function closeResults(){
  active=-1;
  results?.replaceChildren();
  if(results)results.hidden=true;
  input?.setAttribute('aria-expanded','false');
  input?.removeAttribute('aria-activedescendant');
}
function setActive(next){
  const links=[...(results?.querySelectorAll('a')||[])];
  if(!links.length)return;
  active=(next+links.length)%links.length;
  links.forEach((link,i)=>link.classList.toggle('active',i===active));
  input?.setAttribute('aria-activedescendant',links[active].id);
  links[active].scrollIntoView({block:'nearest'});
}
async function load(){
  try{
    const response=await fetch(new URL('website/data/search.json',base));
    if(!response.ok)throw new Error(`HTTP ${response.status}`);
    index=await response.json();
  }catch(_){index=[];announce('Documentation search failed to load.')}
}
function excerpt(text,query){
  const clean=String(text||'').trim();
  if(!clean)return'';
  const at=clean.toLowerCase().indexOf(query);
  const start=Math.max(0,at<0?0:at-52);
  return (start?'…':'')+clean.slice(start,start+150)+(start+150<clean.length?'…':'');
}
function render(items,query){
  if(!results)return;
  results.replaceChildren();active=-1;
  if(!query){closeResults();return}
  const visible=items.slice(0,10);
  if(!visible.length){
    const empty=document.createElement('p');empty.className='search-empty';empty.textContent='No matching documentation.';
    results.append(empty);results.hidden=false;input?.setAttribute('aria-expanded','true');announce('No results found.');return;
  }
  visible.forEach((item,i)=>{
    const link=document.createElement('a');link.id=`search-result-${i}`;link.href=new URL(item.url.replace(/^\//,''),base).href;
    const title=document.createElement('strong');title.textContent=item.title;
    const text=document.createElement('span');text.textContent=excerpt(item.text,query);
    link.append(title,text);results.append(link);
  });
  results.hidden=false;input?.setAttribute('aria-expanded','true');announce(`${items.length} result${items.length===1?'':'s'} found.`);
}
function search(){
  const query=input?.value.trim().toLowerCase()||'';
  const terms=query.split(/\s+/).filter(Boolean);
  const matches=terms.length?index.filter(item=>{const haystack=(item.title+' '+item.text).toLowerCase();return terms.every(term=>haystack.includes(term))}):[];
  render(matches,query);
}
input?.addEventListener('input',search);
input?.addEventListener('keydown',event=>{
  if(event.key==='ArrowDown'){event.preventDefault();setActive(active+1)}
  else if(event.key==='ArrowUp'){event.preventDefault();setActive(active-1)}
  else if(event.key==='Enter'&&active>=0){event.preventDefault();results?.querySelectorAll('a')[active]?.click()}
  else if(event.key==='Escape'){closeResults();input.blur()}
});
document.addEventListener('click',event=>{if(!event.target.closest('.search'))closeResults()});
load();
