#!/usr/bin/env python3
"""Real local-echo capture → Inspector → Repeater → persistence → Map browser journey."""
import argparse,hashlib,json,os,sys,time
from pathlib import Path
from playwright.sync_api import sync_playwright
a=argparse.ArgumentParser();a.add_argument('--base',required=True);a.add_argument('--proxy-port',type=int,required=True);a.add_argument('--out',required=True);ns=a.parse_args()
repo=Path(os.environ.get('INTERSEPTOR_REPO',Path.cwd())).resolve();sys.path.insert(0,str(repo/'scripts'));import ui_browser_audit as audit
out=Path(ns.out);out.mkdir(parents=True,exist_ok=True)
r={'base':ns.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'started_unix':time.time(),'cases':[],'screenshots':[],'console_errors':[],'skipped':['Activity flow detail needs a real MCP activity event; excluded to avoid connecting MCP.']}
def save(): (out/'report.json').write_text(json.dumps(r,indent=2,sort_keys=True)+'\n')
def case(n,f):
 try:f();r['cases'].append({'name':n,'status':'pass'})
 except Exception as e:r['cases'].append({'name':n,'status':'fail','reason':type(e).__name__+': '+str(e)})
 save()
def shot(p,n):
 x=out/(n+'.png');p.screenshot(path=str(x),animations='disabled');r['screenshots'].append({'name':n,'path':str(x),'sha256':hashlib.sha256(x.read_bytes()).hexdigest()})
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
srv,thr=audit.start_fixture();origin='http://127.0.0.1:'+str(srv.server_address[1])
try:
 status,body=audit.proxy_request(('127.0.0.1',ns.proxy_port),origin+'/fixture/request-response?generic=1',method='POST',body=b'{"generic":true}')
 if status!=200:raise RuntimeError('fixture capture status '+str(status)+' '+body.decode(errors='replace'))
 with sync_playwright() as pw:
  b=pw.chromium.launch();p=b.new_page(viewport={'width':1440,'height':900},reduced_motion='reduce');p.set_default_timeout(12000)
  p.on('pageerror',lambda e:r['console_errors'].append('pageerror: '+str(e)));p.on('console',lambda m:r['console_errors'].append('console: '+m.text) if m.type=='error' else None)
  p.goto(ns.base,wait_until='domcontentloaded');ready(p)
  def history_inspector_repeater():
   p.locator('#tab-proxy').click();p.locator('#rows .trow').first.wait_for();p.locator('#rows .trow').first.click();p.locator('#inspectSendRepeater').wait_for();p.locator('#inspectSendRepeater').click();p.wait_for_function("document.querySelector('#panel-repeater').classList.contains('active')")
   if origin not in p.locator('#repUrl').input_value():raise AssertionError('repeater URL not hydrated from selected captured flow')
   if 'generic' not in p.locator('#repBody').input_value():raise AssertionError('repeater body not hydrated')
  case('History local capture → Inspector → Repeater editable request',history_inspector_repeater)
  def send_and_history():
   p.locator('#repSend').click();p.wait_for_function("document.querySelector('#repStatus').textContent.includes('200')",timeout=12000);p.locator('#repHistToggle').click();p.locator('#repHistory .h').first.wait_for();shot(p,'repeater-local-echo-history')
  case('Repeater ordinary local echo send → tab history',send_and_history)
  def edit_persist():
   label='?generic=1&draft=persisted';p.locator('#repUrl').fill(origin+'/fixture/request-response'+label);p.locator('#repHeaders').fill('Content-Type: application/json\nX-Local-Fixture: true');p.wait_for_timeout(700);p.reload(wait_until='domcontentloaded');ready(p);p.locator('#tab-repeater').click();p.locator('#repUrl').wait_for()
   if label not in p.locator('#repUrl').input_value():raise AssertionError('Repeater local draft missing after reload')
  case('Repeater editor draft persists across reload',edit_persist)
  def map_flow():
   p.locator('#tab-map').click();p.wait_for_timeout(600);p.locator('#mapViewSeg [data-v="table"]').click();p.locator('#mapRefresh').click();p.locator('#mapTable tr[data-flow]').first.wait_for(timeout=8000)
   p.locator('#mapSearch').fill('fixture');p.wait_for_timeout(450);row=p.locator('#mapTable tr[data-flow]').first;row.wait_for();row.click();p.locator('#flowModal').wait_for();shot(p,'map-local-flow-inspector');p.locator('#fmProxy').click();p.wait_for_function("document.querySelector('#panel-proxy').classList.contains('active')")
  case('Captured flow appears in Map table/search → Inspector → History',map_flow)
  b.close()
finally:
 srv.shutdown();srv.server_close()
r['finished_unix']=time.time();r['summary']={'pass':sum(x['status']=='pass' for x in r['cases']),'fail':sum(x['status']=='fail' for x in r['cases']),'console_errors':len(r['console_errors'])};save();raise SystemExit(1 if r['summary']['fail'] or r['console_errors'] else 0)
