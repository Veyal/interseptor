#!/usr/bin/env python3
import argparse,hashlib,json,time
from pathlib import Path
from playwright.sync_api import sync_playwright
q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--out',required=True);a=q.parse_args();o=Path(a.out);o.mkdir(parents=True,exist_ok=True)
r={'base':a.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'cases':[],'console_errors':[],'mocked':[],'started_unix':time.time()}
def save():(o/'report.json').write_text(json.dumps(r,indent=2,sort_keys=True)+'\n')
def C(n,k,f):
 try:f();r['cases'].append({'name':n,'kind':k,'status':'pass'})
 except Exception as e:r['cases'].append({'name':n,'kind':k,'status':'fail','reason':type(e).__name__+': '+str(e)})
 save()
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def nav(p,x):p.locator('#tab-'+x).click();p.wait_for_function("x=>document.querySelector('#panel-'+x).classList.contains('active')",arg=x)
def count(p):return len(p.evaluate("async()=>((await (await fetch('/api/flows?limit=10000')).json()).flows||[])"))
def clear(p):p.evaluate("()=>document.querySelector('#toast').textContent='' ")
def waittoast(p,s):p.wait_for_function("s=>document.querySelector('#toast').textContent.includes(s)",arg=s)
def confirm(p):p.locator('#confirmModal').wait_for();p.locator('#confirmOk').click()
with sync_playwright() as pw:
 b=pw.chromium.launch();c=b.new_context(viewport={'width':1440,'height':900},accept_downloads=True);p=c.new_page();p.set_default_timeout(12000);p.on('pageerror',lambda e:r['console_errors'].append(str(e)));p.goto(a.base,wait_until='domcontentloaded');ready(p)
 def imports():
  nav(p,'settings');p.locator('#setNav button[data-sec="project"]').click();n=count(p)
  with p.expect_download() as d:p.locator('#exportHAR').click()
  f=o/'x.har';d.value.save_as(str(f));h=json.loads(f.read_text());e=h['log']['entries'][0];e['request']['url']='http://example.com/final-har';e['request']['method']='POST';e['response']['status']=201;f.write_text(json.dumps(h));clear(p);p.locator('#importHARBtn').click();p.locator('#importHARFile').set_input_files(str(f));confirm(p);waittoast(p,'HAR import: 1 entry');p.wait_for_timeout(300)
  fs=p.evaluate("async()=>((await (await fetch('/api/flows?limit=10000')).json()).flows||[])");hit=[x for x in fs if x['host']=='example.com' and x['path']=='/final-har' and x['method']=='POST' and x['status']==201]
  if count(p)!=n+1 or not hit:raise AssertionError('HAR count/exact flow assertion failed')
  hid=hit[0]['id'];p.reload(wait_until='domcontentloaded');ready(p)
  if not any(x['id']==hid for x in p.evaluate("async()=>((await (await fetch('/api/flows?limit=10000')).json()).flows||[])")):raise AssertionError('HAR flow absent after reload')
  nav(p,'settings');p.locator('#setNav button[data-sec="project"]').click();n=count(p)
  with p.expect_download() as d:p.locator('#exportProject').click()
  f=o/'x.json';d.value.save_as(str(f));x=json.loads(f.read_text());e=x['har']['log']['entries'][0];e['request']['url']='http://example.com/final-project';e['request']['method']='PUT';e['response']['status']=202;f.write_text(json.dumps(x));clear(p);p.locator('#importProjectBtn').click();p.locator('#importProjectFile').set_input_files(str(f));waittoast(p,'imported ');p.wait_for_timeout(300)
  fs=p.evaluate("async()=>((await (await fetch('/api/flows?limit=10000')).json()).flows||[])");hit=[x for x in fs if x['host']=='example.com' and x['path']=='/final-project' and x['method']=='PUT' and x['status']==202]
  if count(p)<=n or not hit:raise AssertionError('project count/exact flow assertion failed')
  pid=hit[0]['id'];p.reload(wait_until='domcontentloaded');ready(p)
  if not any(x['id']==pid for x in p.evaluate("async()=>((await (await fetch('/api/flows?limit=10000')).json()).flows||[])")):raise AssertionError('project flow absent after reload')
 C('REAL HAR/project import increases count and exact flow fields survive reload','real',imports)
 def scope_capture():
  nav(p,'settings');p.locator('#setNav button[data-sec="scope"]').click();host='final-scope.example.com';p.locator('#newScopeHost').fill(host);p.locator('#newScopePath').fill('/final');p.locator('#addScopeBtn').click();row=p.locator('#scopeBody tr',has=p.locator('input[value="'+host+'"]').first).first;row.wait_for();row.locator('input[data-k="enabled"]').uncheck();p.reload(wait_until='domcontentloaded');ready(p);nav(p,'settings');p.locator('#setNav button[data-sec="scope"]').click();row=p.locator('#scopeBody tr',has=p.locator('input[value="'+host+'"]').first).first;row.wait_for()
  if row.locator('input[data-k="enabled"]').is_checked():raise AssertionError('named scope rule enabled')
  nav(p,'settings');p.locator('#setNav button[data-sec="proxy"]').click();t=p.locator('#capScopeToggle');old=t.get_attribute('aria-pressed');p.route('**/api/settings',lambda z:z.fulfill(status=503,body='generic') if z.request.method=='PUT' else z.continue_());clear(p);t.click();waittoast(p,'capture:');p.wait_for_timeout(250)
  if t.get_attribute('aria-pressed')!=old:raise AssertionError('rejected capture did not roll back')
  p.unroute('**/api/settings');t.click();p.wait_for_timeout(350);v=p.evaluate("async()=>!!(await (await fetch('/api/settings')).json()).captureScopeOnly")
  if v!=(t.get_attribute('aria-pressed')=='true'):raise AssertionError('accepted capture not durable')
 C('MIXED named scope disable + rejected/accepted capture writes durable','mixed',scope_capture)
 def notes_compare_decoder():
  nav(p,'notes');txt='## final retry\n\nexact text';p.locator('#notesEdit').fill(txt);p.route('**/api/notes',lambda z:z.fulfill(status=503,body='generic') if z.request.method=='PUT' else z.continue_());p.locator('#notesSeg button[data-m="preview"]').click();p.locator('#notesSaveRetry').wait_for();p.unroute('**/api/notes');p.locator('#notesSaveRetry').click();p.wait_for_function("()=>document.querySelector('#notesStatus').dataset.state==='saved'&&document.querySelector('#notesSaveRetry').hidden")
  p.reload(wait_until='domcontentloaded');ready(p);nav(p,'notes');
  if p.locator('#notesEdit').input_value()!=txt:raise AssertionError('exact note absent')
  nav(p,'proxy');rows=p.locator('#rows .trow');rows.nth(0).click();i=rows.nth(0).get_attribute('data-id');rows.nth(1).click(modifiers=['Meta']);j=rows.nth(1).get_attribute('data-id');p.locator('#selCompare').click();p.locator('#compareBody').wait_for();
  p.wait_for_function("()=>document.querySelector('#compareBody').innerText.includes('RESPONSE BODY')")
  if '#'+i not in p.locator('#compareTitle').inner_text() or '#'+j not in p.locator('#compareTitle').inner_text() or 'RESPONSE BODY' not in p.locator('#compareBody').inner_text():raise AssertionError('compare identity/content missing')
  p.locator('#compareClose').click();p.locator('#cmdkBtn').click();p.locator('#cmdkInput').fill('Open Decoder');p.locator('#cmdkOpt0').click();p.locator('#decIn').fill('generic');p.locator('#decOps [data-op="base64encode"]').click();p.wait_for_function("()=>document.querySelector('#decOut').value==='Z2VuZXJpYw=='")
 C('MIXED exact Notes retry/reload; Compare identities; Decoder exact output','mixed',notes_compare_decoder)
 c.close();b.close()
r['mocked']=['Generic 503 fault injection for Notes and capture preference writes.'];r['finished_unix']=time.time();r['summary']={'pass':sum(x['status']=='pass' for x in r['cases']),'fail':sum(x['status']=='fail' for x in r['cases']),'console_errors':len(r['console_errors'])};save();raise SystemExit(1 if r['summary']['fail'] or r['console_errors'] else 0)
