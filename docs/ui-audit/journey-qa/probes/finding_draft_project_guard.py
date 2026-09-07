#!/usr/bin/env python3
"""Current-source browser proof: an unselected failed Finding draft blocks project switch POST."""
import argparse, hashlib, json, os, sys, time
from pathlib import Path
from playwright.sync_api import sync_playwright
REPO=Path(os.environ.get('INTERSEPTOR_REPO',Path.cwd())).resolve(); OUT=None
def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def ready(p):
 p.wait_for_selector('#tabs[aria-busy="false"]',state='attached');p.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
 if p.locator('#setupModal').is_visible():p.locator('#setupSkip').click()
def main(a):
 global OUT; OUT=Path(a.out);OUT.parent.mkdir(parents=True,exist_ok=True);r={'base':a.base,'runtime_digest':a.digest,'probe_sha256':sha(__file__),'started_unix':time.time(),'cases':[]}
 def case(n,fn):
  try: v=fn();r['cases'].append({'name':n,'status':'pass',**(v or {})})
  except Exception as e:r['cases'].append({'name':n,'status':'fail','error':f'{type(e).__name__}: {e}'})
 with sync_playwright() as pw:
  b=pw.chromium.launch();c=b.new_context(viewport={'width':390,'height':844});p=c.new_page();p.set_default_timeout(10000);p.goto(a.base,wait_until='domcontentloaded');ready(p)
  fid,other=1,2; marker='unselected-failed-draft-'+str(time.time_ns());patches=[];switch_posts=[]
  def make_unselected_failed_draft():
   p.locator('#mobileToolSelectUi').click();p.get_by_role('option',name='Findings',exact=True).last.click();p.locator(f'#findList .find-row[data-id="{fid}"]').click()
   if p.locator('#findToggleEdit').inner_text()=='Edit':p.locator('#findToggleEdit').click()
   p.route(f'**/api/findings/{fid}',lambda route:(patches.append(route.request.post_data or ''),route.fulfill(status=500,content_type='application/json',body='{"error":"fixture draft rejection"}')) if route.request.method=='PATCH' else route.continue_())
   p.locator('#findSummary').fill(marker);p.locator('#findSummary').blur();p.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Save failed')")
   p.locator('#findBackToList').click();p.locator(f'#findList .find-row[data-id="{other}"]').click();p.wait_for_function("id=>document.querySelector('#findDetail')?.dataset.findingId===String(id)",arg=other)
  case('failed-finding-draft-can-be-unselected',make_unselected_failed_draft)
  def blocked_switch():
   def project(route):
    if route.request.method=='POST':switch_posts.append(route.request.post_data or '');route.fulfill(status=200,content_type='application/json',body='{"switching":"other"}')
    else:route.fulfill(status=200,content_type='application/json',body='{"current":"current","dir":"/tmp/current","canSwitch":true,"projects":[{"name":"current","path":"/tmp/current"},{"name":"other","path":"/tmp/other"}]}')
   p.route('**/api/project**',project);p.locator('#mobileProjectBtn').click();p.locator('#projModal').wait_for();p.locator('#pmList .pm-row').click();p.wait_for_function("document.querySelector('#pmSwitchNote')?.textContent.includes('Save or retry Findings')")
   assert not switch_posts, switch_posts
   return {'switch_post_count':len(switch_posts),'guard_text':p.locator('#pmSwitchNote').inner_text()}
  case('unselected-failed-finding-draft-blocks-mocked-project-switch-zero-post',blocked_switch)
  def revisit_retry():
   p.locator('#pmClose').click();p.locator('#findBackToList').click();p.locator(f'#findList .find-row[data-id="{fid}"]').click();p.locator('#findSaveRetry').wait_for();assert p.locator('#findSummary').input_value()==marker
   p.unroute(f'**/api/findings/{fid}');p.locator('#findSaveRetry').click();p.wait_for_function("async v=>(await (await fetch('/api/findings/'+v[0])).json()).summary===v[1]",arg=[fid,marker]);p.unroute('**/api/project**')
   return {'retry_patch_failures':len(patches)}
  case('unselected-failed-finding-draft-revisit-and-retry-persists',revisit_retry)
  c.close();b.close()
 r['finished_unix']=time.time();r['failures']=[x['name'] for x in r['cases'] if x['status']=='fail'];OUT.write_text(json.dumps(r,indent=2)+'\n');print(json.dumps({'out':str(OUT),'cases':len(r['cases']),'failures':r['failures']}));return 1 if r['failures'] else 0
if __name__=='__main__':
 q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--digest',required=True);q.add_argument('--out',required=True);z=q.parse_args();raise SystemExit(main(z))
