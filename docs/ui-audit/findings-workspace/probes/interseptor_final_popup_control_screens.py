#!/usr/bin/env python3
"""Capture read-only visual evidence for custom dropdowns and column picker.

Requires BASE_URL, RUNTIME_DIGEST, and OUT_DIR.  Uses only visible controls.
"""
import hashlib, json, os
from pathlib import Path
from playwright.sync_api import sync_playwright
import importlib.util

BASE = os.environ['BASE_URL'].rstrip('/')
DIGEST = os.environ['RUNTIME_DIGEST']
OUT = Path(os.environ['OUT_DIR']); OUT.mkdir(parents=True, exist_ok=True)
PROBE_PATH = Path(__file__).with_name('interseptor_popup_audit.py')
spec = importlib.util.spec_from_file_location('popup_audit', PROBE_PATH)
audit = importlib.util.module_from_spec(spec); spec.loader.exec_module(audit)

def settle(page):
    page.evaluate('()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))')
    page.wait_for_timeout(250)

def main():
    paths=[]; facts=[]
    with sync_playwright() as p:
        browser=p.chromium.launch()
        # Phone: actual Findings severity listbox, captured after settled geometry.
        page=browser.new_page(viewport={'width':390,'height':844}); page.set_default_timeout(7000); audit.wait_ready(page)
        trigger=page.locator('#mobileToolSelectUi'); trigger.click()
        page.locator('#'+trigger.get_attribute('aria-controls')).locator('[role="option"][data-value="findings"]').click()
        severity=page.locator('#findFilterSeverityUi'); severity.scroll_into_view_if_needed(); severity.click(); settle(page)
        menu=page.locator('#'+severity.get_attribute('aria-controls')); rect=menu.bounding_box()
        path=OUT/'phone-findings-severity-dropdown.png'; page.screenshot(path=str(path),animations='disabled'); paths.append(str(path)); facts.append({'case':'phone-findings-severity-dropdown','menuRect':rect,'viewport':[390,844]})
        page.keyboard.press('Escape')
        # Phone: actual Proxy column picker with its first checkbox focused.
        trigger=page.locator('#mobileToolSelectUi'); trigger.click()
        page.locator('#'+trigger.get_attribute('aria-controls')).locator('[role="option"][data-value="proxy"]').click()
        picker_button=page.locator('#colPickerBtn'); picker_button.click(); page.locator('#colPicker').wait_for(state='visible'); page.locator('#colPicker input').first.focus(); settle(page)
        path=OUT/'phone-column-picker.png'; page.screenshot(path=str(path),animations='disabled'); paths.append(str(path)); facts.append({'case':'phone-column-picker','pickerRect':page.locator('#colPicker').bounding_box(),'viewport':[390,844]})
        page.keyboard.press('Escape'); page.close()
        # Desktop upstream dropdown is an actual custom select. Generic fixtures have
        # no Android/iOS device choices, so subtitle-bearing device options cannot be shown.
        page=browser.new_page(viewport={'width':1440,'height':900}); page.set_default_timeout(7000); audit.wait_ready(page)
        page.locator('.tab[data-tab="settings"]').click(); page.locator('#setNav button[data-sec="proxy"]').click()
        upstream=page.locator('#setUpstreamSchemeUi'); upstream.scroll_into_view_if_needed(); page.wait_for_timeout(80); upstream.click(); settle(page)
        menu=page.locator('#'+upstream.get_attribute('aria-controls'))
        subtitles=menu.locator('.ui-select-opt-sub').count()
        path=OUT/'desktop-upstream-scheme-dropdown.png'; page.screenshot(path=str(path),animations='disabled'); paths.append(str(path)); facts.append({'case':'desktop-upstream-scheme-dropdown','menuRect':menu.bounding_box(),'subtitleOptionCount':subtitles,'viewport':[1440,900]})
        page.keyboard.press('Escape'); page.close(); browser.close()
    result={'base_url':BASE,'runtime_digest':DIGEST,'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'shared_probe_sha256':hashlib.sha256(PROBE_PATH.read_bytes()).hexdigest(),'screenshots':paths,'facts':facts}
    report=OUT/'control-screens.json'; report.write_text(json.dumps(result,indent=2)+'\n'); print(json.dumps({'out':str(report),'screenshots':paths,'facts':facts},indent=2))

if __name__=='__main__': main()
