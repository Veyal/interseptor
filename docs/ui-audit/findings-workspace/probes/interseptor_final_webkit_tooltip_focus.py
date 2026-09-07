#!/usr/bin/env python3
"""Read-only WebKit focus-event tooltip probe.

This complements the documented macOS WebKit Tab-accessibility limitation by
testing the production focus event directly. Requires BASE_URL and
RUNTIME_DIGEST; OUT_PATH is optional.
"""
import hashlib, json, os
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE=os.environ['BASE_URL'].rstrip('/')
DIGEST=os.environ['RUNTIME_DIGEST']
OUT=Path(os.environ.get('OUT_PATH','/tmp/interseptor-webkit-tooltip-focus.json'))

def main():
    with sync_playwright() as p:
        browser=p.webkit.launch(); page=browser.new_page(viewport={'width':1024,'height':768}); page.set_default_timeout(7000)
        page.goto(BASE+'/',wait_until='domcontentloaded'); page.wait_for_timeout(1300); page.keyboard.press('Escape')
        page.locator('.tab[data-tab="findings"]').click()
        guide=page.locator('#findGuide'); guide.focus(); page.wait_for_timeout(80)
        assert page.evaluate('e=>document.activeElement===e',guide.element_handle())
        tooltip=page.locator('#uiTooltip'); assert tooltip.is_visible()
        page.keyboard.press('Escape'); assert not tooltip.is_visible(); browser.close()
    result={'runtime_digest':DIGEST,'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'case':{'name':'control/webkit/tooltip-focus-event-escape','status':'pass','entry':'direct-focus-event','note':'Tab traversal is separately skipped by macOS WebKit keyboard-access profile'}}
    OUT.write_text(json.dumps(result,indent=2)+'\n'); print(json.dumps({'out':str(OUT),**result['case'],'script_sha256':result['script_sha256']},indent=2))

if __name__=='__main__': main()
