#!/usr/bin/env python3
"""Render Devices settings with generic fixture-only discovery responses."""
import argparse,hashlib,json
from pathlib import Path
from playwright.sync_api import sync_playwright
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
FIXTURES={
 '/api/network/hosts':{'hosts':[{'address':'127.0.0.1','label':'Loopback (fixture)'}]},
 '/api/android/status':{'available':False,'devices':[],'externalBindAllowed':False},
 '/api/ios/status':{'devices':[],'simctlAvailable':False,'externalBindAllowed':False},
 '/api/ios/ssh/status':{'externalBindAllowed':False},
}
def main(a):
 out=Path(a.out);out.mkdir(parents=True,exist_ok=True);r={'base':a.base,'fixture_only':True,'fixtures':FIXTURES,'probe_sha256':sha(__file__),'shots':[],'failures':[]}
 with sync_playwright() as pw:
  b=pw.chromium.launch()
  for w,h in ((1440,900),(390,844)):
   c=b.new_context(viewport={'width':w,'height':h});p=c.new_page();p.set_default_timeout(8000)
   def route(route):
    path=route.request.url.split('?',1)[0].replace(a.base,'')
    if path in FIXTURES:route.fulfill(status=200,content_type='application/json',body=json.dumps(FIXTURES[path]))
    else:route.continue_()
   p.route('**/api/**',route)
   try:
    p.goto(a.base,wait_until='domcontentloaded');p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
    if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
    if w==390:
     p.locator('#mobileToolSelectUi').click();p.locator('[role="option"]',has_text='Settings').last.click()
    else:p.locator('#tab-settings').click()
    nav=p.locator('#setNav button[data-sec="devices"]');nav.click() if nav.is_visible() else nav.evaluate('x=>x.click()')
    p.wait_for_function("getComputedStyle(document.querySelector('.set-sec[data-sec=\"devices\"]')).display!=='none'")
    p.evaluate("""async()=>{await Promise.race([Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>null))),new Promise(r=>setTimeout(r,1000))]);await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));}""")
    f=out/f'chromium-{w}x{h}-devices-generic-fixture.png';p.screenshot(path=str(f),animations='disabled');r['shots'].append({'path':str(f),'sha256':sha(f),'viewport':[w,h]})
   except Exception as e:r['failures'].append(f'{w}x{h}: {type(e).__name__}: {e}')
   finally:c.close()
  b.close()
 (out/'report.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps({'shots':len(r['shots']),'failures':r['failures']}));return 1 if r['failures'] else 0
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--base',required=True);p.add_argument('--out',required=True);a=p.parse_args();raise SystemExit(main(a))
