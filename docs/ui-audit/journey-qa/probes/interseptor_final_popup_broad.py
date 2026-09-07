#!/usr/bin/env python3
"""Read-only final popup/disclosure audit for a held isolated Interseptor candidate.

Requires BASE_URL.  It uses only visible custom controls; the upstream scheme
is returned to Direct without pressing Save, so it makes no server mutation.
"""
import hashlib, importlib.util, json, os
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE=os.environ['BASE_URL'].rstrip('/')
OUT=Path(os.environ.get('OUT_PATH','/tmp/interseptor-popup-broad.json'))
IMAGE_PATH=os.environ.get('IMAGE_PATH',str(OUT.with_name(OUT.stem+'-actual-image.png')))
DIGEST=os.environ['RUNTIME_DIGEST']
PROBE_PATH=Path(__file__).with_name('interseptor_popup_audit.py')
spec=importlib.util.spec_from_file_location('popup_audit',PROBE_PATH)
audit_mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(audit_mod)

def visible(locator): return locator.count() and locator.evaluate('e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)')
def add(cases,name,fn,**detail):
    try: fn(); cases.append(dict(name=name,status='pass',**detail))
    except Exception as exc: cases.append(dict(name=name,status='fail',error=f'{type(exc).__name__}: {exc}',**detail))
def mobile_tab(page,value):
    trigger=page.locator('#mobileToolSelectUi'); trigger.click()
    page.locator('#'+trigger.get_attribute('aria-controls')).locator('[role="option"][data-value="'+value+'"]').click()
def pick(page,trigger_id,value):
    trigger=page.locator('#'+trigger_id); trigger.scroll_into_view_if_needed(); page.wait_for_timeout(80); trigger.click()
    page.locator('#'+trigger.get_attribute('aria-controls')).locator('[role="option"][data-value="'+value+'"]').click()

def main():
    cases=[]
    with sync_playwright() as p:
        for engine in ('chromium','firefox','webkit'):
            browser=getattr(p,engine).launch()
            # Actual feature entries, including the operator-upload image fixture.
            page=browser.new_page(viewport={'width':390,'height':844}); page.set_default_timeout(7000)
            audit_mod.wait_ready(page)
            for ident,trigger in [('findGuideModal','#findGuide'),('findExportModal','#findExportOpen'),('findCreateModal','#findNew'),('checksModal','#checksBtn'),('codecsModal','#codecsBtn'),('projModal','#mobileProjectBtn')]:
                def feature(ident=ident,trigger=trigger):
                    if ident=='checksModal': mobile_tab(page,'scanner')
                    elif ident.startswith('find'): mobile_tab(page,'findings')
                    page.locator(trigger).click(); page.locator('#'+ident).wait_for(state='visible'); page.keyboard.press('Escape')
                add(cases,f'feature/{engine}/{ident}',feature,entry='full-feature')
            def shortcuts():
                if engine=='webkit':
                    page.keyboard.press('Control+k'); page.locator('#cmdkInput').fill('Shortcuts'); page.keyboard.press('Enter')
                else: page.keyboard.press('?')
                page.locator('#shortcutsModal').wait_for(state='visible'); page.keyboard.press('Escape')
            add(cases,f'feature/{engine}/shortcutsModal',shortcuts,entry='full-feature')
            def setup():
                page.keyboard.press('Control+k'); page.locator('#cmdkInput').fill('Run setup wizard'); page.keyboard.press('Enter'); page.locator('#setupModal').wait_for(state='visible'); page.keyboard.press('Escape')
            add(cases,f'feature/{engine}/setupModal',setup,entry='full-feature')
            def image():
                mobile_tab(page,'findings'); page.locator('#findList [data-id="2"]').click(); page.wait_for_timeout(100)
                page.get_by_role('link',name='Evidence',exact=True).click(); page.wait_for_timeout(60)
                img=page.locator('img.find-doc-img, img.md-img').first; assert img.count(), 'operator-upload image unavailable'
                img.click(); page.locator('#imgLightbox').wait_for(state='visible')
                if engine=='chromium': page.screenshot(path=IMAGE_PATH,animations='disabled')
                page.keyboard.press('Escape')
            add(cases,f'feature/{engine}/imgLightbox',image,entry='full-feature',fixture='generic operator_upload image')
            page.close()
            # All normally reachable disclosures, through their owning UI panels.
            page=browser.new_page(viewport={'width':1024,'height':768}); page.set_default_timeout(7000); audit_mod.wait_ready(page)
            sub=audit_mod.Audit(); audit_mod.test_disclosures(sub,page,engine); cases.extend(sub.cases); page.close()
            # Conditional Tags and HTTPS advanced-trust details, via visible controls.
            page=browser.new_page(viewport={'width':1024,'height':768}); page.set_default_timeout(7000); audit_mod.wait_ready(page)
            def tags():
                page.locator('.tab[data-tab="findings"]').click(); page.locator('#findList [data-id="4"]').click(); page.wait_for_timeout(80)
                d=page.locator('#findTagsDisclosure'); assert d.is_visible(); s=d.locator('summary'); before=d.evaluate('e=>e.open'); s.click(); assert d.evaluate('e=>e.open')!=before; s.focus(); page.keyboard.press('Space'); assert d.evaluate('e=>e.open')==before
            add(cases,f'disclosure/{engine}/tags',tags)
            def upstream():
                page.locator('.tab[data-tab="settings"]').click(); page.locator('#setNav button[data-sec="proxy"]').click(); page.wait_for_timeout(80)
                pick(page,'setUpstreamSchemeUi','https'); page.wait_for_timeout(50); d=page.locator('#upstreamProxyAdvanced'); assert d.is_visible(); s=d.locator('summary'); before=d.evaluate('e=>e.open'); s.click(); assert d.evaluate('e=>e.open')!=before; s.focus(); page.keyboard.press('Space'); assert d.evaluate('e=>e.open')==before; pick(page,'setUpstreamSchemeUi','direct'); assert not d.is_visible()
            add(cases,f'disclosure/{engine}/upstream-advanced',upstream,entry='ui-only-no-save')
            page.close()
            # Mobile custom controls and keyboard/touch hint behavior in a fresh context.
            page=browser.new_page(viewport={'width':390,'height':844}); page.set_default_timeout(7000); audit_mod.wait_ready(page)
            def custom_select():
                mobile_tab(page,'findings'); t=page.locator('#findFilterSeverityUi'); t.scroll_into_view_if_needed(); t.click(); m=page.locator('#'+t.get_attribute('aria-controls')); r=m.bounding_box(); assert r and r['y']>=0 and r['y']+r['height']<=844+1, r; page.keyboard.press('Escape')
            add(cases,f'control/{engine}/custom-select-bottom',custom_select)
            def columns():
                mobile_tab(page,'proxy'); b=page.locator('#colPickerBtn'); b.click(); p=page.locator('#colPicker'); assert p.is_visible(); p.locator('input').first.focus(); page.keyboard.press('Escape'); assert not p.is_visible(); assert page.evaluate("()=>document.activeElement===document.querySelector('#colPickerBtn')")
            add(cases,f'control/{engine}/column-picker-escape-focus',columns)
            def toast():
                page.evaluate("""async()=>{const c=await import('/js/core.js');c.toast('generic fixture one');c.toast('generic fixture two')}"""); assert visible(page.locator('#toast'))
            add(cases,f'control/{engine}/toast-stack',toast,entry='direct-component')
            def tooltip():
                mobile_tab(page,'findings'); page.evaluate('()=>document.activeElement?.blur()'); guide=page.locator('#findGuide')
                for _ in range(80):
                    page.keyboard.press('Tab')
                    if guide.evaluate('e=>document.activeElement===e'): break
                if engine=='webkit':
                    # macOS WebKit Playwright's tab mode skips visible buttons; record
                    # the environment limitation, after proving the trigger is present.
                    assert guide.is_visible() and guide.get_attribute('tabindex') in (None,'0')
                    cases.append(dict(name=f'control/{engine}/tooltip-keyboard',status='skipped',reason='macOS WebKit Playwright keyboard-access mode omits buttons from Tab traversal'))
                    return
                assert guide.evaluate('e=>document.activeElement===e'); page.wait_for_timeout(70); assert visible(page.locator('#uiTooltip')); page.keyboard.press('Escape'); assert not visible(page.locator('#uiTooltip'))
            add(cases,f'control/{engine}/tooltip-keyboard-escape',tooltip)
            page.close(); browser.close()
    result={'runtime_digest':DIGEST,'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'shared_probe_sha256':hashlib.sha256(PROBE_PATH.read_bytes()).hexdigest(),'image_path':IMAGE_PATH,'cases':cases}
    OUT.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({'out':str(OUT),'cases':len(cases),'failed':sum(x['status']=='fail' for x in cases),'skipped':sum(x['status']=='skipped' for x in cases),'script_sha256':result['script_sha256'],'shared_probe_sha256':result['shared_probe_sha256']},indent=2))
if __name__=='__main__': main()
