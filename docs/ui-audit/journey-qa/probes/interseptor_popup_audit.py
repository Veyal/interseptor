#!/usr/bin/env python3
"""Read-only cross-browser popup/disclosure audit for an isolated Interseptor UI.

Example:
  BASE_URL=http://127.0.0.1:51131 OUT_DIR=/tmp/popup-audit \\
    python3 /tmp/interseptor_popup_audit.py
"""
import hashlib, json, os, sys, time
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE=os.environ.get("BASE_URL", "http://127.0.0.1:51131").rstrip("/")
OUT=Path(os.environ.get("OUT_DIR", "/tmp/interseptor-popup-audit"))
OUT.mkdir(parents=True, exist_ok=True)
RUNTIME_DIGEST=os.environ.get("RUNTIME_DIGEST", "ec7fbe819737302c90414f124448658c2b3c540881c60a53a3b7bf3378cd91e7")
VIEWPORTS=((1440,900),(1024,768),(390,844))
MODALS=("flowModal","shortcutsModal","checksModal","codecsModal","oobModal","projModal","authzModal","findGuideModal","findCreateModal","findPickModal","findFlowPickModal","findExportModal","compareModal","decModal","confirmModal","promptModal","setupModal","imgLightbox")

class Audit:
    def __init__(self): self.cases=[]; self.defects=[]
    def add(self, name, status, **detail):
        self.cases.append(dict(name=name,status=status,**detail))
        if status=="fail": self.defects.append(dict(name=name,**detail))
    def call(self,name,fn,**detail):
        try: fn(); self.add(name,"pass",**detail)
        except Exception as e: self.add(name,"fail",error=f"{type(e).__name__}: {e}",**detail)

def wait_ready(page):
    page.goto(BASE+"/", wait_until="domcontentloaded", timeout=15000)
    page.wait_for_timeout(1300)
    page.locator("#fSearch").wait_for(state="visible", timeout=10000)
    # A fresh disposable browser context may show the welcome shell. Escape is
    # its documented non-mutating dismissal; do not Skip (which writes setup state).
    if visible(page,"#setupModal"):
        page.keyboard.press("Escape")
        page.locator("#setupModal").wait_for(state="hidden",timeout=1200)

def visible(page, sel): return page.locator(sel).count()>0 and page.locator(sel).evaluate("e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)")
def modal_open_shell(page, ident):
    page.evaluate("""async id => { const c=await import('/js/core.js'); c.openModal(document.getElementById(id)); }""", ident)
    page.locator("#"+ident).wait_for(state="visible", timeout=2500)
    # surface-enter is intentionally animated; only measure its settled geometry.
    page.wait_for_timeout(350)
def close_escape(page): page.keyboard.press("Escape"); page.wait_for_timeout(50)
def owner_label(page):
    return page.evaluate("""()=>({tab:document.querySelector('.tab.active')?.dataset.tab||'',sec:document.querySelector('#setNav button.on')?.dataset.sec||''})""")

def modal_check(a,page,engine,vp,ident):
    # Direct shared-helper shell check. It creates the real visible dialog and exercises
    # the production registry/focus stack, but does not fetch or start its feature.
    invoker="#tab-proxy" if visible(page,"#tab-proxy") else "#mobileToolSelectUi"
    page.locator(invoker).focus()
    modal_open_shell(page,ident)
    dialog=page.locator("#"+ident+" [role=dialog]")
    def geometry():
        r=dialog.bounding_box(); assert r, "dialog has no bounds"
        assert r["x"]>=-1 and r["y"]>=-1, r
        assert r["x"]+r["width"]<=vp[0]+1 and r["y"]+r["height"]<=vp[1]+1, r
    a.call(f"modal/{engine}/{vp[0]}x{vp[1]}/{ident}/bounds",geometry,entry="direct-shell")
    def focus_loop():
        info=page.evaluate("""id=>{const d=document.querySelector('#'+id+' [role=dialog]');const q='a[href],button,input,select,textarea,[contenteditable="true"],[tabindex]:not([tabindex="-1"])';const v=[...d.querySelectorAll(q)].filter(e=>!e.disabled&&!e.hidden&&e.getClientRects().length); return {count:v.length,first:v[0]?.id||v[0]?.getAttribute('aria-label')||v[0]?.tagName,last:v.at(-1)?.id||v.at(-1)?.getAttribute('aria-label')||v.at(-1)?.tagName};}""",ident)
        assert info["count"]>0, info
        page.evaluate("""id=>{const d=document.querySelector('#'+id+' [role=dialog]');const q='a[href],button,input,select,textarea,[contenteditable="true"],[tabindex]:not([tabindex="-1"])';const v=[...d.querySelectorAll(q)].filter(e=>!e.disabled&&!e.hidden&&e.getClientRects().length);v.at(-1).focus()}""",ident)
        page.keyboard.press("Tab")
        assert page.evaluate("""id=>document.querySelector('#'+id+' [role=dialog]').contains(document.activeElement)""",ident)
        page.evaluate("""id=>{const d=document.querySelector('#'+id+' [role=dialog]');const q='a[href],button,input,select,textarea,[contenteditable="true"],[tabindex]:not([tabindex="-1"])';[...d.querySelectorAll(q)].filter(e=>!e.disabled&&!e.hidden&&e.getClientRects().length)[0].focus()}""",ident)
        page.keyboard.press("Shift+Tab")
        assert page.evaluate("""id=>document.querySelector('#'+id+' [role=dialog]').contains(document.activeElement)""",ident)
    a.call(f"modal/{engine}/{vp[0]}x{vp[1]}/{ident}/tab-trap",focus_loop,entry="direct-shell")
    def escape_return():
        close_escape(page)
        assert not visible(page,"#"+ident), "Escape left dialog visible"
        assert page.evaluate("""sel=>document.activeElement===document.querySelector(sel)""",invoker), page.evaluate("()=>document.activeElement?.id")
    a.call(f"modal/{engine}/{vp[0]}x{vp[1]}/{ident}/escape-return",escape_return,entry="direct-shell")

def click_detail(a,page,engine,label,locator):
    summary=locator.locator("summary")
    if not locator.evaluate("e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)"):
        a.add(f"disclosure/{engine}/{label}","skipped",reason="present but not visible in current generic fixture/UI state",owner=owner_label(page)); return
    def action():
        locator.scroll_into_view_if_needed(); before=locator.evaluate("e=>e.open"); summary.click(); page.wait_for_timeout(35)
        assert locator.evaluate("e=>e.open") != before, "summary did not toggle open state"
        summary.focus(); assert summary.evaluate("e=>document.activeElement===e")
        page.keyboard.press("Space"); page.wait_for_timeout(35)
        assert locator.evaluate("e=>e.open") == before, "Space did not restore disclosure state"
    a.call(f"disclosure/{engine}/{label}",action,owner=owner_label(page))

def visit(page,tab,sec=None):
    if page.locator('.tab.active').get_attribute('data-tab') != tab:
        desktop=page.locator(f'.tab[data-tab="{tab}"]')
        if visible(page,f'.tab[data-tab="{tab}"]'):
            desktop.click()
        else:
            # The mobile rail is a hidden native adapter. Exercise its visible custom
            # trigger/listbox, never select_option() on the adapter.
            trigger=page.locator('#mobileToolSelectUi')
            if trigger.get_attribute('aria-expanded') == 'true':
                trigger.press('Escape'); page.wait_for_timeout(80)
            trigger.click()
            menu=page.locator('#'+trigger.get_attribute('aria-controls'))
            menu.locator('[role="option"][data-value="'+tab+'"]').click()
        page.wait_for_timeout(120)
    if sec:
        desktop_sec=page.locator(f'#setNav button[data-sec="{sec}"]')
        if visible(page,f'#setNav button[data-sec="{sec}"]'):
            desktop_sec.click()
        else:
            trigger=page.locator('#settingsSectionSelectUi')
            if trigger.get_attribute('aria-expanded') == 'true':
                trigger.press('Escape'); page.wait_for_timeout(80)
            trigger.click()
            page.locator('#'+trigger.get_attribute('aria-controls')).locator('[role="option"][data-value="'+sec+'"]').click()
        page.wait_for_timeout(120)

def test_disclosures(a,page,engine):
    # Every record includes the summary text plus DOM occurrence. Visits make hidden
    # panel content actually visible; we never toggle by setting .open directly.
    routes=[("proxy",None),("intercept",None),("intruder",None),("findings",None),
      ("settings","proxy"),("settings","tls"),("settings","api"),("settings","session")]
    seen=set(); observed={}
    for tab,sec in routes:
        visit(page,tab,sec)
        if tab=="settings" and sec=="api":
            # Discover MCP disclosures through the visible settings section, only UI route.
            btn=page.locator('#apiSub button[data-s="mcp"]')
            if btn.count(): btn.click(); page.wait_for_timeout(180)
        rows=page.locator("details").evaluate_all("""els=>els.map((e,i)=>({i,id:e.id,summary:e.querySelector('summary')?.textContent?.trim()||'',cls:e.className,text:e.textContent?.trim().slice(0,120)||'',hidden:e.hidden,visible:!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)}))""")
        for row in rows:
            key=(row["id"],row["summary"],row["cls"],row["text"])
            observed[key]=row
            if key in seen: continue
            if not row["visible"]: continue
            seen.add(key)
            loc=page.locator("details").nth(row["i"])
            label=f"{row['i']}:{row['id'] or row['summary']}"
            click_detail(a,page,engine,label,loc)
    # Findings detail is rendered only after a record is selected. Exercise its
    # actual disclosure without saving or changing the fixture.
    visit(page,"findings")
    record=page.locator('#findList [data-id="1"]')
    if record.count():
        record.click(); page.wait_for_timeout(100)
        rows=page.locator("details").evaluate_all("""els=>els.map((e,i)=>({i,id:e.id,summary:e.querySelector('summary')?.textContent?.trim()||'',cls:e.className,text:e.textContent?.trim().slice(0,120)||'',visible:!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)}))""")
        for row in rows:
            key=(row["id"],row["summary"],row["cls"],row["text"]); observed[key]=row
            if key in seen or not row["visible"]: continue
            seen.add(key); click_detail(a,page,engine,f"{row['i']}:{row['id'] or row['summary']}",page.locator("details").nth(row["i"]))
    # Visit the API-keys subsection as well as MCP, whose async documentation
    # contributes three more disclosures.
    visit(page,"settings","api")
    keybtn=page.locator('#apiSub button[data-s="keys"]')
    if keybtn.count():
        keybtn.click(); page.wait_for_timeout(80)
        rows=page.locator("details").evaluate_all("""els=>els.map((e,i)=>({i,id:e.id,summary:e.querySelector('summary')?.textContent?.trim()||'',cls:e.className,text:e.textContent?.trim().slice(0,120)||'',visible:!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)}))""")
        for row in rows:
            key=(row["id"],row["summary"],row["cls"],row["text"]); observed[key]=row
            if key in seen or not row["visible"]: continue
            seen.add(key); click_detail(a,page,engine,f"{row['i']}:{row['id'] or row['summary']}",page.locator("details").nth(row["i"]))
    # Checks carries its own disclosure inside a real modal; enter it through its UI.
    visit(page,"scanner")
    page.locator("#checksBtn").click(); page.wait_for_timeout(180)
    rows=page.locator("details").evaluate_all("""els=>els.map((e,i)=>({i,id:e.id,summary:e.querySelector('summary')?.textContent?.trim()||'',cls:e.className,text:e.textContent?.trim().slice(0,120)||'',visible:!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)}))""")
    for row in rows:
        key=(row["id"],row["summary"],row["cls"],row["text"])
        observed[key]=row
        if key in seen: continue
        if not row["visible"]: continue
        seen.add(key); click_detail(a,page,engine,f"{row['i']}:{row['id'] or row['summary']}",page.locator("details").nth(row["i"]))
    close_escape(page)
    for key,row in observed.items():
        if key not in seen:
            a.add(f"disclosure/{engine}/{row['i']}:{row['id'] or row['summary']}","skipped",reason="not reachable in supplied generic fixture/UI state",owner=owner_label(page))
    return seen

def feature_entries(a,page,engine):
    # Full operator entries where the isolated generic fixture makes them reachable.
    cases=[("findGuideModal","#findGuide"),("findExportModal","#findExportOpen"),("findCreateModal","#findNew"),("checksModal","#checksBtn"),("codecsModal","#codecsBtn"),("projModal","#mobileProjectBtn")]
    for ident,trigger in cases:
        def act(ident=ident,trigger=trigger):
            if ident=="checksModal": visit(page,"scanner")
            elif ident in ("findGuideModal","findExportModal","findCreateModal"): visit(page,"findings")
            page.locator(trigger).click(); page.locator("#"+ident).wait_for(state="visible",timeout=2500); close_escape(page)
        a.call(f"modal/{engine}/full-entry/{ident}",act,entry="full-feature")
    # Keyboard shortcut is a real entry; setup is a command-palette entry, both non-mutating.
    def shortcuts():
        page.keyboard.press("?"); page.locator("#shortcutsModal").wait_for(state="visible"); close_escape(page)
    a.call(f"modal/{engine}/full-entry/shortcutsModal",shortcuts,entry="full-feature")
    def setup():
        page.keyboard.press("Control+k"); page.locator("#cmdkInput").fill("Run setup wizard"); page.keyboard.press("Enter"); page.locator("#setupModal").wait_for(state="visible"); close_escape(page)
    a.call(f"modal/{engine}/full-entry/setupModal",setup,entry="full-feature")
    # The fixture's 1px operator-upload image is opened through its click handler.
    def image():
        visit(page,"findings"); page.locator('#findList [data-id="2"]').click(); page.wait_for_timeout(120)
        page.get_by_role('link',name='Evidence',exact=True).click(); page.wait_for_timeout(80)
        img=page.locator("img.find-doc-img, img.md-img").first
        assert img.count(), "fixture image unavailable"; img.click(); page.locator("#imgLightbox").wait_for(state="visible"); close_escape(page)
    a.call(f"modal/{engine}/full-entry/imgLightbox",image,entry="full-feature",fixture="operator-upload 1px image")

def custom_control_cases(a,page,engine):
    # Custom select trigger at a bottom viewport must open a listbox inside the screen.
    visit(page,"findings")
    def select_bottom():
        t=page.locator('#findFilterSeverityUi'); assert t.count(), "custom select trigger missing"
        t.scroll_into_view_if_needed(); t.click(); menu=page.locator('#'+t.get_attribute('aria-controls')); r=menu.bounding_box(); assert r and r['y']>=0 and r['y']+r['height']<=page.viewport_size['height']+1, r; page.keyboard.press('Escape')
    a.call(f"control/{engine}/custom-select-bottom",select_bottom)
    def col_picker():
        visit(page,"proxy"); b=page.locator('#colPickerBtn'); b.click(); p=page.locator('#colPicker'); assert visible(page,'#colPicker'); p.locator('input').first.focus(); page.keyboard.press('Escape'); assert not visible(page,'#colPicker'); assert page.evaluate("()=>document.activeElement===document.querySelector('#colPickerBtn')")
    a.call(f"control/{engine}/column-picker-escape-focus",col_picker)
    def tooltip():
        # At touch-sized widths hints intentionally require focus-visible. Tab from
        # the preceding action rather than calling focus(), which is not keyboard focus.
        page.evaluate('()=>document.activeElement?.blur()')
        el=page.locator('#findGuide')
        for _ in range(80):
            page.keyboard.press('Tab')
            if el.evaluate('e=>document.activeElement===e'): break
        assert el.evaluate('e=>document.activeElement===e'), page.evaluate('()=>document.activeElement?.id')
        page.wait_for_timeout(80); assert page.locator('#uiTooltip').count() and visible(page,'#uiTooltip'); page.keyboard.press('Escape'); assert not visible(page,'#uiTooltip')
    a.call(f"control/{engine}/tooltip-keyboard-escape",tooltip)
    def toasts():
        page.evaluate("""async()=>{const c=await import('/js/core.js');c.toast('generic fixture toast one');c.toast('generic fixture toast two');}"""); page.wait_for_timeout(30); assert page.locator('#toast').count() and visible(page,'#toast')
    a.call(f"control/{engine}/toast-stack",toasts)

def run_engine(a,p,engine):
    browser=getattr(p,engine).launch()
    for vp in VIEWPORTS:
        page=browser.new_page(viewport={"width":vp[0],"height":vp[1]})
        try:
            wait_ready(page)
            for ident in MODALS: modal_check(a,page,engine,vp,ident)
            # One per engine screenshots a representative modal surface at each mandated viewport.
            modal_open_shell(page,"findGuideModal"); page.screenshot(path=str(OUT/f"{engine}-{vp[0]}x{vp[1]}-guide.png")); close_escape(page)
        except Exception as e: a.add(f"engine/{engine}/{vp[0]}x{vp[1]}/bootstrap","fail",error=f"{type(e).__name__}: {e}")
        finally: page.close()
    page=browser.new_page(viewport={"width":390,"height":844})
    try:
        wait_ready(page); feature_entries(a,page,engine); disclosures=test_disclosures(a,page,engine); custom_control_cases(a,page,engine)
        a.add(f"inventory/{engine}/details","pass",count=len(disclosures),note="unique DOM occurrence inventory across actual UI routes")
    except Exception as e: a.add(f"engine/{engine}/feature-disclosures","fail",error=f"{type(e).__name__}: {e}")
    finally: page.close(); browser.close()

if __name__=="__main__":
    a=Audit(); started=time.time()
    with sync_playwright() as p:
        for engine in ("chromium","firefox","webkit"): run_engine(a,p,engine)
    result={"base_url":BASE,"runtime_digest":RUNTIME_DIGEST,"script_sha256":hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),"started":started,"finished":time.time(),"cases":a.cases,"defects":a.defects}
    (OUT/"popup-audit.json").write_text(json.dumps(result,indent=2)+"\n")
    print(json.dumps({"out":str(OUT),"cases":len(a.cases),"failed":len(a.defects),"script_sha256":result["script_sha256"]},indent=2))
    sys.exit(1 if a.defects else 0)
