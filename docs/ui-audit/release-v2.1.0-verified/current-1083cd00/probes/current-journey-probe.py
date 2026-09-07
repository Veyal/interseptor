#!/usr/bin/env python3
"""Prepared-only loopback browser coverage for findings CVSS/export recovery.

Run only after supplying QA_REPO, QA_EXPECT_RUNTIME, QA_EXPECT_BINARY and QA_OUT.
It imports the retained fixture lifecycle from the accepted release-candidate probe.
"""
from __future__ import annotations
import hashlib, importlib.util, json, os, sys, time, traceback, tempfile, secrets, shutil, subprocess
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE_PROBE=Path(os.environ["QA_BASE_PROBE"]).resolve()
ROOT=Path(os.environ.get('QA_REPO', Path.cwd())).resolve()
OUT=Path(os.environ.get('QA_OUT', '/tmp/interseptor-findings-cvss-export-supplemental-'+str(int(time.time()))))
ENGINES=tuple(os.environ.get('QA_ENGINES','chromium,firefox,webkit').split(','))
VECTOR_A='CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N'
VECTOR_B='CVSS:4.0/AV:L/AC:H/AT:P/PR:H/UI:P/VC:L/VI:H/VA:N/SC:N/SI:N/SA:N'

def require(name):
    value=os.environ.get(name,'').strip()
    if not value: raise SystemExit(name+' is required; this prepared probe never guesses a release identity')
    return value

def load_base():
    spec=importlib.util.spec_from_file_location('retained_visual_probe',BASE_PROBE)
    mod=importlib.util.module_from_spec(spec); sys.modules[spec.name]=mod; spec.loader.exec_module(mod)
    mod.ROOT=ROOT; mod.FROZEN=require('QA_EXPECT_RUNTIME'); mod.BINARY=require('QA_EXPECT_BINARY')
    return mod

def ok(value, message):
    if not value: raise AssertionError(message)

def wait_api(base, path, predicate, message):
    end=time.monotonic()+15; last=None
    while time.monotonic()<end:
        status,last=base.req(path[0],path[1]) if isinstance(path,tuple) else (0,None)
        if status==200 and predicate(last): return last
        time.sleep(.1)
    raise AssertionError(message+': '+repr(last))

def visible_select(page, selector, value):
    native=page.locator(selector)
    trigger_id=native.evaluate("s=>{const t=s._uiSelect?.trigger;if(!t)throw new Error('missing visible _uiSelect.trigger');return t.id}")
    trigger=page.locator('#'+trigger_id); trigger.scroll_into_view_if_needed(); ok(trigger.is_visible(),selector+' visible control missing')
    trigger.click(); page.locator('[role="option"][data-value="'+value+'"]:visible').click()

def install_fetch_gate(page, finding_id):
    page.evaluate("""id => {
      const original=window.fetch.bind(window);
      const state={mode:'pass', patches:[], reports:[], releases:[]};
      window.__supplementalFetch=state;
      window.fetch=(input, init={}) => {
        const url=typeof input==='string'?input:input.url;
        const method=(init.method||'GET').toUpperCase();
        if(url.includes('/api/findings/report')) state.reports.push(url);
        if(method==='PATCH' && url.endsWith('/api/findings/'+id)) {
          state.patches.push(init.body||'');
          if(state.mode==='hold') return new Promise(resolve=>state.releases.push(()=>resolve(original(input,init))));
          if(state.mode==='fail') return Promise.resolve(new Response(JSON.stringify({error:'synthetic finding PATCH rejection'}),{status:500,headers:{'content-type':'application/json'}}));
        }
        return original(input,init);
      };
    }""", finding_id)

def open_edit_review(page, title):
    page.locator('.tab[data-tab="findings"]').click()
    page.locator('#findList .find-row').filter(has_text=title).click()
    if page.locator('#findToggleEdit').inner_text()=='Edit': page.locator('#findToggleEdit').click()
    page.locator('.find-section-nav [data-find-section="review"]').click()
    page.locator('#findCvss').wait_for(state='visible')

def await_cvss_preview(page, vector):
    page.locator('#findCvss').fill(vector); page.locator('[data-cvss-preview]').click()
    page.locator('[data-cvss-apply]:not([disabled])').wait_for(state='visible')

def open_draft_export(page, fmt='md'):
    page.locator('#findExportOpen').click(); page.locator('#findExportModal').wait_for(state='visible')
    visible_select(page,'#findExportMode','draft')
    visible_select(page,'#findExportFmt',fmt)
    visible_select(page,'#findExportStatuses','all')

def prepare_switchable_local_binary(base):
    """Start the exact candidate in a disposable root with re-exec-safe loopback env.

    The retained base fixture correctly uses managed descriptor listeners and locks
    project switching. This separate owned fixture is required only for the actual
    project round-trip: normal re-exec retains these environment addresses.
    """
    paths=base.audit.runtime_source_paths(ROOT); source=base.audit.source_identity(ROOT,paths,base.audit.source_base_commit(ROOT))
    ok(source['runtime_sha256']==base.FROZEN,'runtime digest mismatch '+repr(source))
    root=Path(tempfile.mkdtemp(prefix='interseptor-ui-audit-')); project='ui-audit-'+secrets.token_hex(6)
    (root/'projects'/project).mkdir(parents=True)
    (root/base.audit.AUDIT_SENTINEL_NAME).write_text(f'interseptor-ui-audit\nproject={project}\n',encoding='utf-8')
    candidate=Path(os.environ.get('QA_BINARY',ROOT/'interseptor')); binary=root/'interseptor-audit'; shutil.copy2(candidate,binary)
    ok(hashlib.sha256(binary.read_bytes()).hexdigest()==base.BINARY,'candidate binary digest mismatch')
    version=subprocess.run([str(binary),'version'],capture_output=True,text=True,check=True).stdout.strip()
    ok('2.0.10-local' in version,'candidate binary is not local 2.0.10: '+version)
    control=base.audit.reserve_loopback_listener(); proxy_listener=base.audit.reserve_loopback_listener()
    control_addr='127.0.0.1:'+str(control.getsockname()[1]); proxy_addr='127.0.0.1:'+str(proxy_listener.getsockname()[1])
    # Normal re-exec binds the same private addresses. Release our reservations
    # immediately before this child starts; no inherited descriptors or live app.
    control.close(); proxy_listener.close()
    env={key:value for key,value in os.environ.items() if not key.startswith('INTERSEPTOR_')}
    env.update({'INTERSEPTOR_DATA_DIR':str(root),'INTERSEPTOR_CONTROL_ADDR':control_addr,'INTERSEPTOR_PROXY_ADDR':proxy_addr,'INTERSEPTOR_NO_UPDATE_CHECK':'1','INTERSEPTOR_NO_BROWSER':'1'})
    process=subprocess.Popen([str(binary),'--data-dir',str(root),'--project',project,'--control-addr',control_addr,'--proxy-port',str(proxy_addr.rsplit(':',1)[1])],cwd=ROOT,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    base_url='http://'+control_addr; end=time.monotonic()+20
    while time.monotonic()<end:
        if process.poll() is not None: raise RuntimeError('switchable candidate exited before readiness')
        try:
            status,identity=base.req(base_url,'/api/project')
            if status==200 and identity.get('current')==project and identity.get('canSwitch') is True:
                return process,root,project,base_url,('127.0.0.1',int(proxy_addr.rsplit(':',1)[1])),source,[],version,hashlib.sha256(binary.read_bytes()).hexdigest()
        except Exception: pass
        time.sleep(.1)
    base.audit.stop_managed_process(process); base.audit.remove_managed_root(root,project)
    raise RuntimeError('switchable candidate did not become ready')

def switch_isolated_project(page, original, created):
    def dismiss_first_run_setup():
        modal=page.locator('#setupModal')
        if modal.is_visible():
            page.locator('#setupSkip').click()
            modal.wait_for(state='hidden')
    page.locator('#projBadge').click(); page.locator('#projModal').wait_for(state='visible')
    page.locator('#pmNew').fill(created); page.locator('#pmNewBtn').click()
    page.wait_for_function("name=>document.querySelector('#projBadge')?.textContent.includes(name)",arg=created,timeout=35000)
    dismiss_first_run_setup()
    page.locator('#projBadge').click(); page.locator('#projModal').wait_for(state='visible')
    page.locator('#pmList .pm-row').filter(has_text=original).click()
    page.wait_for_function("name=>document.querySelector('#projBadge')?.textContent.includes(name)",arg=original,timeout=35000)
    dismiss_first_run_setup()

def run_engine(pw, engine, base):
    report={'engine':engine,'cases':{},'failures':[],'runtime_expected':base.FROZEN,'binary_expected':base.BINARY,'downloads':[]}
    browser=getattr(pw,engine).launch(); context=browser.new_context(viewport={'width':1440,'height':900},accept_downloads=True); page=context.new_page()
    process=root=project=control_base=proxy=source=reservations=fixture=None
    def case(name, fn):
        try: fn(); report['cases'][name]='pass'
        except Exception as exc:
            report['cases'][name]='fail'; report['failures'].append(name+': '+type(exc).__name__+': '+str(exc)+'\n'+traceback.format_exc())
            try:
                path=OUT/'failures'/f'{engine}-{name.replace(" ","-")}.png'; path.parent.mkdir(parents=True,exist_ok=True); page.screenshot(path=str(path)); report.setdefault('failure_screenshots',[]).append({'path':str(path),'sha256':hashlib.sha256(path.read_bytes()).hexdigest()})
            except Exception: pass
    try:
        process,root,project,control_base,proxy,source,reservations,version,binary_sha=prepare_switchable_local_binary(base)
        report.update({'runtime_observed':source.get('runtime_sha256'),'binary_observed':binary_sha,'binary_version':version})
        fid,ids,fixture=base.seed(control_base,proxy)
        title='QA findings improvements'; st,second=base.req(control_base,'/api/findings','POST',{'title':'QA supplemental second finding','severity':'Low','status':'open'})
        ok(st==200,'second generic finding failed')
        page.goto(control_base,wait_until='domcontentloaded'); open_edit_review(page,title); install_fetch_gate(page,fid)

        def cvss_latest_intent_and_retry():
            page.evaluate("window.__supplementalFetch.mode='hold'")
            await_cvss_preview(page,VECTOR_A); page.locator('[data-cvss-apply]').click()
            page.wait_for_function("window.__supplementalFetch.patches.length===1")
            page.locator('#findCvss').fill(VECTOR_B); page.locator('#findCvss').blur()
            # Navigation remounts the editor while A is still waiting; B must own the draft.
            page.locator('#findList .find-row').filter(has_text='QA supplemental second finding').click()
            page.locator('#findList .find-row').filter(has_text=title).click(); page.locator('.find-section-nav [data-find-section="review"]').click()
            page.locator('#findCvss').wait_for(); ok(page.locator('#findCvss').input_value()==VECTOR_B,'newer vector B was lost during delayed A remount')
            page.evaluate("window.__supplementalFetch.mode='pass'; window.__supplementalFetch.releases.shift()()")
            wait_api(base,(control_base,'/api/findings/'+str(fid)),lambda f:f.get('cvss')==VECTOR_A,'delayed A never reached persistence')
            page.locator('#findCvss').wait_for(); ok(page.locator('#findCvss').input_value()==VECTOR_B,'A acknowledgement overwrote B')
            await_cvss_preview(page,VECTOR_B); page.evaluate("window.__supplementalFetch.mode='fail'"); page.locator('[data-cvss-apply]').click()
            page.locator('[data-cvss-status].error').wait_for(); ok(page.locator('#findCvss').input_value()==VECTOR_B,'failed Apply discarded B')
            page.locator('[data-cvss-apply]:not([disabled])').wait_for(); page.evaluate("window.__supplementalFetch.mode='pass'"); page.locator('[data-cvss-apply]').click()
            saved=wait_api(base,(control_base,'/api/findings/'+str(fid)),lambda f:f.get('cvss')==VECTOR_B,'retry did not persist B')
            ok(saved.get('severity')!='Low','retry did not persist matching normalized severity')
        case('delayed CVSS A preserves newer B and failed retry retains B',cvss_latest_intent_and_retry)

        def export_waits_and_failed_drafts_block():
            page.locator('.find-section-nav [data-find-section="overview"]').click(); summary='New summary exported only after save'
            page.evaluate("window.__supplementalFetch.mode='hold'; window.__supplementalFetch.reports=[]")
            patch_count=page.evaluate('window.__supplementalFetch.patches.length')
            page.locator('#findSummary').fill(summary); page.locator('#findSummary').blur(); page.wait_for_function("count=>window.__supplementalFetch.patches.length>count", arg=patch_count)
            open_draft_export(page,'json')
            with page.expect_download(timeout=12000) as download:
                page.locator('#findExport').click(); page.evaluate("window.__supplementalFetch.mode='pass'; window.__supplementalFetch.releases.shift()()")
            target=OUT/'downloads'/f'{engine}-saved-summary.json'; target.parent.mkdir(parents=True,exist_ok=True); download.value.save_as(str(target))
            exported=json.loads(target.read_text()); matching=[f for f in exported.get('findings',[]) if f.get('id')==fid]
            ok(len(matching)==1 and matching[0].get('summary')==summary,'export omitted the newly saved summary'); report['downloads'].append({'path':str(target),'sha256':hashlib.sha256(target.read_bytes()).hexdigest()})
            page.locator('#findExportModal').wait_for(state='hidden')
            page.evaluate("window.__supplementalFetch.mode='fail'; window.__supplementalFetch.reports=[]")
            page.locator('#findSummary').fill('Rejected stale export text'); page.locator('#findSummary').blur(); page.locator('#findSaveState').get_by_text('Save failed').wait_for()
            for mode in ('draft','final'):
                try:
                    open_draft_export(page); visible_select(page,'#findExportMode',mode); page.locator('#findExport').click()
                    page.locator('#toast .toast-item.show').filter(has_text='Save or retry finding changes and Apply CVSS previews before exporting.').last.wait_for()
                    ok(not page.evaluate('window.__supplementalFetch.reports.length'),'failed edit issued a stale '+mode+' report request')
                finally:
                    if page.locator('#findExportModal').is_visible(): page.locator('#findExportClose').click()
            page.evaluate("window.__supplementalFetch.mode='pass'"); page.locator('#findSaveRetry').click()
            wait_api(base,(control_base,'/api/findings/'+str(fid)),lambda f:f.get('summary')=='Rejected stale export text','retry after export barrier did not clear failed draft')
        case('export waits for saved edit and blocks failed Draft and Final',export_waits_and_failed_drafts_block)

        def failed_apply_discard_restores_saved_vector_and_unblocks_navigation():
            open_edit_review(page,'QA supplemental second finding')
            install_fetch_gate(page,second['id'])
            await_cvss_preview(page,VECTOR_A); page.locator('[data-cvss-apply]').click()
            wait_api(base,(control_base,'/api/findings/'+str(second['id'])),lambda f:f.get('cvss')==VECTOR_A,'saved baseline vector A missing')
            page.wait_for_function("document.querySelector('#findCvss')?.value===%s && document.querySelector('[data-cvss-status]')?.textContent===''" % json.dumps(VECTOR_A))
            await_cvss_preview(page,VECTOR_B); page.evaluate("window.__supplementalFetch.mode='fail'"); page.locator('[data-cvss-apply]').click()
            page.locator('[data-cvss-status].error').wait_for(); discard=page.locator('[data-cvss-discard]')
            discard.wait_for(state='visible'); discard.click(); page.locator('[data-cvss-status]').get_by_text('Preview discarded').wait_for()
            ok(page.locator('#findCvss').input_value()==VECTOR_A,'discard after failed Apply did not restore saved vector A')
            page.locator('#findToggleEdit').click(); page.wait_for_function("document.querySelector('#findToggleEdit')?.textContent==='Edit'")
            created='qa-supplemental-project-'+str(second['id'])
            switch_isolated_project(page,project,created)
            page.locator('.tab[data-tab="findings"]').click(); page.locator('#findList .find-row').filter(has_text='QA supplemental second finding').click()
            ok(page.locator('#findTitleText').inner_text()=='QA supplemental second finding','return switch lost the original finding context')
            open_draft_export(page)
            with page.expect_download(timeout=12000) as download: page.locator('#findExport').click()
            target=OUT/'downloads'/f'{engine}-discarded-failed-preview.md'; target.parent.mkdir(parents=True,exist_ok=True); download.value.save_as(str(target)); ok(target.stat().st_size>0,'discarded failed preview still blocked export')
            report['downloads'].append({'path':str(target),'sha256':hashlib.sha256(target.read_bytes()).hexdigest()})
        case('failed Apply discard restores vector and unblocks Done project switch and export',failed_apply_discard_restores_saved_vector_and_unblocks_navigation)
    except Exception:
        report['failures'].append(traceback.format_exc())
    finally:
        if fixture: fixture.shutdown(); fixture.server_close()
        if process: base.audit.cleanup_managed_audit(process,root,project,reservations)
        context.close(); browser.close()
    return report

def main():
    if OUT.exists(): raise SystemExit('QA_OUT already exists')
    base=load_base(); OUT.mkdir(mode=0o700); reports=[]
    with sync_playwright() as pw:
        for engine in ENGINES:
            report=run_engine(pw,engine,base); reports.append(report); (OUT/(engine+'.json')).write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
    data={'probe':str(Path(__file__)),'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'base_probe':str(BASE_PROBE),'base_probe_sha256':hashlib.sha256(BASE_PROBE.read_bytes()).hexdigest(),'reports':reports}
    (OUT/'report.json').write_text(json.dumps(data,indent=2,sort_keys=True)+'\n'); print(OUT/'report.json')
    return 1 if any(r['failures'] or any(v=='fail' for v in r['cases'].values()) for r in reports) else 0

if __name__=='__main__': raise SystemExit(main())
