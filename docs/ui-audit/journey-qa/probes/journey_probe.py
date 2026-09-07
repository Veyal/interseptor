#!/usr/bin/env python3
"""Disposable-candidate Findings/evidence journey probe. Writes only beneath /tmp."""
import argparse, hashlib, json, os, sys, time
from pathlib import Path
REPO = Path(os.environ.get("INTERSEPTOR_REPO", Path.cwd())).resolve()
sys.path.insert(0, str(REPO / "scripts"))
import ui_browser_audit as audit
from playwright.sync_api import sync_playwright

OUT = None
REPORT = {"cases": {}, "screenshots": [], "console_errors": [], "page_errors": []}

def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def rec(name, fn):
    try:
        value = fn()
        REPORT["cases"][name] = {"status": "pass", **(value or {})}
        print("PASS", name, flush=True)
        return value
    except Exception as e:
        REPORT["cases"][name] = {"status": "fail", "reason": f"{type(e).__name__}: {e}"}
        print("FAIL", name, e, flush=True)
        return None
def shot(page, name, engine, size):
    page.evaluate("""async()=>{await Promise.race([Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>null))),new Promise(r=>setTimeout(r,750))]);await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));}""")
    p = OUT / "screenshots" / f"{engine}-{size[0]}x{size[1]}-{name}.png"; p.parent.mkdir(parents=True,exist_ok=True)
    page.screenshot(path=str(p), animations="disabled")
    REPORT["screenshots"].append({"path":str(p),"sha256":sha(p),"engine":engine,"viewport":list(size),"surface":name})
def ready(page):
    page.wait_for_selector('#tabs[aria-busy="false"]', state="attached", timeout=20000)
    page.wait_for_function("[...document.querySelectorAll('.tab')].every(x=>!x.disabled)")
    if page.locator("#setupModal").is_visible(): page.locator("#setupSkip").click()
def findings(page, mobile=False):
    if mobile:
        page.locator("#mobileToolSelectUi").click(); page.get_by_role("option",name="Findings",exact=True).last.click()
    else: page.locator("#tab-findings").click()
    page.wait_for_function("document.querySelector('#panel-findings')?.classList.contains('active')")
def assert_no_visible_select(page):
    bad=page.evaluate("""()=>[...document.querySelectorAll('select')].filter(x=>{const s=getComputedStyle(x),r=x.getBoundingClientRect();return r.width>2&&r.height>2&&s.display!=='none'&&s.visibility!=='hidden'&&Number(s.opacity||1)>0&&s.appearance!=='none'}).map(x=>x.id)""")
    assert not bad, f"visible native selects: {bad}"
def custom_choose(page, native_id, option):
    trigger=page.locator('#'+native_id+'Ui')
    assert trigger.count(), f"custom trigger missing for #{native_id}"
    trigger.click(); menu=trigger.get_attribute('aria-controls'); assert menu, f"custom trigger has no listbox: #{native_id}Ui"
    page.locator('#'+menu+' [role="option"]',has_text=option).click()
def blurfill(page, sel, text):
    page.locator(sel).fill(text); page.locator(sel).blur()
def main(a):
    global OUT; OUT=Path(a.out); OUT.mkdir(parents=True,exist_ok=True)
    REPORT.update({"base":a.base,"proxy":a.proxy,"started_unix":time.time(),"probe_sha256":sha(__file__),"source_hashes":a.source_hashes})
    with sync_playwright() as pw:
      # Chromium owns mutations. It first captures generic local HTTP evidence, then creates the finding entirely via UI.
      browser=pw.chromium.launch(); ctx=browser.new_context(viewport={"width":1440,"height":900},accept_downloads=True)
      ctx.grant_permissions(['clipboard-read','clipboard-write'],origin=a.base)
      # Count production reads of the native picker API without changing its value.
      ctx.add_init_script("""()=>{window.__qaSavePickerReads=0;const d=Object.getOwnPropertyDescriptor(Window.prototype,'showSaveFilePicker')||Object.getOwnPropertyDescriptor(window,'showSaveFilePicker');Object.defineProperty(window,'showSaveFilePicker',{configurable:true,get(){window.__qaSavePickerReads++;return d?(d.get?d.get.call(window):d.value):undefined}})}""")
      page=ctx.new_page(); page.set_default_timeout(12000)
      page.on("console",lambda m: REPORT["console_errors"].append(m.text) if m.type=="error" else None); page.on("pageerror",lambda e:REPORT["page_errors"].append(str(e)))
      page.goto(a.base,wait_until="domcontentloaded"); ready(page)
      srv,thread=audit.start_fixture(); fixture=f"http://127.0.0.1:{srv.server_address[1]}"
      def capture():
        status,body=audit.proxy_request(("127.0.0.1",int(a.proxy.rsplit(':',1)[1])),fixture+"/fixture/request-response?example=1",method="POST",body=b'{"fixture":true}')
        assert status==200, audit.bounded_response_diagnostic(status,body)
        page.locator('#tab-proxy').click(); page.locator('#rows .trow').first.wait_for()
      rec("chromium:captured-generic-http-flow",capture)
      def create_and_edit():
        findings(page); assert_no_visible_select(page); page.locator('#findNew').click(); page.locator('#findCreateModal').wait_for()
        title='Generic Findings journey '+str(time.time_ns()); page.locator('#fcTitle').fill(title); custom_choose(page,'fcSeverity','High'); page.locator('#fcSave').click()
        page.wait_for_function("async title=>(await (await fetch('/api/findings')).json()).findings.some(f=>f.title===title)",arg=title)
        fid=page.evaluate("async title=>(await (await fetch('/api/findings')).json()).findings.find(f=>f.title===title).id",title)
        page.locator(f'#findList .find-row[data-id="{fid}"]').click(); page.locator('#findDetail #findToggleEdit').wait_for()
        # Overview + technical details section.
        blurfill(page,'#findSummary','Generic UI-only summary'); blurfill(page,'#findImpact','Generic fixture impact'); blurfill(page,'#findWhy','Generic fixture reason'); blurfill(page,'#findTarget','https://example.com/findings-journey')
        page.locator('#findEditTags').click(); page.locator('#promptInput').fill('qa, generic'); page.locator('#promptOk').click()
        # Evidence: typed step, captured local flow picked through UI, and generic screenshot attached through normal file input.
        page.locator('.find-section-nav [data-find-section="evidence"]').click(); page.locator('#findAddText').click(); blurfill(page,'#findBody .block-text','Generic local reproduction step')
        page.locator('#findAddFlow').click(); page.locator('#findFlowPickModal').wait_for(); page.locator('#findFlowPickList input[type="checkbox"]').first.check(); page.locator('#ffpAttach').click(); page.locator('#findBody .find-doc-flow').wait_for()
        page.locator('#findAddImage').click(); page.evaluate("""async()=>{const c=document.createElement('canvas');c.width=c.height=1;c.getContext('2d').fillRect(0,0,1,1);const b=await new Promise(ok=>c.toBlob(ok,'image/png'));const d=new DataTransfer();d.items.add(new File([b],'generic-fixture.png',{type:'image/png'}));const i=document.querySelector('#findImageFile');i.files=d.files;i.dispatchEvent(new Event('change',{bubbles:true}))}""")
        page.locator('#findBody .find-doc-image').wait_for();
        # Remediation and Review fields plus custom controls.
        page.locator('.find-section-nav [data-find-section="remediation"]').click(); blurfill(page,'#findFix','Generic remediation'); blurfill(page,'#findRetest','Generic secure retest')
        # Scalar blur writes rerender the document; wait for their queue before opening the next portal select.
        page.wait_for_timeout(1000)
        page.locator('.find-section-nav [data-find-section="review"]').click(); page.wait_for_timeout(300); custom_choose(page,'findStatus','verified'); page.wait_for_timeout(300); custom_choose(page,'findConfidence','Firm')
        page.wait_for_function("async id=>{const f=await (await fetch('/api/findings/'+id)).json();return f.summary==='Generic UI-only summary'&&f.impact==='Generic fixture impact'&&f.why==='Generic fixture reason'&&f.fix==='Generic remediation'&&f.retest==='Generic secure retest'&&f.status==='verified'&&f.confidence==='firm'&&(f.tags||[]).includes('qa')&&(f.blocks||[]).some(x=>x.type==='flow')&&(f.blocks||[]).some(x=>x.type==='image')}",arg=fid)
        return {"fid":fid,"title":title}
      created=rec("chromium:create-edit-all-sections-and-evidence-ui",create_and_edit) or {}; fid=created.get("fid"); title=created.get("title")
      def persistence_navigation():
        assert fid; page.reload(wait_until="domcontentloaded"); ready(page); findings(page); page.locator(f'#findList .find-row[data-id="{fid}"]').click(); page.locator('.find-section-nav [data-find-section="evidence"]').click(); page.wait_for_function("id=>location.hash==='#finding-'+id+'/evidence'",arg=fid)
        page.go_back(); page.go_forward(); page.locator(f'#findList .find-row[data-id="{fid}"]').click(); page.locator('.find-section-nav [data-find-section="evidence"]').click(); page.wait_for_function("id=>location.hash==='#finding-'+id+'/evidence'",arg=fid)
        page.locator('.find-inline-toggle').first.click(); body=page.locator('.find-inline-content'); body.wait_for(); expected=body.inner_text(); page.locator('[data-copy-evidence]').click(); page.wait_for_function("async text=>(await navigator.clipboard.readText())===text",arg=expected); shot(page,'inline-http-copy',"chromium",(1440,900))
      rec("chromium:reload-deeplink-back-forward-inline-copy",persistence_navigation)
      def filters_and_readiness():
        page.locator('#findSearch').fill('Generic Findings journey'); page.wait_for_selector(f'#findList .find-row[data-id="{fid}"]')
        custom_choose(page,'findFilterSeverity','High'); page.wait_for_selector(f'#findList .find-row[data-id="{fid}"]')
        custom_choose(page,'findFilterStatus','Verified'); page.wait_for_selector(f'#findList .find-row[data-id="{fid}"]')
        page.locator('.find-section-nav [data-find-section="review"]').click(); page.locator('.find-stage-link').click(); page.wait_for_selector('#find-sec-review')
      rec("chromium:search-custom-filters-readiness-navigation",filters_and_readiness)
      def export_paths():
        # Instrument immediately before the export action; saveFile is invoked only by that action.
        page.evaluate("""()=>{window.__qaSavePickerReads=0;Object.defineProperty(window,'showSaveFilePicker',{configurable:true,get(){window.__qaSavePickerReads++;return undefined}})}""")
        downloads=[]
        for label,ext in [('Markdown','md'),('HTML with images','html'),('JSON','json')]:
          page.locator('#findExportOpen').click(); page.locator('#findExportModal').wait_for(); assert_no_visible_select(page); custom_choose(page,'findExportFmt',label)
          with page.expect_download() as event: page.locator('#findExport').click()
          download=event.value; target=OUT/'downloads'/('interseptor-findings.'+ext); target.parent.mkdir(parents=True,exist_ok=True); download.save_as(str(target)); content=target.read_text(encoding='utf-8')
          assert target.stat().st_size>0 and download.suggested_filename.endswith('.'+ext), (download.suggested_filename,target.stat().st_size)
          assert title in content and 'Generic UI-only summary' in content and 'Generic local reproduction step' in content, ext
          if ext=='json':
            parsed=json.loads(content); assert 'flow' in content and 'image' in content and 'Generic remediation' in content, type(parsed)
          downloads.append({'format':ext,'path':str(target),'size':target.stat().st_size,'sha256':sha(target),'suggested':download.suggested_filename})
          page.locator('#findExportModal').wait_for(state='hidden')
        # Confirm the recoverable API error keeps controls usable; this deliberately avoids any false native-picker claim.
        page.locator('#findExportOpen').click(); page.locator('#findExportModal').wait_for()
        page.route('**/api/findings/report*',lambda route:route.fulfill(status=500,content_type='application/json',body='{"error":"fixture export error"}'))
        page.locator('#findExport').click(); page.wait_for_function("document.querySelector('#findExport')?.disabled===false"); assert page.locator('#findExportModal').is_visible()
        page.unroute('**/api/findings/report*'); page.locator('#findExportClose').click()
        reads=page.evaluate("window.__qaSavePickerReads"); assert reads==0, f"native picker property accessed {reads} times"
        return {"downloads":downloads,"native_picker_property_accesses":reads}
      rec("chromium:export-bytes-and-recoverable-error",export_paths)
      def failed_edit_retry():
        page.locator('#findToggleEdit').click() if page.locator('#findToggleEdit').inner_text()=="Edit" else None; page.locator('.find-section-nav [data-find-section="overview"]').click(); marker='retry-'+str(time.time_ns())
        page.route(f'**/api/findings/{fid}',lambda route:route.fulfill(status=500,content_type='application/json',body='{"error":"fixture save rejection"}') if route.request.method=='PATCH' else route.continue_())
        blurfill(page,'#findSummary',marker); page.wait_for_function("document.querySelector('#findSaveState')?.textContent.includes('Save failed')"); assert page.locator('#findSaveRetry').is_visible()
        page.unroute(f'**/api/findings/{fid}'); page.locator('#findSaveRetry').click(); page.wait_for_function("async v=>(await (await fetch('/api/findings/'+v[0])).json()).summary===v[1]",arg=[fid,marker])
      rec("chromium:failed-edit-retains-and-retries",failed_edit_retry)
      shot(page,'desktop-final',"chromium",(1440,900)); srv.shutdown(); srv.server_close(); ctx.close(); browser.close()
      # Representative connected reading paths in Firefox/WebKit, including mobile's custom navigation.
      for engine in ('firefox','webkit'):
        b=getattr(pw,engine).launch(); c=b.new_context(viewport={'width':390,'height':844},reduced_motion='reduce',accept_downloads=True); p=c.new_page(); p.set_default_timeout(12000)
        p.on('console',lambda m,e=engine: REPORT['console_errors'].append(e+': '+m.text) if m.type=='error' else None); p.on('pageerror',lambda er,e=engine: REPORT['page_errors'].append(e+': '+str(er)))
        p.goto(a.base,wait_until='domcontentloaded'); ready(p)
        def representative(p=p,e=engine):
          findings(p,True); assert_no_visible_select(p); p.locator(f'#findList .find-row[data-id="{fid}"]').click(); p.locator('.find-section-nav [data-find-section="evidence"]').click(); p.locator('.find-inline-toggle').first.click(); p.locator('.find-inline-content').wait_for(); p.locator('#findExportOpen').click(); p.locator('#findExportModal').wait_for(); assert_no_visible_select(p); custom_choose(p,'findExportFmt','JSON'); shot(p,'phone-evidence-export',e,(390,844));
          with p.expect_download() as event: p.locator('#findExport').click()
          download=event.value; target=OUT/'downloads'/f'{e}-interseptor-findings.json'; download.save_as(str(target)); content=target.read_text(encoding='utf-8'); assert target.stat().st_size>0 and title in content and 'Generic local reproduction step' in content
        rec(f'{engine}:phone-connected-evidence-export-custom-selector',representative); c.close(); b.close()
    REPORT['finished_unix']=time.time(); REPORT['failures']=[k for k,v in REPORT['cases'].items() if v['status']=='fail']; REPORT['report_sha256_source']=sha(__file__)
    (OUT/'journey-report.json').write_text(json.dumps(REPORT,indent=2,sort_keys=True)+'\n')
    print(json.dumps({'failures':REPORT['failures'],'screenshots':len(REPORT['screenshots'])},indent=2))
    return 1 if REPORT['failures'] else 0
if __name__=='__main__':
    q=argparse.ArgumentParser();q.add_argument('--base',required=True);q.add_argument('--proxy',required=True);q.add_argument('--out',required=True);q.add_argument('--source-hashes',default='');z=q.parse_args();raise SystemExit(main(z))
