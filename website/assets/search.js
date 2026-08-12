const input=document.querySelector('#search');
const results=document.querySelector('.search-results');
const status=document.querySelector('#search-status');
const base=document.querySelector('base')?.href||new URL(document.body.dataset.baseurl||'/',document.baseURI).href;
let index=[];
function announce(message){if(status)status.textContent=message}
async function load(){try{const r=await fetch(new URL('data/search.json',base));if(!r.ok)throw new Error(`HTTP ${r.status}`);index=await r.json()}catch(_){index=[];announce('Documentation search failed to load.')}}
function render(items,queried){if(!results)return;results.replaceChildren();results.style.cssText='';if(!queried)return;const visible=items.slice(0,8);if(!visible.length){announce('No results found.');return}results.style.cssText='position:absolute;right:1rem;top:66px;width:min(420px,calc(100vw - 2rem));background:#fff;border:1px solid #d9d9d1;padding:.5rem;z-index:8';visible.forEach(item=>{const a=document.createElement('a');a.href=new URL(item.url.replace(/^\//,''),base).href;a.textContent=item.title;a.style.cssText='display:block;padding:.65rem;color:#10151d';results.append(a)});announce(`${items.length} result${items.length===1?'':'s'} found.`)}
input?.addEventListener('input',()=>{const q=input.value.trim().toLowerCase();render(q?index.filter(x=>(x.title+' '+x.text).toLowerCase().includes(q)):[],Boolean(q))});document.addEventListener('click',e=>{if(!e.target.closest('.search'))render([],false)});load();
