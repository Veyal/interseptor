#!/usr/bin/env python3
"""Read-only phone geometry check for an explicit disposable candidate URL."""
import argparse,hashlib,json
from pathlib import Path
from playwright.sync_api import sync_playwright
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def main(a):
 r={'base':a.base,'probe_sha256':sha(__file__),'engines':{},'failures':[]}
 with sync_playwright() as pw:
  for name in ('chromium','firefox','webkit'):
   b=getattr(pw,name).launch();p=b.new_page(viewport={'width':390,'height':844});p.set_default_timeout(8000)
   try:
    p.goto(a.base,wait_until='domcontentloaded');p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
    if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
    p.locator('#mobileToolSelectUi').click();p.locator('[role="option"]',has_text='Intruder').last.click();p.locator('#intrTemplate').wait_for()
    m=p.evaluate("""()=>{const t=document.querySelector('#intrTemplate'),res=document.querySelector('#intrResults');let sc=null;for(let n=res.parentElement;n;n=n.parentElement){if(/auto|scroll/.test(getComputedStyle(n).overflowY)){sc=n;break}};const h=t.getBoundingClientRect().height,initial=res.getBoundingClientRect();if(sc)sc.scrollTop=sc.scrollHeight;const final=res.getBoundingClientRect();return {template_height:h,results_initial:{top:initial.top,bottom:initial.bottom},results_after_scroll:{top:final.top,bottom:final.bottom,visible:final.bottom>0&&final.top<innerHeight},scroll_container:{client_height:sc?.clientHeight,scroll_height:sc?.scrollHeight,scroll_top:sc?.scrollTop}}}""")
    if m['template_height']<180:raise AssertionError('template below 180px')
    if not m['results_after_scroll']['visible']:raise AssertionError('results unreachable by internal scroll')
    r['engines'][name]=m
   except Exception as e:r['failures'].append(f'{name}: {type(e).__name__}: {e}')
   finally:b.close()
 Path(a.out).mkdir(parents=True,exist_ok=True);Path(a.out,'report.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps({'failures':r['failures'],'engines':r['engines']}));return 1 if r['failures'] else 0
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--base',required=True);p.add_argument('--out',required=True);a=p.parse_args();raise SystemExit(main(a))
