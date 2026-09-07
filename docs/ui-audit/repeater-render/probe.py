#!/usr/bin/env python3
"""Portable, local-only browser QA for Repeater RESPONSE Render."""
import hashlib, importlib.util, json, os, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT=Path(os.environ.get("INTERSEPTOR_REPO",Path.cwd())).resolve()
OUT=Path(os.environ["QA_OUT"])
ENGINES=tuple(e for e in os.environ.get("QA_ENGINES","chromium,firefox,webkit").split(",") if e)
spec=importlib.util.spec_from_file_location("audit",ROOT/"scripts/ui_browser_audit.py")
if not spec or not spec.loader: raise SystemExit("INTERSEPTOR_REPO must contain scripts/ui_browser_audit.py")
audit=importlib.util.module_from_spec(spec);sys.modules[spec.name]=audit;spec.loader.exec_module(audit)

HTML_A=b'<!doctype html><html><body><h1 id="render-marker">QA Render A</h1><p>fixture body A</p><script>window.__qaExecuted=1;document.body.dataset.executed="yes"</script></body></html>'
HTML_B=b'<!doctype html><html><body><h1 id="render-marker">QA Render B</h1><p>fixture body B</p><script>window.__qaExecuted=1</script></body></html>'
JSON_BODY=b'{"fixture":"json","answer":42}'
class Fixture(BaseHTTPRequestHandler):
 protocol_version="HTTP/1.1"
 def do_GET(self):
  if self.path=="/qa/render-html": body,mime,status=HTML_A,"text/html; charset=utf-8",200
  elif self.path=="/qa/render-html-new": body,mime,status=HTML_B,"text/html; charset=utf-8",200
  elif self.path=="/qa/render-json": body,mime,status=JSON_BODY,"application/json",200
  elif self.path=="/qa/render-delay": time.sleep(1.2);body,mime,status=HTML_B,"text/html; charset=utf-8",200
  else: body,mime,status=b"not found","text/plain; charset=utf-8",404
  self.send_response(status);self.send_header("Content-Type",mime);self.send_header("Cache-Control","no-store");self.send_header("Content-Length",str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*_): return
def fixture_start():
 s=ThreadingHTTPServer(("127.0.0.1",0),Fixture);threading.Thread(target=s.serve_forever,daemon=True).start();return s
def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def ensure(v,msg):
 if not v: raise AssertionError(msg)
def passed(cases,name):
 cases[name]="pass";print("PASS "+name,flush=True)
def set_theme(page,desired):
 current=page.evaluate("document.documentElement.getAttribute('data-theme')==='light'?'light':'dark'")
 if current!=desired: page.locator("#themeToggle").click()
 ensure(page.evaluate("document.documentElement.getAttribute('data-theme')==='light'?'light':'dark'")==desired,"theme toggle did not apply "+desired)
def reveal_mobile_response(page):
 if page.locator("#repHistToggle").get_attribute("aria-expanded")=="true": page.locator("#repHistToggle").click()
 page.locator(".rep-work").evaluate("el=>{el.scrollTop=el.scrollHeight}")
 page.locator('#repResSeg [data-view="render"]').wait_for(state="visible",timeout=10000)
 page.locator('#repResView iframe.http-render-frame').wait_for(state="visible",timeout=10000)
def shot(page,name,shots):
 p=OUT/"screenshots"/name;p.parent.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(p),full_page=True);shots.append({"path":str(p),"sha256":sha(p)})
def send(page,url):
 page.locator("#repUrl").fill(url);page.locator("#repSend").click();page.locator("#repStatus").get_by_text("200",exact=False).wait_for(timeout=20000)
def choose(page,view):
 b=page.locator(f'#repResSeg [data-view="{view}"]');b.wait_for(state="visible",timeout=10000);b.click();page.wait_for_timeout(150)
def rendered(page,marker):
 f=page.locator('#repResView iframe.http-render-frame');f.wait_for(state="visible",timeout=15000)
 ensure(f.get_attribute("sandbox")=="","Render iframe sandbox must be empty")
 doc=f.get_attribute("srcdoc") or "";ensure(marker in doc,f"missing {marker} in render srcdoc");ensure("HTTP/1.1" not in doc and "Content-Type:" not in doc,"headers leaked into Render document")
 handle=f.element_handle();ensure(handle is not None,"Render iframe is not attached");frame=handle.content_frame();ensure(frame is not None,"Render iframe has no child frame");frame.locator("#render-marker").wait_for(state="visible",timeout=10000);ensure(frame.locator("#render-marker").inner_text()==marker,"wrong Render iframe body")
 ensure(frame.locator("body").evaluate("body => body.ownerDocument.defaultView.__qaExecuted || body.dataset.executed || ''") in (None,""),"script ran inside sandboxed Render iframe")
 return f
def run(pw,engine):
 browser=getattr(pw,engine).launch();ctx=browser.new_context(viewport={"width":1440,"height":900});page=ctx.new_page()
 cases={};shots=[];fails=[];proc=root=project=base=proxy=source=res=None;fixture=None
 try:
  proc,root,project,base,proxy,source,res=audit.prepare_managed_audit();fixture=fixture_start();fb=f"http://127.0.0.1:{fixture.server_port}"
  page.goto(base,wait_until="domcontentloaded");audit.wait_ready(page);page.locator(".tab[data-tab=repeater]").click();page.locator("#repUrl").wait_for(state="visible",timeout=15000)
  # HTML response defaults to Pretty: exercising Render requires an explicit click.
  send(page,fb+"/qa/render-html");page.locator('#repResSeg [data-view="render"]').wait_for(state="visible",timeout=15000);choose(page,"render");rendered(page,"QA Render A");shot(page,f"{engine}-1440-light-repeater-render-a.png",shots);passed(cases,"html-send-render-sandbox-header-separation")
  for view in ("raw","pretty","decoded","render"): choose(page,view)
  rendered(page,"QA Render A");page.set_viewport_size({"width":1024,"height":768});shot(page,f"{engine}-1024-light-repeater-response-modes.png",shots);passed(cases,"raw-pretty-decoded-render-transitions")
  send(page,fb+"/qa/render-html-new");choose(page,"render");rendered(page,"QA Render B");passed(cases,"new-html-replaces-previous-preview")
  send(page,fb+"/qa/render-json");ensure(page.locator('#repResSeg [data-view="render"]').is_hidden(),"Render visible for JSON response");page.locator("#repResView").get_by_text("fixture",exact=False).wait_for(timeout=15000);ensure(page.locator('#repResView iframe.http-render-frame').count()==0,"JSON retained Render iframe");passed(cases,"json-render-hidden-pretty-fallback")
  # Pending and failed loopback sends must clear a previous iframe.
  send(page,fb+"/qa/render-html");choose(page,"render");rendered(page,"QA Render A");page.locator("#repUrl").fill(fb+"/qa/render-delay");page.locator("#repSend").click();page.locator("#repResView").get_by_text("sending",exact=False).wait_for(timeout=4000);ensure(page.locator('#repResView iframe.http-render-frame').count()==0,"pending request retained iframe");page.locator("#repStatus").get_by_text("200",exact=False).wait_for(timeout=15000)
  page.locator("#repUrl").fill("http://127.0.0.1:1/qa/unavailable");page.locator("#repSend").click();page.locator("#repStatus").get_by_text("502",exact=False).wait_for(timeout=15000);ensure(page.locator('#repResView iframe.http-render-frame').count()==0,"terminal 502 request retained iframe");passed(cases,"pending-error-clear-stale-preview")
  # New Repeater tab has independent result state; return to first tab retains its error.
  page.locator("#repTabsAdd").click();send(page,fb+"/qa/render-html-new");choose(page,"render");rendered(page,"QA Render B");page.locator("#repTabs .rt-select").first.click();page.locator("#repResView").get_by_text("502",exact=False).wait_for(timeout=10000);ensure(page.locator('#repResView iframe.http-render-frame').count()==0,"first tab showed second tab preview");passed(cases,"per-tab-response-ownership")
  # Repeater history selection must render the earlier HTML response in the same shared renderer.
  if page.locator("#repHistToggle").get_attribute("aria-expanded")!="true": page.locator("#repHistToggle").click()
  page.locator("#repHistory .h").last.wait_for(state="visible",timeout=10000);page.locator("#repHistory .h").last.click();page.locator("#repStatus").get_by_text("200",exact=False).wait_for(timeout=10000);choose(page,"render");rendered(page,"QA Render A");page.set_viewport_size({"width":390,"height":844});reveal_mobile_response(page);set_theme(page,"light");shot(page,f"{engine}-390-light-repeater-render.png",shots);set_theme(page,"dark");shot(page,f"{engine}-390-dark-repeater-render.png",shots);passed(cases,"repeater-history-render")
  page.set_viewport_size({"width":1440,"height":900})
  # Proxy a local fixture response and compare History's renderer, including retained Inspect action and removed row hint.
  status,_=audit.proxy_request(proxy,fb+"/qa/render-html");ensure(status==200,f"proxy fixture status {status}");page.locator(".tab[data-tab=proxy]").click();row=page.locator("#rows .trow").first;row.wait_for(state="visible",timeout=15000);ensure("Click inspect" not in (row.get_attribute("title") or ""),"obsolete history-row instruction remains");row.hover();row.click();page.locator("#inspect").wait_for(state="visible",timeout=10000);row.click(modifiers=["Control"]);ensure(page.locator('[title*="Click inspect"]').count()==0,"instructional hint appeared after row actions")
  hrender=page.locator('#inspect .seg[data-side="res"] [data-view="render"]');hrender.wait_for(state="visible",timeout=10000);hrender.click();hist_iframe=page.locator('#resView iframe.http-render-frame');hist_iframe.wait_for(state="visible",timeout=15000);ensure(hist_iframe.get_attribute("sandbox")=="","History sandbox differs");ensure("QA Render A" in (hist_iframe.get_attribute("srcdoc") or ""),"History Render differs from Repeater")
  page.set_viewport_size({"width":390,"height":844});set_theme(page,"dark");shot(page,f"{engine}-390-dark-history-render.png",shots);passed(cases,"history-render-parity-row-hover-click-ctrl")
 except Exception as e:
  message=type(e).__name__+": "+str(e);fails.append(message);print("FAIL "+engine+": "+message,file=sys.stderr,flush=True)
  try: shot(page,f"{engine}-failure.png",shots);(OUT/f"{engine}-failure-dom.html").write_text(page.content())
  except Exception as cap: fails.append("failure capture: "+repr(cap))
 finally:
  if fixture: fixture.shutdown();fixture.server_close()
  if proc: audit.cleanup_managed_audit(proc,root,project,res)
  ctx.close();browser.close()
 result={"engine":engine,"cases":cases,"failures":fails,"screenshots":shots,"application_source":source}
 (OUT/(engine+".json")).write_text(json.dumps(result,indent=2)+"\n");print(OUT/(engine+".json"),flush=True)
 return result
def main():
 if OUT.exists(): raise SystemExit(f"QA_OUT already exists; refusing to overwrite retained evidence: {OUT}")
 OUT.mkdir(mode=0o700)
 started=time.time()
 with sync_playwright() as pw: reports=[run(pw,e) for e in ENGINES]
 report={"started_unix":started,"finished_unix":time.time(),"probe":str(Path(__file__).resolve()),"probe_sha256":sha(__file__),"reports":reports}
 (OUT/"report.json").write_text(json.dumps(report,indent=2)+"\n");print(OUT/"report.json");return 1 if any(r["failures"] for r in reports) else 0
if __name__=="__main__": raise SystemExit(main())
