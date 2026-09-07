#!/usr/bin/env python3
"""Settled Findings visual evidence for an explicitly supplied disposable base URL."""
import argparse, hashlib, json
from pathlib import Path
from playwright.sync_api import sync_playwright
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def settle(p):
 p.evaluate("""async()=>{await Promise.race([Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>null))),new Promise(r=>setTimeout(r,1000))]);await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));}""")
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def findings(p,w,section='evidence',edit=False):
 if w==390:
  p.locator('#mobileToolSelectUi').click();p.locator('[role="option"]',has_text='Findings').last.click()
 else:p.locator('#tab-findings').click()
 if w==390 and p.locator('#findBackToList').is_visible():p.locator('#findBackToList').click()
 row=p.locator('#findList .find-row[data-id="2"]');row.click() if row.is_visible() else row.evaluate("x=>x.click()");p.locator(f'#findDetail .find-section-nav [data-find-section="{section}"]').click()
 if edit:p.locator('#findToggleEdit').click();p.locator('#findDocActions:not([hidden])').wait_for()
def main(a):
 out=Path(a.out);out.mkdir(parents=True,exist_ok=True);r={'base':a.base,'probe_sha256':sha(__file__),'shots':[],'failures':[]}
 with sync_playwright() as pw:
  for engine in a.engines.split(','):
   b=getattr(pw,engine).launch()
   for w,h in ((1440,900),(1024,768),(390,844)):
    c=b.new_context(viewport={'width':w,'height':h});p=c.new_page();p.set_default_timeout(7000)
    try:
     p.goto(a.base,wait_until='domcontentloaded');ready(p)
     for name,sec,edit in [('overview','overview',False),('evidence','evidence',False),('remediation','remediation',False),('review','review',False),('edit-evidence','evidence',True)]:
      findings(p,w,sec,edit);settle(p);f=out/f'{engine}-{w}x{h}-{name}.png';p.screenshot(path=str(f),animations='disabled');r['shots'].append({'path':str(f),'sha256':sha(f),'surface':name,'engine':engine,'viewport':[w,h]})
      if edit:p.locator('#findToggleEdit').click()
     findings(p,w,'evidence');p.locator('.find-inline-toggle').first.click();p.locator('.find-inline-content').wait_for();settle(p);f=out/f'{engine}-{w}x{h}-inline.png';p.screenshot(path=str(f),animations='disabled');r['shots'].append({'path':str(f),'sha256':sha(f),'surface':'inline','engine':engine,'viewport':[w,h]})
     p.locator('#findExportOpen').click();p.locator('#findExportModal').wait_for();settle(p);f=out/f'{engine}-{w}x{h}-export.png';p.screenshot(path=str(f),animations='disabled');r['shots'].append({'path':str(f),'sha256':sha(f),'surface':'export','engine':engine,'viewport':[w,h]});p.locator('#findExportClose').click()
     p.locator('a.find-open-flow').first.click();p.locator('#flowModal').wait_for();settle(p);f=out/f'{engine}-{w}x{h}-http-popup.png';p.screenshot(path=str(f),animations='disabled');r['shots'].append({'path':str(f),'sha256':sha(f),'surface':'http-popup','engine':engine,'viewport':[w,h]})
    except Exception as e:r['failures'].append(f'{engine}-{w}x{h}: {type(e).__name__}: {e}')
    finally:c.close()
   b.close()
 (out/'report.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps({'shots':len(r['shots']),'failures':r['failures']}));return 1 if r['failures'] else 0
if __name__=='__main__':
 x=argparse.ArgumentParser();x.add_argument('--base',required=True);x.add_argument('--out',required=True);x.add_argument('--engines',default='chromium,firefox,webkit');z=x.parse_args();raise SystemExit(main(z))
