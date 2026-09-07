#!/usr/bin/env python3
"""Visual-only dark-theme supplement for the retained Repeater Render QA."""
import hashlib, importlib.util, json, os, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT=Path(os.environ.get('INTERSEPTOR_REPO',Path.cwd())).resolve()
OUT=Path(os.environ['QA_OUT'])
ENGINES=('chromium','firefox','webkit')
spec=importlib.util.spec_from_file_location('audit',ROOT/'scripts/ui_browser_audit.py')
if not spec or not spec.loader: raise SystemExit('missing scripts/ui_browser_audit.py')
audit=importlib.util.module_from_spec(spec);sys.modules[spec.name]=audit;spec.loader.exec_module(audit)
BODY=b'<!doctype html><html><body><h1 id="render-marker">QA Render Visual</h1><p>dark theme settled fixture</p><script>window.__qaExecuted=1</script></body></html>'
class Fixture(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.send_header('Content-Length',str(len(BODY)));self.end_headers();self.wfile.write(BODY)
 def log_message(self,*_): return
def sha(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def require(ok,message):
 if not ok: raise AssertionError(message)
def fixture():
 server=ThreadingHTTPServer(('127.0.0.1',0),Fixture);threading.Thread(target=server.serve_forever,daemon=True).start();return server
def settle_dark(page):
 if page.evaluate("document.documentElement.getAttribute('data-theme')==='light'"): page.locator('#themeToggle').click()
 page.wait_for_timeout(400)
 state=page.evaluate("""() => {
  const trigger=document.querySelector('#mobileToolSelectUi');
  const swatch=document.createElement('div');swatch.style.cssText='position:fixed;visibility:hidden;background:var(--bg3)';document.body.append(swatch);
  const expected=getComputedStyle(swatch).backgroundColor,actual=trigger?getComputedStyle(trigger).backgroundColor:'';swatch.remove();
  return {theme:document.documentElement.getAttribute('data-theme')||'dark',actual,expected};
 }""")
 require(state['theme']=='dark','theme toggle did not leave the app in dark mode')
 require(state['actual']==state['expected'],f"mobile trigger background {state['actual']} did not settle to --bg3 {state['expected']}")
 return state
def run_engine(pw,engine,base,fixture_base,source):
 browser=getattr(pw,engine).launch();ctx=browser.new_context(viewport={'width':1440,'height':900});page=ctx.new_page();shots=[];failure=None;theme=None
 try:
  page.goto(base,wait_until='domcontentloaded');audit.wait_ready(page);page.locator('.tab[data-tab=repeater]').click();page.set_viewport_size({'width':390,'height':844});page.locator('#repUrl').fill(fixture_base+'/render');page.locator('#repSend').click();page.locator('#repStatus').get_by_text('200',exact=False).wait_for(timeout=20000);render_button=page.locator('#repResSeg [data-view=render]');render_button.wait_for(state='visible',timeout=10000);render_button.click()
  frame=page.locator('#repResView iframe.http-render-frame');frame.wait_for(state='visible',timeout=10000);handle=frame.element_handle();require(handle is not None,'render frame detached');child=handle.content_frame();require(child is not None,'render frame inaccessible');child.locator('#render-marker').wait_for(state='visible',timeout=10000);require(frame.get_attribute('sandbox')=='','render frame sandbox changed')
  page.locator('.rep-work').evaluate('el=>{el.scrollTop=el.scrollHeight}')
  page.locator('#repResSeg [data-view=render]').wait_for(state='visible',timeout=10000);frame.wait_for(state='visible',timeout=10000)
  theme=settle_dark(page)
  path=OUT/'screenshots'/(engine+'-390-dark-repeater-render-settled.png');path.parent.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(path),animations='disabled');shots.append({'path':str(path),'sha256':sha(path)})
  print('PASS '+engine+' settled dark mobile Repeater Render',flush=True)
 except Exception as exc:
  failure=type(exc).__name__+': '+str(exc);print('FAIL '+engine+': '+failure,file=sys.stderr,flush=True)
 finally:
  ctx.close();browser.close()
 result={'engine':engine,'case':'settled-dark-mobile-repeater-render','failure':failure,'screenshots':shots,'theme':theme,'application_source':source}
 (OUT/(engine+'.json')).write_text(json.dumps(result,indent=2)+'\n');return result
def main():
 if OUT.exists(): raise SystemExit('QA_OUT already exists; refusing to overwrite: '+str(OUT))
 OUT.mkdir(mode=0o700);server=fixture();proc=root=project=reservations=None
 try:
  proc,root,project,base,_proxy,source,reservations=audit.prepare_managed_audit();fixture_base='http://127.0.0.1:'+str(server.server_port)
  with sync_playwright() as pw: results=[run_engine(pw,e,base,fixture_base,source) for e in ENGINES]
  report={'probe':str(Path(__file__).resolve()),'probe_sha256':sha(__file__),'application_source':source,'results':results}
  (OUT/'report.json').write_text(json.dumps(report,indent=2)+'\n');print(OUT/'report.json');return 1 if any(x['failure'] for x in results) else 0
 finally:
  server.shutdown();server.server_close()
  if proc: audit.cleanup_managed_audit(proc,root,project,reservations)
if __name__=='__main__': raise SystemExit(main())
