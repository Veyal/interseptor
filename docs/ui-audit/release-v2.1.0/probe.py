#!/usr/bin/env python3
"""Disposable, loopback-only browser verification for frozen findings improvements."""
from __future__ import annotations
import base64, hashlib, importlib.util, json, os, sys, time, traceback, tempfile, secrets, shutil, subprocess
from pathlib import Path
from urllib.request import Request, urlopen
from playwright.sync_api import sync_playwright

ROOT=Path(os.environ.get('QA_REPO',Path.cwd())).resolve()
OUT=Path(os.environ.get('QA_OUT','/tmp/interseptor-findings-improvements-qa-'+str(int(time.time()))))
ENGINES=tuple(os.environ.get('QA_ENGINES','chromium,firefox,webkit').split(','))
FROZEN='7f342c0709e2ba0620072b020245fd61c744ac39045ec2bdd8acac824e52edf0'
BINARY='1db1e11eb1881b4d6295749f459bf2737938d217ef459e4aaf970bf288a593d7'
PNG=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLMeAAAAABJRU5ErkJggg==')
spec=importlib.util.spec_from_file_location('audit',ROOT/'scripts/ui_browser_audit.py'); audit=importlib.util.module_from_spec(spec); sys.modules['audit']=audit; spec.loader.exec_module(audit)
def req(base,path,method='GET',body=None):
    data=None if body is None else json.dumps(body).encode(); h={'X-Interseptor-CSRF':'1','Content-Type':'application/json'} if (data or method not in ('GET','HEAD')) else {}
    try:
        with urlopen(Request(base+path,data=data,method=method,headers=h),timeout=12) as r:
            return r.status,json.loads(r.read().decode() or '{}')
    except Exception as e:
        raw=e.read().decode() if hasattr(e,'read') else str(e)
        try: val=json.loads(raw)
        except Exception: val={'error':raw}
        return getattr(e,'code',0),val
def ok(v,msg):
    if not v: raise AssertionError(msg)
def wait_api(base,path,pred,msg):
    end=time.monotonic()+12; latest=None
    while time.monotonic()<end:
        st,latest=req(base,path)
        if st==200 and pred(latest): return latest
        time.sleep(.15)
    raise AssertionError(msg+': '+repr(latest))
def shot(page,engine,label,shots):
    p=OUT/'screenshots'/f'{engine}-{label}.png'; p.parent.mkdir(parents=True,exist_ok=True); page.screenshot(path=str(p),full_page=True)
    shots.append({'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
def flush(report):
    (OUT/'partial.json').write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
def seed(base,proxy):
    fixture,thread=audit.start_fixture(); target=f'http://127.0.0.1:{fixture.server_port}'
    for suffix in ('/session-a','/session-b'):
        status,_=audit.proxy_request(proxy,target+suffix,'POST',b'{"generic":true}')
        ok(status==200,'loopback fixture did not respond')
    end=time.monotonic()+10; flows=[]
    while time.monotonic()<end:
        _,data=req(base,'/api/flows?limit=20'); flows=(data or {}).get('flows',[])
        if len(flows)>=2: break
        time.sleep(.1)
    ok(len(flows)>=2,'candidate did not capture two loopback flows'); ids=[flows[0]['id'],flows[1]['id']]
    f={'title':'QA findings improvements','severity':'Low','status':'open','summary':'Generic loopback proof only.','targets':[{'url':'https://example.com/records/1','methods':['POST'],'role':'anonymous','flow_ids':[ids[0]]},{'url':'https://example.com/records/2','methods':['POST'],'role':'anonymous','flow_ids':[ids[1]]}], 'proofReview':{'execution':'not_executed','reason':'Bounded loopback fixture; impact not executed.'}}
    st,f=req(base,'/api/findings','POST',f); ok(st==200,'seed finding failed '+repr(f))
    for id in ids:
        st,_=req(base,f"/api/findings/{f['id']}/flows",'POST',{'flowId':id,'role':'result','proof':'Generic loopback result.'})
        ok(st==200,'attach flow failed')
    return f['id'],ids,fixture
def prepare_local_binary():
    """Run the freshly built local-version binary with retained loopback FDs."""
    paths=audit.runtime_source_paths(ROOT); source=audit.source_identity(ROOT,paths,audit.source_base_commit(ROOT))
    ok(source['runtime_sha256']==FROZEN,'runtime digest mismatch '+repr(source))
    root=Path(tempfile.mkdtemp(prefix='interseptor-ui-audit-')); project='ui-audit-'+secrets.token_hex(6)
    (root/'projects'/project).mkdir(parents=True)
    (root/audit.AUDIT_SENTINEL_NAME).write_text(f"interseptor-ui-audit\nproject={project}\n")
    binary=root/'interseptor-audit'; shutil.copy2(ROOT/'interseptor',binary); ok(hashlib.sha256(binary.read_bytes()).hexdigest()==BINARY,'repo binary digest mismatch')
    version=subprocess.run([str(binary),'version'],capture_output=True,text=True,check=True).stdout.strip()
    ok('2.0.10-local' in version,'managed binary is not local 2.0.10: '+version)
    reservations=[audit.reserve_loopback_listener(),audit.reserve_loopback_listener()]; control,proxy=reservations
    env=audit.managed_candidate_env(control.fileno(),proxy.fileno())
    process=subprocess.Popen([str(binary),'--data-dir',str(root),'--project',project,'--control-port',str(control.getsockname()[1]),'--proxy-port',str(proxy.getsockname()[1])],cwd=ROOT,env=env,pass_fds=(control.fileno(),proxy.fileno()),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    base='http://127.0.0.1:'+str(control.getsockname()[1]); end=time.monotonic()+20
    while time.monotonic()<end:
        if process.poll() is not None: raise RuntimeError('candidate exited')
        try:
            _,v=req(base,'/api/version'); return process,root,project,base,('127.0.0.1',proxy.getsockname()[1]),source,reservations,version,hashlib.sha256(binary.read_bytes()).hexdigest()
        except Exception: time.sleep(.1)
    raise RuntimeError('candidate did not become ready')
def original_run(pw,engine):
    report={'engine':engine,'cases':{},'failures':[],'screenshots':[],'external_requests':[],'runtime_expected':FROZEN}; flush(report)
    browser=getattr(pw,engine).launch(); context=browser.new_context(viewport={'width':1440,'height':900},accept_downloads=True); page=context.new_page()
    process=root=project=base=proxy=source=res=fixture=None
    def case(name,fn):
        try: fn(); report['cases'][name]='pass'
        except Exception as e:
            report['cases'][name]='fail'; report['failures'].append(name+': '+type(e).__name__+': '+str(e));
            try: shot(page,engine,'failure-'+name.replace('/','-'),report['screenshots'])
            except Exception: pass
        flush(report)
    try:
        process,root,project,base,proxy,source,res,binary_version,binary_sha256=prepare_local_binary(); report['binary_version']=binary_version; report['binary_sha256']=binary_sha256
        ok(source.get('runtime_sha256')==FROZEN,'runtime digest mismatch '+repr(source))
        page.evaluate("""() => { window.__qaTrace=[]; for (const type of ['pointerdown','click','focusout']) document.addEventListener(type,e=>window.__qaTrace.push({type,target:e.target.id||e.target.className||e.target.tagName,href:e.target.getAttribute?.('href')||'',active:document.activeElement?.id||document.activeElement?.className||''}),true); }""")
        page.on('request',lambda r: report['external_requests'].append(r.url) if not r.url.startswith(base) and not r.url.startswith('data:') else None)
        fid,ids,fixture=seed(base,proxy)
        page.goto(base,wait_until='domcontentloaded'); page.evaluate("""() => { window.__qaTrace=[]; for (const type of ['pointerdown','click','focusout']) document.addEventListener(type,e=>window.__qaTrace.push({type,target:e.target.id||e.target.className||e.target.tagName,href:e.target.getAttribute?.('href')||'',active:document.activeElement?.id||document.activeElement?.className||''}),true); }"""); page.locator('.tab[data-tab="findings"]').click(); page.locator('#findList .find-row').filter(has_text='QA findings improvements').click(); page.locator('#findToggleEdit').click(); page.wait_for_selector('#findTargetCleanup')
        shot(page,engine,'light-desktop-overview',report['screenshots'])
        def revisions():
            page.locator('#findRename').click(); page.locator('#promptInput').fill('QA findings restored'); page.locator('#promptOk').click()
            wait_api(base,f'/api/findings/{fid}',lambda f:f['title']=='QA findings restored','rename persistence')
            _,history=req(base,f'/api/finding-revisions/{fid}'); chosen=None
            for item in history['revisions']:
                _,detail=req(base,f"/api/finding-revisions/{fid}/{item['id']}")
                if any(d['field']=='title' and d.get('before')=='QA findings improvements' and d.get('after')=='QA findings restored' for d in detail.get('diff',[])): chosen=item['id']; break
            ok(chosen,'could not identify title revision')
            _,revision_detail=req(base,f'/api/finding-revisions/{fid}/{chosen}'); snapshot=revision_detail['revision']['snapshot']
            page.locator('.find-section-nav [data-find-section="review"]').click(); page.locator('.find-revisions summary').click(); page.locator(f'[data-revision="{chosen}"] summary').click(); page.locator(f'[data-revision="{chosen}"] [data-restore]').wait_for(); shot(page,engine,'light-desktop-revision-diff',report['screenshots']); page.locator(f'[data-revision="{chosen}"] [data-restore]').click(); page.locator('#confirmOk').click()
            wait_api(base,f'/api/findings/{fid}',lambda f:f['title']==snapshot['title'],'revision restore selected snapshot')
        case('revisions-diff-and-restore',revisions); shot(page,engine,'light-desktop-revisions',report['screenshots'])
        def cleanup():
            page.locator('.find-section-nav [data-find-section="overview"]').click(); page.locator('#findTargetCleanup').click(); page.wait_for_selector('.find-target-cleanup [data-apply]'); shot(page,engine,'light-desktop-cleanup-preview',report['screenshots'])
            ok('duplicate' in page.locator('.find-target-cleanup').inner_text().lower(),'cleanup preview missing duplicate count'); page.locator('.find-target-cleanup [data-cancel]').click()
            st,f=req(base,f'/api/findings/{fid}'); before=len(f['targets']); page.locator('#findTargetCleanup').click(); page.wait_for_selector('.find-target-cleanup [data-template]'); toggles=page.locator('.find-target-cleanup [data-template]'); ok(toggles.count()==2,'expected two numeric template suggestions'); toggles.nth(0).click(); toggles.nth(1).click(); page.locator('.find-target-cleanup [data-apply]').click(); wait_api(base,f'/api/findings/{fid}',lambda f:len(f['targets'])<before,'cleanup template apply'); merged=wait_api(base,f'/api/findings/{fid}',lambda f:len(f['targets'])==1 and set(f['targets'][0].get('flow_ids',[]))==set(ids),'template cleanup did not merge both evidence references')
        case('target-cleanup-cancel-and-apply',cleanup)
        def image_claim_export_cvss():
            page.locator('.find-section-nav [data-find-section="evidence"]').click()
            with page.expect_file_chooser() as chooser: page.locator('#findAddImage').click()
            chooser.value.set_files({'name':'browser.png','mimeType':'image/png','buffer':PNG}); page.wait_for_timeout(700); page.wait_for_selector('.find-block-source',state='attached')
            native=page.locator('.find-block-source'); trigger_id=native.evaluate("s => { const t=s._uiSelect?.trigger; if(!t)throw new Error('missing custom source trigger'); return t.id; }"); source=page.locator('#'+trigger_id); source.scroll_into_view_if_needed(); page.wait_for_timeout(350); ok(source.is_visible(),'custom source trigger is hidden'); source.click(); page.wait_for_timeout(100); ok(source.get_attribute('aria-expanded')=='true','image-source menu closed after settled trigger click'); page.locator('[role="option"][data-value="browser_screenshot"]:visible').click(); page.wait_for_timeout(450)
            text=page.locator('#findBody').inner_text(); ok('Ingested:' in text and 'Browser capture' in text,'provenance/classification absent')
            page.wait_for_timeout(900); page.locator('.find-section-nav [data-find-section="review"]').click(); page.wait_for_timeout(350); report['review_click_trace']=page.evaluate("() => ({trace:window.__qaTrace.slice(-30),active:[...document.querySelectorAll('.find-workspace-panel')].filter(x=>getComputedStyle(x).display!=='none').map(x=>x.dataset.findPanel),claim:document.querySelector('[data-capability=state_change]')?.outerHTML.slice(0,400)})"); claim=page.locator('[data-capability="state_change"]'); claim.locator('summary').click(); page.locator('[data-capability="state_change"] [data-claim-note]').wait_for(state='visible'); page.locator('[data-capability="state_change"] [data-claim-note]').fill('Generic state change declaration'); page.locator('[data-capability="state_change"] [data-claim-note]').blur(); page.wait_for_timeout(300); shot(page,engine,'light-desktop-claim-controls',report['screenshots'])
            # Missing before/control evidence must remain a declared review gap and final export must be rejected.
            page.locator('#findExportOpen').click(); page.locator('#findExport').click(); page.wait_for_selector('#findExportChecks:not([hidden])'); ok(page.locator('#findExportChecks').inner_text(),'final gate has no review links')
            page.locator('#findExportModeUi').click(); page.locator('[role="option"][data-value="draft"]').click()
            with page.expect_download() as d: page.locator('#findExport').click()
            path=OUT/'downloads'/f'{engine}-draft.md'; path.parent.mkdir(parents=True,exist_ok=True); d.value.save_as(str(path)); ok(path.stat().st_size>0,'draft export empty')
            page.locator('.cvss-calculator summary').click(); page.locator('#findCvss').fill('CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N'); page.locator('[data-cvss-preview]').click(); page.wait_for_selector('[data-cvss-apply]:not([disabled])'); shot(page,engine,'light-desktop-cvss-calculator',report['screenshots'])
            st,before=req(base,f'/api/findings/{fid}'); page.locator('[data-cvss-apply]').click(); wait_api(base,f'/api/findings/{fid}',lambda f:f.get('cvss','').startswith('CVSS:4.0') and f.get('severity')!='Low','cvss apply')
        case('provenance-claims-final-gate-draft-and-cvss-apply',image_claim_export_cvss); shot(page,engine,'light-desktop-review',report['screenshots'])
        def deleted():
            st,d=req(base,'/api/findings','POST',{'title':'QA deleted sibling','severity':'Info'}); ok(st==200,'deleted sibling seed '+repr(d)); did=d['id']; st,deleted=req(base,f'/api/findings/{did}','DELETE'); ok(st in (200,204),'delete failed '+repr(deleted))
            page.locator('#findDeletedOpen').click(); page.wait_for_selector('#findDeletedModal'); shot(page,engine,'light-desktop-deleted-dialog',report['screenshots']); page.locator('#findDeletedModal').get_by_text('QA deleted sibling').locator('xpath=ancestor::article').get_by_role('button',name='Restore').click(); page.locator('#confirmOk').click(); wait_api(base,f'/api/findings/{did}',lambda f:f['title']=='QA deleted sibling','deleted restore')
        case('deleted-list-and-restore',deleted)
        def session():
            page.locator('.tab[data-tab="proxy"]').click(); page.wait_for_selector('#rows .trow'); rows=page.locator('#rows .trow'); rows.nth(0).click(); rows.nth(1).click(modifiers=['Meta']); rows.nth(0).click(button='right'); page.get_by_text('Inspect session timeline',exact=True).click(); page.wait_for_selector('#sessionInspectModal',state='visible'); page.wait_for_selector('#sessionInspectTimeline:not([hidden])'); text=page.locator('#sessionInspectModal').inner_text().lower(); ok('passive observations only' in text and 'unknown unless directly captured' in text,'passive session safety text missing'); ok(page.locator('[data-session-role="unassigned"][aria-pressed="true"]').count()==2,'initial roles must all be Unassigned'); shot(page,engine,'light-desktop-session-timeline',report['screenshots']); page.locator('#sessionInspectRefresh').click(); page.locator('#sessionInspectClose').click()
        case('passive-session-inspection',session); shot(page,engine,'light-desktop-session',report['screenshots'])
        def mobile_theme():
            page.set_viewport_size({'width':390,'height':844}); page.locator('#themeToggle').click(); page.wait_for_timeout(300); ok(page.evaluate('document.documentElement.dataset.theme')!='light','theme did not enter dark'); ok(page.evaluate('document.documentElement.scrollWidth<=innerWidth'),'390 document overflow'); shot(page,engine,'dark-390',report['screenshots']); page.locator('#themeToggle').click(); page.wait_for_timeout(300); shot(page,engine,'light-390',report['screenshots'])
        case('light-dark-mobile-reachability',mobile_theme)
        case('no-visible-native-controls',lambda: ok(page.locator('select:visible').count()==0,'native select visible'))
        ok(not report['external_requests'],'unexpected external requests '+repr(report['external_requests']))
    except Exception as e: report['cases']['fatal']='fail'; report['failures'].append(traceback.format_exc())
    finally:
        if fixture: fixture.shutdown(); fixture.server_close()
        if process: audit.cleanup_managed_audit(process,root,project,res)
        context.close(); browser.close(); flush(report)
    return report
def main():
    if OUT.exists(): raise SystemExit('QA_OUT already exists')
    OUT.mkdir(mode=0o700); probe=Path(__file__); reports=[]
    with sync_playwright() as pw:
        for engine in ENGINES: reports.append(run(pw,engine))
    data={'frozen_runtime_sha256':FROZEN,'probe':str(probe),'probe_sha256':hashlib.sha256(probe.read_bytes()).hexdigest(),'reports':reports}
    (OUT/'report.json').write_text(json.dumps(data,indent=2,sort_keys=True)+'\n'); print(OUT/'report.json')
    return 1 if any(r['failures'] for r in reports) else 0
# Entry point is intentionally below the added probe.

# This extension deliberately reuses the frozen fixture/runtime helpers above.
# It adds only evidence that the frozen probe did not make discriminating.
def settled(page):
    return page.evaluate("""async () => {
      const samples=[];
      for(let i=0;i<8;i++){
        await new Promise(requestAnimationFrame);
        const css=getComputedStyle(document.documentElement);
        samples.push(['--bg','--fg','--accent'].map(k=>css.getPropertyValue(k).trim()).join('|'));
      }
      return {stable:samples.slice(-4).every(x=>x===samples.at(-1)), sample:samples.at(-1),
        scrollWidth:document.documentElement.scrollWidth, width:innerWidth};
    }""")

def visual_shot(page, engine, label, report, focus):
    focus.scroll_into_view_if_needed(); focus.wait_for(state='visible')
    settled_state=focus.evaluate("""async el => {
      for(let i=0;i<240;i++){
        await new Promise(requestAnimationFrame);
        const modal=el.closest('.modal-shell') || el;
        const style=getComputedStyle(modal), running=modal.getAnimations({subtree:true}).some(a=>a.playState==='running');
        const toast=[...document.querySelectorAll('.toast-item.show')].some(t=>getComputedStyle(t).opacity!=='0');
        if(!running&&!toast) return {opacity:style.opacity,transform:style.transform,stable:true};
      }
      const style=getComputedStyle(el.closest('.modal-shell') || el);
      return {opacity:style.opacity,transform:style.transform,stable:false};
    }""")
    ok(settled_state['stable'] and float(settled_state['opacity'])>=.99,'surface/transient toast did not settle before screenshot')
    p=OUT/'screenshots'/f'{engine}-{label}.png'; p.parent.mkdir(parents=True,exist_ok=True)
    page.screenshot(path=str(p))
    report['screenshots'].append({'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'settled':settled_state})

def visual_case(report, name, fn):
    try:
        fn(); report['cases'][name]='pass'
    except Exception as e:
        report['cases'][name]='fail'; report['failures'].append(f'{name}: {type(e).__name__}: {e}')

def custom_trigger(page, selector):
    native=page.locator(selector)
    trigger_id=native.evaluate("s=>{const t=s._uiSelect?.trigger;if(!t)throw new Error('missing _uiSelect.trigger');return t.id}")
    trigger=page.locator('#'+trigger_id); trigger.scroll_into_view_if_needed(); ok(trigger.is_visible(),selector+' custom trigger is not visible')
    return trigger

def rename_ui(page, base, fid, title):
    page.locator('#findRename').click(); page.locator('#promptInput').fill(title); page.locator('#promptOk').click()
    return wait_api(base,f'/api/findings/{fid}',lambda f:f['title']==title,'UI rename did not persist')

def find_revision(base, fid, snapshot_title):
    _, history=req(base,f'/api/finding-revisions/{fid}')
    for item in history['revisions']:
        _, detail=req(base,f'/api/finding-revisions/{fid}/{item["id"]}')
        if detail.get('revision',{}).get('snapshot',{}).get('title')==snapshot_title:
            return item,detail
    raise AssertionError('older title snapshot was not present')

def mobile_check(page, control):
    control.scroll_into_view_if_needed(); box=control.bounding_box(); ok(box is not None and box['width']<=390 and box['x']>=0, 'mobile control is not reachable within viewport')
    state=settled(page); ok(state['stable'] and state['sample'], 'theme variables did not settle'); ok(state['scrollWidth']<=state['width'],'mobile document overflow')
    ok(page.locator('select:visible').count()==0,'native select leaked visually')

def visual_run(pw, engine):
    report={'engine':engine,'cases':{},'failures':[],'screenshots':[],'external_requests':[],
            'runtime_expected':FROZEN,'frozen_helper_sha256':hashlib.sha256(Path('/tmp/interseptor_findings_improvements_qa_frozen.py').read_bytes()).hexdigest()}
    browser=getattr(pw,engine).launch(); context=browser.new_context(viewport={'width':1440,'height':900}); page=context.new_page()
    process=root=project=base=proxy=source=res=fixture=None
    try:
        process,root,project,base,proxy,source,res,binary_version,binary_sha256=prepare_local_binary()
        report.update({'binary_version':binary_version,'binary_sha256':binary_sha256,'runtime_observed':source.get('runtime_sha256')})
        ok(source.get('runtime_sha256')==FROZEN,'runtime digest mismatch')
        page.on('request',lambda r: report['external_requests'].append(r.url) if not r.url.startswith(base) and not r.url.startswith('data:') else None)
        fid,ids,fixture=seed(base,proxy); report.update({'finding_id':fid,'flow_ids':ids})
        page.goto(base,wait_until='domcontentloaded'); page.locator('.tab[data-tab="findings"]').click()
        page.locator('#findList .find-row').filter(has_text='QA findings improvements').click(); page.locator('#findToggleEdit').click(); page.wait_for_selector('#findTargetCleanup')

        def cleanup_desktop():
            try:
                page.locator('#findTargetCleanup').click(); page.locator('.find-target-cleanup [data-apply]').wait_for()
                ok('duplicate' in page.locator('.find-target-cleanup').inner_text().lower(),'cleanup preview lacks duplicate accounting')
                visual_shot(page,engine,'desktop-light-cleanup-preview',report,page.locator('.find-target-cleanup [data-apply]'))
            finally:
                page.locator('.find-target-cleanup [data-cancel]').click() if page.locator('.find-target-cleanup [data-cancel]').count() else None
        visual_case(report,'desktop cleanup preview',cleanup_desktop)

        def restore_distinct_older_title():
            old='QA older selected snapshot'; newer='QA newer distinct title'
            # A revision stores the post-edit snapshot, so select the edit that created old.
            rename_ui(page,base,fid,old)
            chosen,detail=find_revision(base,fid,old); snapshot=detail['revision']['snapshot']
            ok(snapshot['title']==old,'selected revision did not snapshot the older title')
            before_targets=[(t['url'],tuple(sorted(t.get('flow_ids',[])))) for t in snapshot['targets']]
            rename_ui(page,base,fid,newer); ok(newer!=snapshot['title'],'newer title did not differ from selected snapshot')
            _,before_history=req(base,f'/api/finding-revisions/{fid}')
            page.locator('.find-section-nav [data-find-section="review"]').click(); page.locator('.find-revisions summary').click()
            page.locator(f'[data-revision="{chosen["id"]}"] summary').click(); page.locator(f'[data-revision="{chosen["id"]}"] [data-restore]').wait_for()
            page.locator(f'[data-revision="{chosen["id"]}"] .find-revision-field summary').first.click()
            visual_shot(page,engine,'desktop-light-expanded-before-after-diff',report,page.locator(f'[data-revision="{chosen["id"]}"] .find-revision-diff'))
            page.locator(f'[data-revision="{chosen["id"]}"] [data-restore]').click(); page.locator('#confirmOk').click()
            restored=wait_api(base,f'/api/findings/{fid}',lambda f:f['title']==old,'restore did not return distinct older title')
            retained=[(t['url'],tuple(sorted(t.get('flow_ids',[])))) for t in restored['targets']]
            ok(retained==before_targets and set(ids)==set(sum((list(x[1]) for x in retained),[])),'restore did not retain target-flow relationships')
            _,after_history=req(base,f'/api/finding-revisions/{fid}')
            ok(len(after_history['revisions'])>len(before_history['revisions']) and after_history['revisions'][0]['action']=='restore','restore revision was not appended')
            report['revision_restore']={'selected_revision':chosen['id'],'snapshot_title':old,'newer_title':newer,'history_before':len(before_history['revisions']),'history_after':len(after_history['revisions']),'retained_targets':retained}
        visual_case(report,'UI restore selected distinct older revision with retained relationships',restore_distinct_older_title)

        def review_desktop():
            page.locator('.find-section-nav [data-find-section="review"]').click(); claim=page.locator('[data-capability="state_change"]')
            claim.locator('summary').click(); claim.locator('[data-claim-note]').wait_for(state='visible'); claim.locator('[data-claim-note]').fill('Generic loopback declaration'); claim.locator('[data-claim-note]').blur()
            execution=custom_trigger(page,'#findExecution'); execution.click(); page.locator('[role="option"][data-value="not_executed"]:visible').click()
            visual_shot(page,engine,'desktop-light-claim-controls',report,claim.locator('[data-claim-note]'))
            page.locator('.cvss-calculator summary').click(); page.locator('#findCvss').fill('CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N'); page.locator('[data-cvss-preview]').click(); page.locator('[data-cvss-apply]:not([disabled])').wait_for()
            visual_shot(page,engine,'desktop-light-calculator',report,page.locator('[data-cvss-apply]'))
        visual_case(report,'desktop claim custom-select and calculator',review_desktop)

        def deleted_desktop():
            try:
                st,d=req(base,'/api/findings','POST',{'title':'QA deleted dialog sibling','severity':'Info'}); ok(st==200,'delete sibling creation failed')
                st,_=req(base,f'/api/findings/{d["id"]}','DELETE'); ok(st in (200,204),'delete sibling failed')
                page.locator('#findDeletedOpen').click(); page.locator('#findDeletedModal').wait_for(state='visible'); visual_shot(page,engine,'desktop-light-deleted-dialog',report,page.locator('#findDeletedModal .modal-shell'))
            finally:
                if page.locator('#findDeletedModal [data-close]').count(): page.locator('#findDeletedModal [data-close]').click()
        visual_case(report,'desktop deleted dialog',deleted_desktop)

        def session_desktop():
            try:
                page.locator('.tab[data-tab="proxy"]').click(); page.locator('#rows .trow').first.wait_for(); rows=page.locator('#rows .trow'); rows.nth(0).click(); rows.nth(1).click(modifiers=['Meta']); rows.nth(0).click(button='right'); page.get_by_text('Inspect session timeline',exact=True).click()
                page.locator('#sessionInspectTimeline:not([hidden])').wait_for(); text=page.locator('#sessionInspectModal').inner_text().lower(); ok('passive observations only' in text,'session safety wording absent'); visual_shot(page,engine,'desktop-light-session-timeline',report,page.locator('#sessionInspectTimeline'))
            finally:
                if page.locator('#sessionInspectClose').count(): page.locator('#sessionInspectClose').click()
        visual_case(report,'desktop session timeline',session_desktop)

        def mobile_controls():
            page.locator('.tab[data-tab="findings"]').click(); page.set_viewport_size({'width':390,'height':844}); page.locator('#findDetail .find-workspace').wait_for(state='visible')
            page.locator('.find-section-nav [data-find-section="review"]').click(); claim=page.locator('[data-capability="state_change"]');
            if claim.get_attribute('open') is None: claim.locator('summary').click()
            mobile_check(page,claim.locator('[data-claim-note]')); visual_shot(page,engine,'mobile-light-claim',report,claim.locator('[data-claim-note]'))
            page.locator('#themeToggle').click(); dark=settled(page); ok(dark['stable'] and dark['scrollWidth']<=dark['width'],'dark theme failed to settle or overflowed')
            calculator=page.locator('.cvss-calculator');
            if calculator.get_attribute('open') is None: calculator.locator('summary').click()
            mobile_check(page,page.locator('#findCvss')); visual_shot(page,engine,'mobile-dark-calculator',report,page.locator('[data-cvss-apply]'))
            page.locator('#themeToggle').click(); light=settled(page); ok(light['stable'] and light['scrollWidth']<=light['width'],'light theme failed to settle or overflowed')
            page.locator('.find-section-nav [data-find-section="overview"]').click(); page.locator('#findTargetCleanup').click(); page.locator('.find-target-cleanup [data-apply]').wait_for(); mobile_check(page,page.locator('.find-target-cleanup [data-apply]')); visual_shot(page,engine,'mobile-light-cleanup',report,page.locator('.find-target-cleanup [data-apply]')); page.locator('.find-target-cleanup [data-cancel]').click()
            try:
                page.locator('#findDeletedOpen').click(); page.locator('#findDeletedModal').wait_for(state='visible'); mobile_check(page,page.locator('#findDeletedModal [data-close]')); visual_shot(page,engine,'mobile-light-deleted-dialog',report,page.locator('#findDeletedModal .modal-shell'))
            finally:
                if page.locator('#findDeletedModal [data-close]').count(): page.locator('#findDeletedModal [data-close]').click()
        visual_case(report,'390 light-dark mobile controls reachable without overflow',mobile_controls)
        ok(not report['external_requests'],'unexpected external requests '+repr(report['external_requests']))
    except Exception:
        report['failures'].append(traceback.format_exc())
    finally:
        if fixture: fixture.shutdown(); fixture.server_close()
        if process: audit.cleanup_managed_audit(process,root,project,res)
        context.close(); browser.close()
    return report

def visual_main():
    if OUT.exists(): raise SystemExit('QA_OUT already exists')
    OUT.mkdir(mode=0o700); reports=[]
    with sync_playwright() as pw:
        for engine in ENGINES:
            report=visual_run(pw,engine); reports.append(report); (OUT/f'{engine}.json').write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
    data={'runtime_expected_sha256':FROZEN,'binary_expected_sha256':BINARY,'probe':str(Path(__file__)),'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'reports':reports}
    (OUT/'report.json').write_text(json.dumps(data,indent=2,sort_keys=True)+'\n'); print(OUT/'report.json')
    return 1 if any(r['failures'] or any(v=='fail' for v in r['cases'].values()) for r in reports) else 0

if __name__=='__main__': raise SystemExit(visual_main())
