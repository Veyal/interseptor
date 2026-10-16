package control

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIListboxKeyboardModel executes js/listbox.js against a small DOM shim:
// one roving Tab stop that follows the selected option, Arrow/Home/End wrap,
// activation fires exactly once per Enter/Space (never twice through
// wireRowKey), arrows select only when selectionFollowsFocus is on, nested
// controls keep their own arrow/Home/End handling, and setListboxSelection
// never steals focus.
func TestUIListboxKeyboardModel(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	listbox := readUIAsset(t, "js/listbox.js")
	listbox = strings.Replace(listbox, "import { wireRowKey } from './core.js';", "", 1)
	if strings.Contains(listbox, "from './core.js'") {
		t.Fatal("listbox.js import shape changed; update the test's import strip")
	}
	script := `
let active=null;
class El{
  constructor(tag){this.tag=tag;this.attrs={};this.listeners={};this.parent=null;this.children=[];this.onclick=null;this.clicks=0;}
  get tabIndex(){return this.hasAttribute('tabindex')?Number(this.attrs.tabindex):-1;}
  set tabIndex(v){this.attrs.tabindex=String(v);}
  setAttribute(k,v){this.attrs[k]=String(v);}
  getAttribute(k){return k in this.attrs?this.attrs[k]:null;}
  hasAttribute(k){return k in this.attrs;}
  removeAttribute(k){delete this.attrs[k];}
  appendChild(c){c.parent=this;this.children.push(c);return c;}
  addEventListener(type,fn){(this.listeners[type]||(this.listeners[type]=[])).push(fn);}
  focus(){active=this;}
  click(){this.clicks++;if(this.onclick)this.onclick({type:'click',target:this});}
  closest(sel){
    const parts=sel.split(',').map(s=>s.trim());
    for(let n=this;n;n=n.parent){
      if(parts.includes(n.tag))return n;
      if(parts.includes('[role="button"]')&&n.getAttribute('role')==='button')return n;
    }
    return null;
  }
  dispatch(type,init={}){
    const e={type,key:init.key,target:init.target||this,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;}};
    for(let n=e.target;n;n=n.parent)(n.listeners[type]||[]).forEach(fn=>fn.call(n,e));
    return e;
  }
}
globalThis.document={get activeElement(){return active;}};
const fail=m=>{throw Error(m);};
const eq=(got,want,msg)=>{if(got!==want)fail(msg+': got '+JSON.stringify(got)+' want '+JSON.stringify(want));};
const stops=els=>els.filter(el=>el.tabIndex===0).length;
const build=(n)=>{const box=new El('div');const rows=[];for(let i=0;i<n;i++){const r=new El('div');r.setAttribute('data-i',String(i));rows.push(box.appendChild(r));}return {box,rows};};

// --- roles, aria-selected, one Tab stop on the selected option
{
  const {box,rows}=build(3);
  const els=wireListbox(box,rows,{label:'Things',selected:el=>el.getAttribute('data-i')==='1'});
  eq(box.getAttribute('role'),'listbox','container role');
  eq(box.getAttribute('aria-label'),'Things','container label');
  els.forEach(el=>eq(el.getAttribute('role'),'option','option role'));
  eq(els.map(el=>el.getAttribute('aria-selected')).join(),'false,true,false','aria-selected mirrors selected()');
  eq(stops(els),1,'exactly one Tab stop');
  eq(els[1].tabIndex,0,'the selected option owns the Tab stop');
}
// --- Arrow/Home/End move focus and the Tab stop, wrapping; no activation without selectionFollowsFocus
{
  const {box,rows}=build(3);
  let activated=[];
  const els=wireListbox(box,rows,{activate:el=>activated.push(el.getAttribute('data-i'))});
  eq(els[0].tabIndex,0,'first option is the Tab stop when nothing is selected');
  els[0].focus();
  els[0].dispatch('keydown',{key:'ArrowDown'});
  eq(active,els[1],'ArrowDown focuses the next option');
  eq(stops(els),1,'still one Tab stop after ArrowDown');eq(els[1].tabIndex,0,'Tab stop followed focus');
  els[1].dispatch('keydown',{key:'ArrowDown'});els[2].dispatch('keydown',{key:'ArrowDown'});
  eq(active,els[0],'ArrowDown wraps from the last option to the first');
  els[0].dispatch('keydown',{key:'ArrowUp'});
  eq(active,els[2],'ArrowUp wraps from the first option to the last');
  els[2].dispatch('keydown',{key:'Home'});eq(active,els[0],'Home focuses the first option');
  els[0].dispatch('keydown',{key:'End'});eq(active,els[2],'End focuses the last option');
  eq(activated.length,0,'arrows must not activate when selectionFollowsFocus is off');
  const enter=els[2].dispatch('keydown',{key:'Enter'});
  eq(activated.join(),'2','Enter activates the focused option');
  eq(enter.defaultPrevented,true,'Enter is consumed');
  els[2].dispatch('keydown',{key:' '});
  eq(activated.join(),'2,2','Space activates exactly once more');
  eq(els[2].clicks,0,'activation must not also route through a synthetic click');
}
// --- selectionFollowsFocus: an arrow move activates the new option exactly once
{
  const {box,rows}=build(3);
  let activated=[];
  const els=wireListbox(box,rows,{activate:el=>activated.push(el.getAttribute('data-i')),selectionFollowsFocus:true});
  els[0].focus();
  els[0].dispatch('keydown',{key:'ArrowDown'});
  eq(activated.join(),'1','ArrowDown selects the next option once');
  els[1].dispatch('keydown',{key:'Home'});
  eq(activated.join(),'1,0','Home selects the first option once');
  els[0].dispatch('keydown',{key:'Home'});
  eq(activated.join(),'1,0','Home on the first option is a no-op');
}
// --- a real control inside an option keeps its own keys
{
  const {box,rows}=build(2);
  const button=rows[0].appendChild(new El('button'));
  const els=wireListbox(box,rows,{activate:()=>{}});
  els[0].focus();button.focus();
  const end=button.dispatch('keydown',{key:'End',target:button});
  eq(end.defaultPrevented,false,'End inside a nested button is not hijacked');
  eq(active,button,'focus stays on the nested control');
  const enter=button.dispatch('keydown',{key:'Enter',target:button});
  eq(enter.defaultPrevented,false,'Enter on a nested button is left to the button');
}
// --- setListboxSelection re-points aria-selected; moves the Tab stop only when focus is elsewhere
{
  const {box,rows}=build(3);
  const els=wireListbox(box,rows,{});
  active=null;
  setListboxSelection(els,el=>el.getAttribute('data-i')==='2');
  eq(els.map(el=>el.getAttribute('aria-selected')).join(),'false,false,true','aria-selected re-pointed');
  eq(els[2].tabIndex,0,'Tab stop follows the new selection when focus is outside');
  eq(stops(els),1,'one Tab stop');
  els[0].focus();
  setListboxSelection(els,el=>el.getAttribute('data-i')==='1');
  eq(active,els[0],'selection change never steals focus');
  eq(els[2].tabIndex,0,'Tab stop is left alone while focus is inside the list');
}
`
	src := repeaterRenderJS(t, core, "export function wireRowKey(el, onActivate)") + "\n" + listbox + "\n" + script
	if out, err := exec.Command("node", "--input-type=module", "-e", src).CombinedOutput(); err != nil {
		t.Fatalf("listbox keyboard model: %v\n%s", err, out)
	}
}
