#!/usr/bin/env python3
import argparse,hashlib,json,time
from pathlib import Path
from playwright.sync_api import sync_playwright, TimeoutError as PlaywrightTimeoutError
q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--out',required=True);a=q.parse_args();o=Path(a.out);o.mkdir(parents=True,exist_ok=True)
r={'base':a.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'started_unix':time.time(),'kind':'mixed: real Notes persistence plus generic mocked pending Project identity','cases':[],'screenshots':[],'console_errors':[],'mocked':['GET /api/project and POST /api/project/switch are generic routes and never trigger a candidate project restart.']}
def shot(p,n):
 x=o/n;p.screenshot(path=str(x),full_page=True);r['screenshots'].append({'path':str(x),'sha256':hashlib.sha256(x.read_bytes()).hexdigest()})
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
with sync_playwright() as pw:
 b=pw.chromium.launch();c=b.new_context(viewport={'width':1440,'height':900});p=c.new_page();p.set_default_timeout(12000);s={'posts':0,'polls':0}
 def route(z):
  u=z.request.url
  if '/api/project/switch' in u and z.request.method=='POST':s['posts']+=1;return z.fulfill(content_type='application/json',body='{"switching":"generic-next"}')
  if '/api/project' in u:
   if s['posts']:s['polls']+=1
   return z.fulfill(content_type='application/json',body='{"current":"generic-current","dir":"/generic/audit/current","projects":[{"name":"generic-current"},{"name":"generic-next"}],"canSwitch":true}')
  return z.continue_()
 p.route('**/*',route);p.on('console',lambda m:r['console_errors'].append(m.text) if m.type=='error' else None);p.on('pageerror',lambda e:r['console_errors'].append(str(e)))
 try:
  p.goto(a.base,wait_until='domcontentloaded');ready(p);p.locator('#tab-notes').click();draft='## generic modal guard\n\nbackground input must stay unchanged v3';p.locator('#notesEdit').fill(draft);p.wait_for_function("()=>document.querySelector('#notesStatus').dataset.state==='saved'")
  p.locator('#projBadge').click();p.locator('#projModal').wait_for(state='visible');p.locator('#pmList .pm-row[data-proj="generic-next"]').click();p.wait_for_function("()=>document.querySelector('#pmClose').disabled&&document.querySelector('#pmNew').disabled")
  focus=[]
  for _ in range(12):
   p.keyboard.press('Tab');focus.append(p.evaluate("()=>({id:document.activeElement?.id||'',inside:document.querySelector('#projModal')?.contains(document.activeElement)})"))
  if not all(x['inside'] for x in focus):raise AssertionError('Tab escaped the pending Project dialog: '+json.dumps(focus))
  p.keyboard.press('Meta+K');p.wait_for_timeout(150)
  if p.locator('#cmdkInput').count() and p.locator('#cmdkInput').is_visible():raise AssertionError('Meta+K opened command palette over pending Project dialog')
  # Actual pointer action only: no force, JS focus, or synthetic dispatch. Overlay must intercept it.
  intercepted=False
  try:p.locator('#notesEdit').click(timeout=900)
  except PlaywrightTimeoutError as e:intercepted='intercepts pointer events' in str(e)
  if not intercepted:raise AssertionError('background Notes editor was not pointer-intercepted by pending modal')
  p.keyboard.type(' SHOULD-NOT-APPEAR');p.wait_for_timeout(100)
  if p.locator('#notesEdit').input_value()!=draft:raise AssertionError('keyboard changed background Notes draft while switch modal pending')
  if not p.locator('#projModal').is_visible() or not p.locator('#pmNew').is_disabled():raise AssertionError('pending Project modal was dismissed or inputs enabled')
  shot(p,'pending-switch-tab-cmdk-background-guard.png')
  r['cases'].append({'name':'pending switch keeps Tab focus in dialog; blocks Cmd+K and actual background Notes pointer/typing','status':'pass','tab_focus':focus,'project_switch_posts':s['posts'],'project_polls':s['polls'],'background_pointer_intercepted':intercepted,'notes_text':draft})
 except Exception as e:
  r['cases'].append({'name':'pending switch modal guard','status':'fail','reason':type(e).__name__+': '+str(e)})
  try:shot(p,'pending-switch-modal-guard-failure.png')
  except Exception:pass
 c.close();b.close()
r['finished_unix']=time.time();r['summary']={'pass':sum(x['status']=='pass' for x in r['cases']),'fail':sum(x['status']=='fail' for x in r['cases']),'console_errors':len(r['console_errors'])};(o/'report.json').write_text(json.dumps(r,indent=2,sort_keys=True)+'\n');raise SystemExit(1 if r['summary']['fail'] or r['console_errors'] else 0)
