#!/usr/bin/env python3
import argparse, hashlib, json, time
from pathlib import Path
from playwright.sync_api import sync_playwright

q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--out',required=True);a=q.parse_args()
o=Path(a.out);o.mkdir(parents=True,exist_ok=True)
r={'base':a.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'started_unix':time.time(),'cases':[],'console_errors':[],'mocked':['All Project API identities/switch outcomes are generic browser routes; Notes PUT is fault-injected only until Retry. No candidate project restart is requested.']}
def save():(o/'report.json').write_text(json.dumps(r,indent=2,sort_keys=True)+'\n')
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def nav(p,x):p.locator('#tab-'+x).click();p.wait_for_function("x=>document.querySelector('#panel-'+x).classList.contains('active')",arg=x)
def new_page(pw,mode,events):
 b=pw.chromium.launch();c=b.new_context(viewport={'width':1440,'height':900});p=c.new_page();p.set_default_timeout(15000)
 state={'notes_fail':mode=='notes','posts':0,'polls':0,'target':mode=='accept','mode':mode}
 def route(z):
  u=z.request.url
  if '/api/project/switch' in u and z.request.method=='POST':
   state['posts']+=1;events.append({'event':'project.switch.post','body':z.request.post_data or ''})
   if mode=='reject':return z.fulfill(status=503,content_type='application/json',body=json.dumps({'error':'generic rejection'}))
   state['polls']=0;return z.fulfill(content_type='application/json',body=json.dumps({'switching':'generic-next'}))
  if '/api/project' in u:
   if state['posts']:
    state['polls']+=1;cur='generic-next' if state['target'] and state['polls']>=2 else 'generic-current'
   else:cur='generic-current'
   events.append({'event':'project.get','current':cur,'poll':state['polls']})
   return z.fulfill(content_type='application/json',body=json.dumps({'current':cur,'dir':'/generic/audit/'+cur,'projects':[{'name':'generic-current','path':''},{'name':'generic-next','path':''}],'canSwitch':True}))
  if '/api/notes' in u and z.request.method=='PUT' and state['notes_fail']:
   events.append({'event':'notes.put.rejected','body':z.request.post_data or ''})
   return z.fulfill(status=503,content_type='application/json',body=json.dumps({'error':'generic rejected notes save'}))
  return z.continue_()
 p.route('**/*',route);p.on('console',lambda m:r['console_errors'].append(m.text) if m.type=='error' and '503' not in m.text else None);p.on('pageerror',lambda e:r['console_errors'].append(str(e)))
 p.goto(a.base,wait_until='domcontentloaded');ready(p)
 return b,c,p,state
def case(name,f):
 try:f();r['cases'].append({'name':name,'status':'pass'})
 except Exception as e:r['cases'].append({'name':name,'status':'fail','reason':type(e).__name__+': '+str(e)})
 save()
with sync_playwright() as pw:
 def notes_guard_retry():
  events=[];b,c,p,s=new_page(pw,'notes',events);txt='## generic guard retry\n\nexact durable recovery text'
  try:
   nav(p,'notes');p.locator('#notesEdit').fill(txt);p.locator('#notesSeg button[data-m="preview"]').click();p.locator('#notesSaveRetry').wait_for(state='visible');p.wait_for_function("()=>document.querySelector('#notesStatus').dataset.state==='error'")
   p.locator('#projBadge').click();p.locator('#projModal').wait_for(state='visible');p.locator('#pmList .pm-row[data-proj="generic-next"]').click();p.wait_for_timeout(300)
   if s['posts']!=0:raise AssertionError('dirty Notes issued project switch POST')
   if p.locator('#notesEdit').input_value()!=txt or not p.locator('#notesSaveRetry').is_visible():raise AssertionError('blocked switch altered failed Notes draft/Retry')
   if 'Save or retry Notes' not in p.locator('#pmSwitchNote').inner_text():raise AssertionError('Notes guard feedback missing')
   p.screenshot(path=str(o/'notes-guard-blocked.png'),full_page=True)
   p.locator('#pmClose').click();p.locator('#projModal').wait_for(state='hidden')
   s['notes_fail']=False;p.locator('#notesSaveRetry').click();p.wait_for_function("()=>document.querySelector('#notesStatus').dataset.state==='saved'&&document.querySelector('#notesSaveRetry').hidden")
   p.reload(wait_until='domcontentloaded');ready(p);nav(p,'notes')
   if p.locator('#notesEdit').input_value()!=txt:raise AssertionError('successful Notes Retry was not exact after reload')
   r['cases'].append({'name':'rejected Notes PUT blocks Project POST; exact Retry saves and persists','status':'pass','events':events})
  finally:c.close();b.close()
 case('Notes guard and recovery',notes_guard_retry)
 def settings_and_reject():
  events=[];b,c,p,s=new_page(pw,'reject',events)
  try:
   nav(p,'settings');p.locator('#setSearch').fill('proxy');p.locator('#setNav button[data-sec="proxy"]').click();p.locator('#projBadge').click();p.locator('#projModal').wait_for(state='visible');p.locator('#pmList .pm-row[data-proj="generic-next"]').click();p.wait_for_function("()=>!document.querySelector('#pmClose').disabled&&document.querySelector('#pmSwitchNote').textContent.includes('failed')")
   if s['posts']!=1:raise AssertionError('settings search/section navigation blocked clean switch')
   if p.locator('#pmNew').is_disabled() or p.locator('#pmNewPath').is_disabled() or p.locator('#pmClose').is_disabled():raise AssertionError('rejected switch did not restore Project modal fields/Close')
   p.locator('#pmClose').click();nav(p,'settings');p.locator('#setSearch').fill('');p.locator('#setNav button[data-sec="session"]').click();p.locator('#setSessionHeaders').fill('X-Generic-Setting: recovery');p.locator('#projBadge').click();p.locator('#projModal').wait_for(state='visible');p.locator('#pmList .pm-row[data-proj="generic-next"]').click();p.wait_for_timeout(250)
   if s['posts']!=1:raise AssertionError('dirty Settings issued project switch POST')
   if 'Save your Settings changes' not in p.locator('#pmSwitchNote').inner_text():raise AssertionError('Settings draft guard feedback missing')
   r['cases'].append({'name':'Settings search/section excluded; dirty persisted field blocks POST; rejected switch restores modal','status':'pass','events':events})
  finally:c.close();b.close()
 case('Settings exclusions and rejected switch recovery',settings_and_reject)
 def delayed_accept():
  events=[];b,c,p,s=new_page(pw,'delayed',events)
  try:
   nav(p,'proxy');p.locator('#projBadge').click();p.locator('#projModal').wait_for(state='visible');p.locator('#pmList .pm-row[data-proj="generic-next"]').click();p.wait_for_function("()=>document.querySelector('#pmClose').disabled&&document.querySelector('#pmNew').disabled&&document.querySelector('#pmList .pm-row').disabled")
   p.keyboard.press('Escape');p.mouse.click(2,2);p.wait_for_timeout(120)
   if not p.locator('#projModal').is_visible() or not p.locator('#pmNew').is_disabled():raise AssertionError('Escape/backdrop dismissed or enabled delayed switch modal')
   p.screenshot(path=str(o/'delayed-switch-busy.png'),full_page=True)
   s['target']=True;p.wait_for_timeout(1800);ready(p)
   if s['polls']<2 or not any(e['event']=='project.get' and e['current']=='generic-next' for e in events):raise AssertionError('accepted switch did not wait for exact target identity before reload')
   r['cases'].append({'name':'clean delayed accepted switch blocks Escape/backdrop/inputs until target identity','status':'pass','events':events})
  finally:c.close();b.close()
 case('Delayed accepted Project switch modal lock',delayed_accept)
r['finished_unix']=time.time();r['summary']={'pass':sum(x['status']=='pass' for x in r['cases']),'fail':sum(x['status']=='fail' for x in r['cases']),'console_errors':len(r['console_errors'])};save();raise SystemExit(1 if r['summary']['fail'] or r['console_errors'] else 0)
