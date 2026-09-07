#!/usr/bin/env python3
"""Parameterized disposable-candidate recovery probe; no live-project defaults."""
import argparse, hashlib, json, time
from pathlib import Path
from playwright.sync_api import sync_playwright

def digest(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def put(p,x): p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(x,indent=2,sort_keys=True)+'\n')
def run_case(out,name,fn):
  try: fn();out['cases'][name]={'status':'pass'};print('PASS',name,flush=True)
  except Exception as e:out['cases'][name]={'status':'fail','reason':f'{type(e).__name__}: {e}'};print('FAIL',name,e,flush=True)
def ready(p):
  p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
  if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def select(p,fid,section='overview',edit=False):
  p.locator('#tab-findings').click();p.locator(f'#findList .find-row[data-id="{fid}"]').click();p.locator(f'#findDetail .find-section-nav [data-find-section="{section}"]').click()
  if edit and p.locator('#findToggleEdit').inner_text()=='Edit':p.locator('#findToggleEdit').click()
  if edit:p.locator('#findToggleEdit').wait_for()
def fail_patch(calls):
  def handler(route):
    if route.request.method=='PATCH':calls.append(route.request.post_data or '');route.fulfill(status=500,content_type='application/json',body='{"error":"fixture rejection"}')
    else:route.continue_()
  return handler
def main(a):
  out={'base':a.base,'fid':a.fid,'other_fid':a.other_fid,'probe_sha256':digest(__file__),'started_unix':time.time(),'cases':{},'console_errors':[],'page_errors':[],'screenshots':[]}
  with sync_playwright() as pw:
   b=pw.chromium.launch();p=b.new_page(viewport={'width':1440,'height':900});p.set_default_timeout(5000);p.on('console',lambda m:out['console_errors'].append(m.text) if m.type=='error' else None);p.on('pageerror',lambda e:out['page_errors'].append(str(e)));p.goto(a.base,wait_until='domcontentloaded');ready(p)
   def scalar():
    value='scalar-'+str(time.time_ns());calls=[];select(p,a.other_fid,'overview',True);p.route(f'**/api/findings/{a.other_fid}',fail_patch(calls));p.locator('#findSummary').fill(value);p.locator('#findImpact').click();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Save failed')");assert p.locator('#findSummary').input_value()==value and p.locator('#findSaveRetry').is_visible();p.locator('#findToggleEdit').click();assert p.locator('#findSummary').is_visible() and p.locator('#findSummary').input_value()==value and p.locator('#findSaveRetry').is_visible();p.screenshot(path=str(Path(a.out)/'scalar-failed.png'));p.unroute(f'**/api/findings/{a.other_fid}');p.locator('#findSaveRetry').click();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Saved')");saved=p.evaluate("async id=>(await (await fetch('/api/findings/'+id)).json()).summary",a.other_fid);assert saved==value and calls;p.locator('#findToggleEdit').click();p.locator('#findSummary').wait_for(state='detached')
   run_case(out,'scalar-failed-patch-retains-and-retries',scalar)
   def newer_scalar():
    old='old-'+str(time.time_ns());new='new-'+str(time.time_ns());calls=[];select(p,a.other_fid,'overview',True);p.route(f'**/api/findings/{a.other_fid}',fail_patch(calls));p.locator('#findSummary').fill(old);p.locator('#findImpact').click();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Save failed')");p.locator('#findSummary').fill(new);p.locator('#findImpact').click();p.wait_for_timeout(150);assert p.locator('#findSummary').input_value()==new;p.unroute(f'**/api/findings/{a.other_fid}');p.locator('#findSaveRetry').click();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Saved')");assert p.evaluate("async id=>(await (await fetch('/api/findings/'+id)).json()).summary",a.other_fid)==new and len(calls)>=1
   run_case(out,'newer-scalar-wins-after-failure',newer_scalar)
   def body_failure():
    marker='body-recovery-'+str(time.time_ns());calls=[];select(p,a.fid,'evidence',True);p.route(f'**/api/findings/{a.fid}',fail_patch(calls));p.locator('#findAddText').click();p.locator('#findBody .block-text').last.fill(marker);p.locator('#findBody .block-text').last.blur();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Save failed')",timeout=5000);expected=p.locator('#findBody .block-text').evaluate_all("els=>els.map(x=>x.value)");assert marker in expected and p.locator('#findSaveRetry').is_visible();p.screenshot(path=str(Path(a.out)/'body-failed.png'));select(p,a.other_fid,'overview',False);select(p,a.fid,'evidence',True);p.evaluate("async()=>{const m=await import('/js/findings.js');await m.loadFindings()}");p.wait_for_function("m=>[...document.querySelectorAll('#findBody .block-text')].some(x=>x.value===m)",arg=marker);assert p.locator('#findBody .block-text').evaluate_all("els=>els.map(x=>x.value)")==expected;p.unroute(f'**/api/findings/{a.fid}');p.locator('#findSaveRetry').click();p.wait_for_function("async v=>{const f=await (await fetch('/api/findings/'+v[0])).json();return (f.blocks||[]).filter(x=>x.type==='text').map(x=>x.md).join('\\n')===v[1].join('\\n')}",arg=[a.fid,expected]);p.screenshot(path=str(Path(a.out)/'body-retried.png'));assert calls
   run_case(out,'body-500-switch-back-evidence-loadfindings-retry-exact',body_failure)
   def inline_refresh_history_filters():
    select(p,2,'evidence',False);t=p.locator('.find-inline-toggle').first;t.click();p.locator('.find-inline-content').wait_for();p.locator('.find-inline-inspector [data-side="res"]').click();p.locator('.find-inline-content').evaluate("x=>x.scrollTop=10");p.evaluate("async()=>{const m=await import('/js/findings.js');await m.loadFindings()}");p.locator('.find-inline-toggle[aria-expanded="true"]').wait_for();assert p.locator('.find-inline-inspector [data-side="res"]').get_attribute('aria-pressed')=='true';select(p,1,'overview',False);p.locator('#findFilterSeverityUi').click();p.get_by_role('option',name='High',exact=True).click();p.locator('#findOutsideFilter').wait_for();p.go_back();p.wait_for_function("document.querySelector('#findDetail')?.textContent.includes('Fixture high evidence record')||document.querySelector('#findDetail')?.textContent.includes('Fixture critical access control')");p.go_forward();p.wait_for_timeout(100);p.screenshot(path=str(Path(a.out)/'inline-refresh-history-filter.png'))
   run_case(out,'inline-req-res-refresh-missing-filter-back-forward',inline_refresh_history_filters)
   def exact_deeplink_history():
    p.goto(a.base+'/#finding-2/evidence',wait_until='domcontentloaded');ready(p);p.wait_for_function("location.hash==='#finding-2/evidence'");assert p.locator('#findDetail .find-id-badge').inner_text()=='FINDING #2' and p.locator('#findDetail .find-section-nav [data-find-section=\"evidence\"]').get_attribute('aria-current')=='page';select(p,1,'review',False);assert p.locator('#findDetail .find-id-badge').inner_text()=='FINDING #1';p.go_back();p.wait_for_function("location.hash==='#finding-1/overview'");p.go_back();p.wait_for_function("location.hash==='#finding-2/evidence'");assert p.locator('#findDetail .find-id-badge').inner_text()=='FINDING #2' and p.locator('#findDetail .find-section-nav [data-find-section=\"evidence\"]').get_attribute('aria-current')=='page';p.go_forward();p.wait_for_function("location.hash==='#finding-1/overview'");p.go_forward();p.wait_for_function("location.hash==='#finding-1/review'");assert p.locator('#findDetail .find-id-badge').inner_text()=='FINDING #1' and p.locator('#findDetail .find-section-nav [data-find-section=\"review\"]').get_attribute('aria-current')=='page'
   run_case(out,'fresh-deeplink-and-exact-back-forward-identity',exact_deeplink_history)
   b.close()
  out['finished_unix']=time.time();out['failures']=[k for k,v in out['cases'].items() if v['status']=='fail'];out['expected_console_errors']=[x for x in out['console_errors'] if 'status of 500' in x];out['unhandled_console_errors']=[x for x in out['console_errors'] if x not in out['expected_console_errors']];put(Path(a.out)/'report.json',out);return 1 if out['failures'] or out['unhandled_console_errors'] or out['page_errors'] else 0
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('--base',required=True);ap.add_argument('--out',required=True);ap.add_argument('--fid',type=int,default=3);ap.add_argument('--other-fid',type=int,default=4);args=ap.parse_args();raise SystemExit(main(args))
