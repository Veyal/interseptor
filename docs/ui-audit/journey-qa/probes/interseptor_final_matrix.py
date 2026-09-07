#!/usr/bin/env python3
"""Fixture-only final UI matrix for a managed disposable Interseptor candidate.

Usage:
  interseptor_final_matrix.py serve --out DIR
  interseptor_final_matrix.py probe --base URL --out DIR [--session DIR/session.json]
"""
import argparse, hashlib, json, os, signal, sys, time
from pathlib import Path

REPO = Path(os.environ.get("INTERSEPTOR_REPO", Path.cwd())).resolve()
sys.path.insert(0, str(REPO / "scripts"))
import ui_browser_audit as audit
from playwright.sync_api import sync_playwright

PANELS = ("proxy", "intercept", "repeater", "intruder", "scanner", "map", "findings", "notes", "activity", "settings")
SECTIONS = ("proxy", "tls", "devices", "scope", "scanner", "session", "project", "api")
VIEWPORTS = ((1440,900),(1024,768),(390,844))
ENGINES = ("chromium","firefox","webkit")

def sha(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def write(path, obj):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(obj, indent=2, sort_keys=True)+"\n")

def serve(args):
    out=Path(args.out).resolve(); out.mkdir(parents=True, exist_ok=True)
    p,root,project,base,proxy,source,reservations=audit.prepare_managed_audit()
    info={"base":base,"proxy":f"{proxy[0]}:{proxy[1]}","project":project,"root":str(root),"pid":p.pid,"source":source,"probe_sha256":sha(__file__),"started_unix":time.time()}
    write(out/"session.json",info); print(json.dumps(info),flush=True)
    stopping=False
    def stop(*_):
        nonlocal stopping; stopping=True
    signal.signal(signal.SIGTERM,stop); signal.signal(signal.SIGINT,stop)
    while not stopping and p.poll() is None: time.sleep(.25)
    audit.cleanup_managed_audit(p,root,project,reservations)

def check(cases,name,fn):
    try: fn(); cases[name]={"status":"pass"}; print("PASS",name,flush=True)
    except Exception as e: cases[name]={"status":"fail","reason":f"{type(e).__name__}: {e}"}; print("FAIL",name,cases[name]["reason"],flush=True)

def seed(page, base, proxy):
    # A generic local HTTP response captured through the disposable proxy.
    srv,thread=audit.start_fixture(); port=srv.server_address[1]; fx=f"http://127.0.0.1:{port}"
    status,body=audit.proxy_request(("127.0.0.1",int(proxy.rsplit(':',1)[1])),fx+"/fixture/request-response?example=1",method="POST",body=b'{"fixture":true}')
    if status != 200: raise AssertionError(audit.bounded_response_diagnostic(status,body))
    page.locator('#tab-proxy').click(); page.locator('#rows .trow').first.wait_for(timeout=10000)
    # Persist a generic local note through the ordinary Notes editor before the
    # per-engine screenshots; Activity remains intentionally empty because this
    # fixture run does not invoke an external agent.
    page.locator('#tab-notes').click(); page.locator('#notesEdit').wait_for()
    notes='## Generic UI fixture\n\nCaptured local example.com-equivalent request/response evidence only.'
    page.locator('#notesEdit').fill(notes)
    page.wait_for_function("async text=>(await (await fetch('/api/notes')).json()).notes===text",arg=notes)
    # API fixture records only: four generic records, then attach actual captured flow and a UI-created screenshot.
    records=[
      ("Critical","open","Fixture critical access control","/critical"),
      ("High","needs_verification","Fixture high evidence record","/evidence"),
      ("Medium","verified","Fixture medium remediation","/remediation"),
      ("Low","fixed","Fixture low review record","/review"),
    ]
    existing=page.evaluate("async()=>{const x=await (await fetch('/api/findings')).json();return Object.fromEntries((x.findings||[]).map(f=>[f.title,f.id]))}")
    ids=[]
    for sev,status,title,path in records:
      r=existing.get(title) or page.evaluate("""async v=>{const r=await fetch('/api/findings',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({title:v[2],severity:v[0],status:v[1],source:'human',target:'https://example.com'+v[3],summary:'Generic UI-only fixture with no target data.',impact:'Generic fixture impact.',why:'Generic fixture reason.',fix:'Generic fixture remediation.',retest:'Generic fixture retest.',confidence:'firm',blocks:[{type:'text',role:'baseline',md:'Generic baseline fixture statement.'},{type:'text',role:'action',md:'Generic local fixture action.'},{type:'text',role:'result',md:'Generic fixture result.'}]})});if(!r.ok)throw new Error(await r.text());return (await r.json()).id} """,[sev,status,title,path])
      ids.append(r)
    # Keep the canonical first fixture deliberately incomplete so the Review
    # readiness link has an actual Confidence gap; #2 is the report-ready proof fixture.
    page.evaluate("""async id=>{const r=await fetch('/api/findings/'+id,{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({confidence:''})});if(!r.ok)throw new Error(await r.text())}""",ids[0])
    # Attach the captured flow to the high fixture with provenance.
    flow=page.evaluate("async()=>{const x=await (await fetch('/api/flows?limit=10&includeTools=1')).json();return x.flows?.[0]?.id}")
    has_flow=page.evaluate("async id=>((await (await fetch('/api/findings/'+id)).json()).blocks||[]).some(x=>x.type==='flow')",ids[1])
    if not has_flow: page.evaluate("""async v=>{const r=await fetch('/api/findings/'+v[0]+'/flows',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({flowId:v[1],role:'result',note:'Generic local request and response fixture.',proof:'Confirms generic captured HTTP request and response.',source:'captured_flow',sourceFlowId:v[1]})});if(!r.ok)throw new Error(await r.text())}""",[ids[1],flow])
    page.locator('#tab-findings').click(); page.wait_for_selector('#findList .find-row',timeout=10000)
    page.locator(f'#findList .find-row[data-id="{ids[1]}"]').click(); page.wait_for_selector('#findToggleEdit',timeout=10000)
    page.locator('#findDetail .find-section-nav [data-find-section="evidence"]').click(); page.locator('#findEvidenceRail').wait_for()
    page.locator('#findToggleEdit').click(); page.locator('#findDocActions:not([hidden])').wait_for()
    has_image=page.evaluate("async id=>((await (await fetch('/api/findings/'+id)).json()).blocks||[]).some(x=>x.type==='image')",ids[1])
    if has_image:
      page.locator('#findToggleEdit').click(); srv.shutdown(); srv.server_close(); return {"findings":ids,"flow":flow,"fixture_url":fx}
    page.locator('#findAddImage').click()
    # Browser-produced generic 1x1 PNG; the actual UI upload path assigns operator_upload provenance.
    page.evaluate("""async()=>{const c=document.createElement('canvas');c.width=c.height=1;c.getContext('2d').fillRect(0,0,1,1);const b=await new Promise(ok=>c.toBlob(ok,'image/png'));const d=new DataTransfer();d.items.add(new File([b],'generic-fixture.png',{type:'image/png'}));const i=document.querySelector('#findImageFile');i.files=d.files;i.dispatchEvent(new Event('change',{bubbles:true}))}""")
    page.locator('#findBody .find-doc-image').wait_for(timeout=10000)
    page.locator('#findBody .find-doc-image .find-block-proof').fill('Confirms a generic operator-upload screenshot fixture.')
    page.locator('#findToggleEdit').click(); page.wait_for_selector('#findSummary',state='hidden',timeout=10000)
    srv.shutdown(); srv.server_close()
    return {"findings":ids,"flow":flow,"fixture_url":fx}

def assert_panel(page,p):
    tab=page.locator(f'#tab-{p}')
    if tab.is_visible(): tab.click()
    else: tab.evaluate("button=>button.click()")
    page.wait_for_function("p=>document.querySelector('#panel-'+p)?.classList.contains('active')&&document.querySelector('#tab-'+p)?.getAttribute('aria-selected')==='true'",arg=p)
    if not page.locator(f'#panel-{p}').is_visible(): raise AssertionError('selected panel is not visible')

def assert_native_selects(page):
    bad=page.evaluate("""()=>[...document.querySelectorAll('select')].filter(x=>{const s=getComputedStyle(x),r=x.getBoundingClientRect();return r.width>2&&r.height>2&&s.display!=='none'&&s.visibility!=='hidden'&&Number(s.opacity||1)>0&&s.appearance!=='none'}).map(x=>x.id||x.name||x.outerHTML.slice(0,80))""")
    if bad: raise AssertionError('visible native selects: '+str(bad))

def ready(page):
    page.wait_for_selector('#tabs[aria-busy="false"]', state='attached', timeout=20000)
    page.wait_for_function("[...document.querySelectorAll('.tab')].every(tab=>!tab.disabled)")
    page.wait_for_timeout(300)
    if page.locator('#setupModal').is_visible(): page.locator('#setupSkip').click()

def settled_screenshot(page, path):
    page.evaluate("""async()=>{await Promise.race([Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>null))),new Promise(r=>setTimeout(r,1000))]);await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));}""")
    page.screenshot(path=str(path),animations='disabled')

def probe(args):
    out=Path(args.out).resolve(); shots=out/'screenshots'; shots.mkdir(parents=True,exist_ok=True)
    sess=json.loads(Path(args.session).read_text()) if args.session else {}
    report={"base":args.base,"session":sess,"probe_sha256":sha(__file__),"started_unix":time.time(),"cases":{},"screenshots":[],"engines":{}}
    with sync_playwright() as pw:
      for engine in ENGINES:
        browser=getattr(pw,engine).launch()
        engine_cases={}; console=[]; errors=[]; report['engines'][engine]={"cases":engine_cases,"console_errors":console,"page_errors":errors}
        for w,h in VIEWPORTS:
          context=browser.new_context(viewport={"width":w,"height":h},color_scheme='light',reduced_motion='reduce')
          page=context.new_page(); page.set_default_timeout(10000); page.on('console',lambda m,sink=console: sink.append(m.text) if m.type=='error' else None); page.on('pageerror',lambda e,sink=errors:sink.append(str(e)))
          check(engine_cases,f'{w}x{h}:boot',lambda: (page.goto(args.base,wait_until='domcontentloaded'),ready(page)))
          if not page.locator('#tabs[aria-busy="false"]').count(): context.close(); continue
          check(engine_cases,f'{w}x{h}:no-horizontal-overflow',lambda: (_ for _ in ()).throw(AssertionError('document horizontal overflow')) if page.evaluate('document.documentElement.scrollWidth>document.documentElement.clientWidth') else None)
          check(engine_cases,f'{w}x{h}:no-visible-native-select',lambda:assert_native_selects(page))
          for p in PANELS:
            def panel(p=p):
              assert_panel(page,p); assert_native_selects(page)
              if page.evaluate('document.documentElement.scrollWidth>document.documentElement.clientWidth'): raise AssertionError('document horizontal overflow')
              path=shots/f'{engine}-{w}x{h}-panel-{p}.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[w,h],"surface":'panel:'+p})
            check(engine_cases,f'{w}x{h}:panel:{p}',panel)
          # Settings sections need individual visible rendering and screenshots.
          assert_panel(page,'settings')
          for sec in SECTIONS:
            def section(sec=sec):
              nav=page.locator(f'#setNav button[data-sec="{sec}"]'); nav.click() if nav.is_visible() else nav.evaluate("button=>button.click()"); page.wait_for_function("s=>{const x=document.querySelector('.set-sec[data-sec=\"'+s+'\"]');return x&&getComputedStyle(x).display!=='none'}",arg=sec)
              assert_native_selects(page)
              if page.evaluate('document.documentElement.scrollWidth>document.documentElement.clientWidth'): raise AssertionError('document horizontal overflow')
              path=shots/f'{engine}-{w}x{h}-settings-{sec}.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[w,h],"surface":'settings:'+sec})
            check(engine_cases,f'{w}x{h}:settings:{sec}',section)
          # Light/reduced-motion computed assertion is representative at all three sizes.
          check(engine_cases,f'{w}x{h}:reduced-motion',lambda: (_ for _ in ()).throw(AssertionError('nonzero reduced-motion transition')) if page.evaluate("()=>[...document.querySelectorAll('.panel,.modal-shell,.tab')].some(e=>{const s=getComputedStyle(e);return s.transitionDuration.split(',').some(x=>parseFloat(x)>0.01)||s.animationDuration.split(',').some(x=>parseFloat(x)>0.01)})") else None)
          if w==390:
            check(engine_cases,'390x844:mobile-custom-tool-selector',lambda: (page.locator('#mobileToolSelectUi').click(),page.locator('[role="option"]',has_text='Findings').last.click(),page.wait_for_function("document.querySelector('#panel-findings').classList.contains('active')")))
          context.close(); write(out/'progress.json',report)
        # Findings states and popup paths after fixture persisted by chromium, otherwise on every engine.
        context=browser.new_context(viewport={"width":1440,"height":900},color_scheme='dark',reduced_motion='no-preference'); page=context.new_page(); page.set_default_timeout(10000); page.on('console',lambda m,sink=console: sink.append(m.text) if m.type=='error' else None); page.on('pageerror',lambda e,sink=errors:sink.append(str(e)))
        page.goto(args.base,wait_until='domcontentloaded'); ready(page)
        if engine=='chromium': check(engine_cases,'fixture-population',lambda: report.update({"fixture":seed(page,args.base,sess['proxy'])}))
        check(engine_cases,'dark-computed',lambda: (_ for _ in ()).throw(AssertionError('dark context remains light')) if page.evaluate("getComputedStyle(document.body).backgroundColor==='rgb(255, 255, 255)'") else None)
        assert_panel(page,'findings')
        for mode,selector in [('overview','#findList .find-row'),('evidence','#findList .find-row[data-id]'),('remediation','#findList .find-row[data-id]'),('review','#findList .find-row[data-id]'),('read','#findList .find-row[data-id]'),('edit','#findList .find-row[data-id]')]:
          def finding_mode(mode=mode,selector=selector):
            page.locator(selector).first.click()
            if mode in ('evidence','read','edit'): page.locator('#findDetail .find-section-nav [data-find-section="evidence"]').click()
            if mode=='remediation': page.locator('#findDetail .find-section-nav [data-find-section="remediation"]').click()
            if mode=='review': page.locator('#findDetail .find-section-nav [data-find-section="review"]').click()
            if mode=='edit' and page.locator('#findToggleEdit').is_visible(): page.locator('#findToggleEdit').click(); page.locator('#findDocActions:not([hidden])').wait_for()
            if mode in ('evidence','read','edit'): page.locator('#findEvidenceRail').wait_for()
            if mode=='remediation': page.locator('#find-sec-fix').wait_for()
            if mode=='review': page.locator('#find-sec-review').wait_for()
            path=shots/f'{engine}-findings-{mode}.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[1440,900],"surface":'findings:'+mode})
            if mode=='edit': page.locator('#findToggleEdit').click()
          check(engine_cases,'findings:'+mode,finding_mode)
        # Inline evidence Request/Response and Export dialog are actual UI paths.
        def inline_http():
          page.locator('#findList .find-row[data-id="2"]').click(); page.locator('#findDetail .find-section-nav [data-find-section="evidence"]').click(); b=page.locator('.find-inline-toggle').first; b.click(); page.locator('.find-inline-content').wait_for(); path=shots/f'{engine}-inline-http.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[1440,900],"surface":'inline-http'})
        check(engine_cases,'inline-request-response',inline_http)
        def export_dialog():
          page.locator('#findExportOpen').click(); page.locator('#findExportModal').wait_for(); path=shots/f'{engine}-export-dialog.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[1440,900],"surface":'export-dialog'}); page.locator('#findExportClose').click()
        check(engine_cases,'export-dialog',export_dialog)
        # HTTP popup via captured fixture inspector.
        def http_popup():
          assert_panel(page,'findings'); page.locator('#findList .find-row[data-id="2"]').click(); page.locator('#findDetail .find-section-nav [data-find-section="evidence"]').click(); page.locator('a.find-open-flow').first.click(); page.locator('#flowModal').wait_for(); path=shots/f'{engine}-http-popup.png'; settled_screenshot(page,path); report['screenshots'].append({"path":str(path),"sha256":sha(path),"engine":engine,"viewport":[1440,900],"surface":'http-popup'}); page.locator('#fmClose').click()
        check(engine_cases,'http-popup',http_popup)
        context.close(); write(out/'progress.json',report); browser.close()
    report['finished_unix']=time.time(); report['failures']=[k for e in report['engines'].values() for k,v in e['cases'].items() if v['status']=='fail']; report['unhandled_js_errors']={e:v['page_errors']+v['console_errors'] for e,v in report['engines'].items()}
    write(out/'report.json',report); return 1 if report['failures'] or any(report['unhandled_js_errors'].values()) else 0

if __name__=='__main__':
 p=argparse.ArgumentParser(); sp=p.add_subparsers(dest='cmd',required=True); a=sp.add_parser('serve');a.add_argument('--out',required=True); b=sp.add_parser('probe');b.add_argument('--base',required=True);b.add_argument('--out',required=True);b.add_argument('--session'); ns=p.parse_args(); sys.exit(serve(ns) if ns.cmd=='serve' else probe(ns))
