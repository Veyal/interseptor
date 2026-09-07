#!/usr/bin/env python3
"""UI-only secondary-surface audit. Requires a local managed candidate."""
import argparse, hashlib, json, time
from pathlib import Path
from playwright.sync_api import sync_playwright

parser=argparse.ArgumentParser()
parser.add_argument('--base', required=True)
parser.add_argument('--out', required=True)
parser.add_argument('--session')
args=parser.parse_args()
out=Path(args.out); out.mkdir(parents=True,exist_ok=True)
me=Path(__file__); sha=hashlib.sha256(me.read_bytes()).hexdigest()
report={'base':args.base,'session':args.session,'probe':str(me),'probe_sha256':sha,
 'started_unix':time.time(),'cases':[],'screenshots':[], 'console_errors':[],
 'untested':['Repeater Send; Intruder Start; scanner runs and custom-check test/save; codec test/save; API-key creation/reveal; allowlist/share/REST/MCP mutations; device/auth/server configuration. These are excluded UI-only operational actions.']}

def add_case(name, fn):
  try:
    fn(); report['cases'].append({'name':name,'status':'pass'})
  except Exception as e:
    report['cases'].append({'name':name,'status':'fail','reason':str(e)})
  (out/'report.partial.json').write_text(json.dumps(report,indent=2)+'\n')

def visible(page, selector):
  page.wait_for_function("s=>{const e=document.querySelector(s); return !!e && !e.hidden && getComputedStyle(e).display!=='none' && getComputedStyle(e).visibility!=='hidden'}", arg=selector, timeout=4000)

def settle(page):
  page.wait_for_timeout(120)
  page.evaluate("""async()=>{const a=document.getAnimations?document.getAnimations():[];await Promise.race([Promise.all(a.map(x=>x.finished.catch(()=>{}))),new Promise(r=>setTimeout(r,400))]);await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));}""")

def snap(page,name):
  settle(page); p=out/(name+'.png'); page.screenshot(path=str(p),animations='disabled')
  report['screenshots'].append({'name':name,'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})

def nav(page,name,phone):
  if phone:
    page.locator('#mobileToolSelectUi').click()
    page.locator('[role="option"]',has_text=name.title()).last.click()
  else:
    page.locator('#tab-'+name).click()
  visible(page,'#panel-'+name+'.active')

def active_button(page, selector, value, attr):
  page.locator(selector+f' button[{attr}="{value}"]').click()
  page.wait_for_function("([s,v,a])=>document.querySelector(s+' button['+a+'=\"'+v+'\"]')?.getAttribute('aria-pressed')==='true'", arg=[selector,value,attr],timeout=4000)

def select_ui(page, trigger, value):
  control=page.locator(trigger)
  menu=control.get_attribute('aria-controls')
  control.click()
  opt=page.locator('#'+menu+' .ui-select-opt[data-value="'+value+'"]').last
  opt.click()

with sync_playwright() as pw:
  browser=pw.chromium.launch()
  for label, viewport in [('desktop',{'width':1440,'height':900}),('phone',{'width':390,'height':844})]:
    page=browser.new_page(viewport=viewport)
    page.set_default_timeout(5000)
    page.on('pageerror',lambda e: report['console_errors'].append({'viewport':label,'type':'pageerror','message':str(e)}))
    page.on('console',lambda m: report['console_errors'].append({'viewport':label,'type':'console','message':m.text}) if m.type=='error' else None)
    page.goto(args.base,wait_until='domcontentloaded'); page.wait_for_timeout(700)
    if page.locator('#setupModal').is_visible():
      page.locator('#setupSkip').click()
    # Map modes and client-side path/host search scope. No map refresh or discovery actions.
    def map_cases():
      nav(page,'map',label=='phone')
      page.locator('#mapSearch').fill('example.com/fixture')
      select_ui(page,'#mapSearchScopeUi','path')
      for v,target in [('tree','#mapTree'),('table','#mapTable'),('graph','#mapGraphWrap'),('params','#mapParams')]:
        active_button(page,'#mapViewSeg',v,'data-v'); visible(page,target)
      select_ui(page,'#mapSearchScopeUi','all')
      if page.locator('#mapSearchScope').input_value()!='all': raise AssertionError('Map path/host search scope did not update')
    add_case(label+' Map Tree/Table/Graph/Params plus path-host search',map_cases)
    if label=='desktop': snap(page,'desktop-map-secondary')

    def repeater_cases():
      nav(page,'repeater',label=='phone')
      for v in ['raw','pretty','decoded']:
        active_button(page,'#repResSeg',v,'data-view')
      page.locator('#repHistToggle').click(); visible(page,'#repHistory')
      if page.locator('#repHistToggle').get_attribute('aria-expanded')!='true': raise AssertionError('Repeater History aria-expanded false')
      page.locator('#repHistToggle').click()
    add_case(label+' Repeater response Raw/Pretty/Decoded and History drawer',repeater_cases)

    def intruder_cases():
      nav(page,'intruder',label=='phone')
      for mode in ['sniper','__lists__','repeat']:
        active_button(page,'#intrType',mode,'data-t')
      visible(page,'#intrRepeatWrap')
      for sel in ['#intrThreads','#intrDelay','#intrRepeat']:
        if not page.locator(sel).is_enabled(): raise AssertionError(sel+' disabled')
      page.locator('#intrHistToggle').click(); visible(page,'#intrHistory')
      if page.locator('#intrHistToggle').get_attribute('aria-expanded')!='true': raise AssertionError('Intruder History aria-expanded false')
      page.locator('#intrHistToggle').click()
    add_case(label+' Intruder Lists/numeric panels/Race repeat and History drawer',intruder_cases)
    if label=='phone': snap(page,'phone-intruder-secondary')

    def notes_cases():
      nav(page,'notes',label=='phone')
      active_button(page,'#notesSeg','preview','data-m'); visible(page,'#notesPreview')
      active_button(page,'#notesSeg','edit','data-m'); visible(page,'#notesEdit')
    add_case(label+' Notes Edit/Preview',notes_cases)

    def api_cases():
      nav(page,'settings',label=='phone')
      page.wait_for_timeout(300)
      if label=='phone': select_ui(page,'#settingsSectionSelectUi','api')
      else: page.locator('#setNav button[data-sec="api"]').click()
      visible(page,'.set-sec[data-sec="api"]')
      for tab,pane in [('keys','#apiKeys'),('allowlist','#apiAllowlist'),('share','#apiShare'),('rest','#apiRest'),('mcp','#apiMcp')]:
        active_button(page,'#apiSub',tab,'data-s'); visible(page,pane)
    add_case(label+' API Settings Keys/Allowlist/Share/REST/MCP tabs',api_cases)

    def palette_case():
      page.locator('#cmdkBtn').click(); visible(page,'#cmdkInput')
      page.locator('#cmdkInput').fill('Go to Notes')
      page.locator('#cmdkOpt0').click(); visible(page,'#panel-notes.active')
    add_case(label+' Command palette search and safe navigation selection',palette_case)

    # Safe modal tabs only; no Run/Test/Save controls touched.
    def checks_case():
      nav(page,'scanner',label=='phone'); page.locator('#checksBtn').click(); visible(page,'#checksModal')
      page.locator('#checkModeDocs').click(); visible(page,'#checkPaneDocs')
      page.locator('#checkModeCode').click(); visible(page,'#checkPaneCode')
      page.locator('#checksClose').click(); page.wait_for_function("!document.querySelector('#checksModal').offsetParent")
    add_case(label+' Custom Checks Code/Docs navigation',checks_case)
    def codecs_case():
      nav(page,'scanner',label=='phone'); page.locator('#codecsBtn').click(); visible(page,'#codecsModal')
      page.locator('#codecModeDocs').click(); visible(page,'#codecPaneDocs')
      page.locator('#codecModeCode').click(); visible(page,'#codecPaneCode')
      page.locator('#codecsClose').click(); page.wait_for_function("!document.querySelector('#codecsModal').offsetParent")
    add_case(label+' Message Codecs Code/Docs navigation',codecs_case)
    page.close()
  browser.close()
report['finished_unix']=time.time()
report['summary']={'pass':sum(c['status']=='pass' for c in report['cases']),'fail':sum(c['status']=='fail' for c in report['cases']),'console_errors':len(report['console_errors'])}
Path(out/'report.json').write_text(json.dumps(report,indent=2)+'\n')
if report['summary']['fail'] or report['console_errors']: raise SystemExit(1)
