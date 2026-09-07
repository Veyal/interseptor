#!/usr/bin/env python3
import argparse,hashlib,json,time
from pathlib import Path
from playwright.sync_api import sync_playwright
q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--out',required=True);a=q.parse_args();o=Path(a.out);o.mkdir(parents=True,exist_ok=True)
r={'base':a.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'started_unix':time.time(),'cases':[],'screenshots':[],'console_errors':[],'mocked':['Generic 503 fault injection for History note; generic read-only Activity and Intercept routes only. No Forward, Drop, Send, scan, authz, or external operation.']}
def save():(o/'report.json').write_text(json.dumps(r,indent=2,sort_keys=True)+'\n')
def shot(p,n):
 f=o/(n+'.png');p.screenshot(path=str(f),animations='disabled');r['screenshots'].append({'name':n,'path':str(f),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()})
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def nav(p,n,phone=False):
 if p.locator('#panel-'+n).evaluate("e=>e.classList.contains('active')"):return
 if phone:
  trigger=p.locator('#mobileToolSelectUi');menu=trigger.get_attribute('aria-controls');trigger.click();p.locator('#'+menu+' .ui-select-opt[data-value="'+n+'"]').last.click()
 else:p.locator('#tab-'+n).click()
 p.wait_for_function("x=>document.querySelector('#panel-'+x).classList.contains('active')",arg=n)
def case(n,k,f):
 try:f();r['cases'].append({'name':n,'kind':k,'status':'pass'})
 except Exception as e:r['cases'].append({'name':n,'kind':k,'status':'fail','reason':type(e).__name__+': '+str(e)})
 save()
with sync_playwright() as pw:
 b=pw.chromium.launch();c=b.new_context(viewport={'width':1440,'height':900},accept_downloads=True);p=c.new_page();p.set_default_timeout(15000);p.on('console',lambda m:r['console_errors'].append(m.text) if m.type=='error' and '503' not in m.text else None);p.on('pageerror',lambda e:r['console_errors'].append(str(e)));p.goto(a.base,wait_until='domcontentloaded');ready(p)
 def note_and_view():
  nav(p,'proxy');row=p.locator('#rows .trow').first;row.wait_for();row.click();fid=row.get_attribute('data-id');txt='generic exact history note recovery v5';fail={'on':True}
  def route(z):
   if z.request.method=='PUT' and '/api/flows/'+fid+'/note' in z.request.url and fail['on']:return z.fulfill(status=503,content_type='application/json',body='{"error":"generic note rejection"}')
   return z.continue_()
  p.route('**/*',route);p.locator('#noteInput').fill(txt);p.locator('#noteInput').press('Enter');p.locator('#noteRetry').wait_for(state='visible');p.wait_for_function("()=>document.querySelector('#noteSaved').dataset.state==='error'")
  fail['on']=False;p.locator('#noteRetry').click();p.wait_for_function("()=>document.querySelector('#noteSaved').dataset.state==='saved'&&document.querySelector('#noteRetry').hidden")
  p.reload(wait_until='domcontentloaded');ready(p);nav(p,'proxy');p.locator('#rows .trow[data-id="'+fid+'"]').click();p.locator('#noteInput').wait_for()
  if p.locator('#noteInput').input_value()!=txt:raise AssertionError('History note exact text absent after reload')
  view='Generic exact saved view v5';needle='generic-view-filter-v5';p.locator('#fSearch').fill(needle);p.wait_for_timeout(350);p.locator('#viewsBtn').click();p.get_by_text('Save current filters as a view').click();p.locator('#promptInput').fill(view);p.locator('#promptOk').click();p.wait_for_function("()=>document.querySelector('#toast').textContent.includes('view saved')")
  p.reload(wait_until='domcontentloaded');ready(p);nav(p,'proxy');p.locator('#viewsBtn').click();p.get_by_text(view,exact=True).first.wait_for();p.locator('body').press('Escape');p.locator('#fSearch').fill('');p.locator('#viewsBtn').click();p.get_by_text(view,exact=True).first.click();p.wait_for_function("x=>document.querySelector('#fSearch').value===x",arg=needle)
  shot(p,'history-note-retry-and-saved-view')
 case('MIXED History note rejected→Retry→Saved exact reload; saved view reappears and applies filter','mixed',note_and_view)
 def postman():
  nav(p,'repeater');f=o/'generic-current-postman.json';url='http://example.com/current-postman?fixture=1';body_source='{"generic":"postman"}';body='{\n  "generic": "postman"\n}';f.write_text(json.dumps({'info':{'name':'Generic Current Collection','schema':'https://schema.getpostman.com/json/collection/v2.1.0/collection.json'},'item':[{'name':'Current generic request','request':{'method':'POST','header':[{'key':'Content-Type','value':'application/json'}],'url':url,'body':{'mode':'raw','raw':body_source}}}]}))
  p.locator('#repPostmanImport').click();p.locator('#repPostmanFile').set_input_files(str(f));p.wait_for_function("()=>document.querySelector('#toast').textContent.includes('Postman: loaded 1 request')")
  def exact():return p.locator('#repMethod').input_value()=='POST' and p.locator('#repUrl').input_value()==url and p.locator('#repBody').input_value()==body
  if not exact():raise AssertionError('Postman method/URL/body mismatch before reload')
  p.reload(wait_until='domcontentloaded');ready(p);nav(p,'repeater')
  if not exact():raise AssertionError('Postman unsent request did not persist exact method/URL/body')
  shot(p,'postman-unsent-exact-persisted')
 case('REAL Postman import creates unsent exact request that persists across reload','real',postman)
 c.close();b.close()
with sync_playwright() as pw:
 b=pw.chromium.launch();c=b.new_context(viewport={'width':390,'height':844});p=c.new_page();p.set_default_timeout(15000);target={'id':None}
 def route(z):
  u=z.request.url
  if u.endswith('/api/activity'):
   return z.fulfill(content_type='application/json',body=json.dumps({'activity':[{'id':'generic-current-activity','tool':'fixture','ok':True,'summary':'open generic flow #'+str(target['id']),'result':'opened flow #'+str(target['id']),'ts':1}]}))
  if u.endswith('/api/intercept'):
   return z.fulfill(content_type='application/json',body=json.dumps({'enabled':False,'responseEnabled':False,'queue':[{'id':77,'method':'POST','host':'example.com','path':'/generic-held','len':42}],'responseQueue':[]}))
  if '/api/intercept/held/77/raw' in u:return z.fulfill(content_type='application/json',body=json.dumps({'raw':'POST /generic-held HTTP/1.1\r\nHost: example.com\r\n\r\n{"generic":true}'}))
  return z.continue_()
 p.route('**/*',route);p.on('console',lambda m:r['console_errors'].append(m.text) if m.type=='error' else None);p.on('pageerror',lambda e:r['console_errors'].append(str(e)));p.goto(a.base,wait_until='domcontentloaded');ready(p)
 def activity():
  nav(p,'proxy',True);row=p.locator('#rows .trow').first;row.wait_for();target['id']=row.get_attribute('data-id');nav(p,'activity',True);p.locator('#actFeed .act-row').first.wait_for();p.locator('#actFeed .act-row').first.click();p.wait_for_function("id=>document.querySelector('#panel-proxy').classList.contains('active')&&document.querySelector('#rows .trow[data-id=\"'+id+'\"]')?.classList.contains('sel')",arg=target['id'])
  shot(p,'mock-activity-correct-flow')
 case('MOCK Activity exact generic flow ID selects its History row','mock',activity)
 def intercept():
  nav(p,'intercept',True);row=p.locator('#heldList .icpt-item[data-id="77"][data-side="req"]');row.wait_for();row.click();p.wait_for_function("()=>document.querySelector('#heldRaw').value.includes('POST /generic-held HTTP/1.1')&&document.querySelector('#heldRaw').value.includes('Host: example.com')")
  if p.locator('#heldRaw').input_value()!='POST /generic-held HTTP/1.1\nHost: example.com\n\n{"generic":true}':raise AssertionError('held raw editor differs from the line-ending-normalized exact generic mock')
  shot(p,'mock-held-queue-exact-editor')
 case('MOCK held queue opens exact raw editor without Forward or Drop','mock',intercept)
 c.close();b.close()
r['finished_unix']=time.time();r['summary']={'pass':sum(x['status']=='pass' for x in r['cases']),'fail':sum(x['status']=='fail' for x in r['cases']),'console_errors':len(r['console_errors'])};save();raise SystemExit(1 if r['summary']['fail'] or r['console_errors'] else 0)
