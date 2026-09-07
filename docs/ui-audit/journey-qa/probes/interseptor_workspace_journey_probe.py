#!/usr/bin/env python3
"""Bounded browser evidence for workspace/Notes journeys; disposable candidate only."""
import argparse, hashlib, json, time
from pathlib import Path
from playwright.sync_api import sync_playwright

p=argparse.ArgumentParser(); p.add_argument('--base',required=True); p.add_argument('--out',required=True); a=p.parse_args()
out=Path(a.out); out.mkdir(parents=True,exist_ok=True)
report={'base':a.base,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'started_unix':time.time(),'cases':[],'console_errors':[],'expected_mock_console_errors':[],'screenshots':[],'mocked_or_skipped':[]}
def put(): (out/'report.json').write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
def case(name,fn):
  try: fn(); report['cases'].append({'name':name,'status':'pass'})
  except Exception as e: report['cases'].append({'name':name,'status':'fail','reason':type(e).__name__+': '+str(e)})
  put()
def vis(page,s): page.wait_for_function("s=>{const e=document.querySelector(s);return !!e&&!e.hidden&&getComputedStyle(e).display!=='none'&&getComputedStyle(e).visibility!=='hidden'}",arg=s)
def ready(page):
  page.wait_for_selector('#tabs[aria-busy="false"]',state='attached'); page.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
  if page.locator('#setupModal').is_visible(): page.locator('#setupSkip').click()
def snap(page,name):
  f=out/(name+'.png'); page.screenshot(path=str(f),animations='disabled'); report['screenshots'].append({'name':name,'path':str(f),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()})
def native_selects(page):
  bad=page.evaluate("""()=>[...document.querySelectorAll('select')].filter(x=>{const s=getComputedStyle(x),r=x.getBoundingClientRect();return r.width>2&&r.height>2&&s.display!='none'&&s.visibility!='hidden'&&+s.opacity>0&&s.appearance!='none'}).map(x=>x.id)""")
  if bad: raise AssertionError('visible native controls: '+repr(bad))
with sync_playwright() as pw:
  browser=pw.chromium.launch(); ctx=browser.new_context(viewport={'width':1440,'height':900},reduced_motion='reduce'); page=ctx.new_page(); page.set_default_timeout(8000)
  page.on('pageerror',lambda e:report['console_errors'].append('pageerror: '+str(e)))
  page.on('console',lambda m:report['expected_mock_console_errors'].append('console: '+m.text) if m.type=='error' and '503' in m.text else (report['console_errors'].append('console: '+m.text) if m.type=='error' else None))
  page.goto(a.base,wait_until='domcontentloaded'); ready(page)
  case('desktop primary navigation and custom-control invariant',lambda:[(page.locator('#tab-'+x).click(),vis(page,'#panel-'+x+'.active'),native_selects(page)) for x in ('proxy','intercept','repeater','intruder','scanner','map','findings','notes','activity','settings')])
  def all_settings():
    page.locator('#tab-settings').click()
    for sec in ('proxy','tls','devices','scope','scanner','session','project','api'):
      page.locator('#setNav button[data-sec="'+sec+'"]').click(); vis(page,'.set-sec[data-sec="'+sec+'"]'); native_selects(page)
  case('desktop all 8 Settings sections via custom section controls',all_settings)
  def project_locked():
    page.locator('#setNav button[data-sec="project"]').click(); vis(page,'.set-sec[data-sec="project"]')
    page.locator('#projNew').fill('disposable-workspace')
    if page.locator('#projNewBtn').is_enabled() or page.locator('#projSwitchBtn').is_enabled(): raise AssertionError('managed candidate unexpectedly enables project switching')
  case('project create safely rejects while managed candidate locks switching',project_locked)
  def notes_persist():
    page.locator('#tab-notes').click(); vis(page,'#notesEdit')
    text='## Generic workspace note\n\nLocal fixture only.'; page.locator('#notesEdit').fill(text)
    page.locator('#notesSeg button[data-m="preview"]').click(); vis(page,'#notesPreview')
    page.reload(wait_until='domcontentloaded'); ready(page); page.locator('#tab-notes').click(); vis(page,'#notesEdit')
    if page.locator('#notesEdit').input_value()!=text: raise AssertionError('Notes did not persist after reload')
  case('Notes edit preview and reload persistence',notes_persist)
  def notes_race():
    page.locator('#tab-notes').click(); vis(page,'#notesEdit')
    held=[]
    def delay(route): held.append(route)
    page.route('**/api/notes',lambda route: delay(route) if route.request.method=='PUT' else route.continue_())
    page.locator('#notesEdit').fill('## Delayed save race\n\nGeneric local fixture.')
    page.locator('#notesSeg button[data-m="preview"]').click(); page.locator('#notesSeg button[data-m="edit"]').click()
    page.wait_for_timeout(150)
    if len(held)!=1: raise AssertionError('expected one held notes PUT, got '+str(len(held)))
    held[0].continue_(); page.wait_for_timeout(250)
    state=page.evaluate("""()=>({editPressed:document.querySelector('#notesSeg [data-m=edit]').getAttribute('aria-pressed'),editor:getComputedStyle(document.querySelector('#notesEdit')).display,preview:getComputedStyle(document.querySelector('#notesPreview')).display})""")
    snap(page,'notes-delayed-save-immediate-edit')
    if not (state['editPressed']=='true' and state['editor']!='none' and state['preview']=='none'): raise AssertionError('mode/UI mismatch after delayed save: '+repr(state))
    page.unroute('**/api/notes')
  case('BUG Notes delayed-save then immediate Edit preserves selected edit pane',notes_race)
  def failed_retry():
    page.locator('#tab-notes').click(); vis(page,'#notesEdit'); failures=[]
    page.route('**/api/notes',lambda route: (failures.append(1),route.fulfill(status=503,body='temporary generic failure')) if route.request.method=='PUT' else route.continue_())
    page.locator('#notesEdit').fill('## Retry fixture'); page.locator('#notesSeg button[data-m="preview"]').click()
    page.wait_for_timeout(200); vis(page,'#notesSaveRetry')
    page.unroute('**/api/notes'); page.locator('#notesSaveRetry').click(); page.wait_for_timeout(250)
    if page.locator('#notesStatus').get_attribute('data-state')=='error': raise AssertionError('Notes retry left save error')
  case('MOCK Notes failed-save exposes Retry and recovers on real retry',failed_retry)
  # Phone proves both custom picker trigger paths, no native select is visible.
  phone=browser.new_page(viewport={'width':390,'height':844}); phone.set_default_timeout(8000); phone.goto(a.base,wait_until='domcontentloaded'); ready(phone)
  def phone_controls():
    native_selects(phone); phone.locator('#mobileToolSelectUi').click(); menu=phone.locator('#'+phone.locator('#mobileToolSelectUi').get_attribute('aria-controls')); menu.get_by_role('option',name='Settings',exact=True).click(); vis(phone,'#panel-settings.active')
    phone.locator('#settingsSectionSelectUi').click(); phone.locator('#settingsSectionSelectUi').press('End'); phone.locator('#settingsSectionSelectUi').press('Enter'); vis(phone,'.set-sec[data-sec="api"]'); native_selects(phone); snap(phone,'phone-custom-tools-and-settings-picker')
  case('phone custom tools and Settings pickers; no visible native selects',phone_controls)
  report['mocked_or_skipped'] += ['Active scanner, Intruder payload execution, Authz probes, share/tunnel, OOB callback, API/MCP key generation, device/system operations: deliberately not run.', 'Activity-to-flow detail needs an MCP-originated activity event; no MCP service used.', 'Project create/select/rename and import/export are disabled by the required managed-candidate canSwitch lock, so their real mutation paths are blocked by the safety harness.']
  phone.close(); ctx.close(); browser.close()
report['finished_unix']=time.time(); report['summary']={'pass':sum(x['status']=='pass' for x in report['cases']),'fail':sum(x['status']=='fail' for x in report['cases']),'console_errors':len(report['console_errors'])}; put()
raise SystemExit(1 if report['summary']['fail'] or report['console_errors'] else 0)
