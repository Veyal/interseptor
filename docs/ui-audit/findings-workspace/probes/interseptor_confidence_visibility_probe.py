#!/usr/bin/env python3
"""Read-only regression probe for Findings Review's Confidence readiness link."""
import hashlib,json,os,sys
from pathlib import Path
from playwright.sync_api import sync_playwright
base=os.environ['BASE_URL'].rstrip('/')
runtime_digest=os.environ['RUNTIME_DIGEST']
out=Path(os.environ.get('OUT_PATH','/tmp/interseptor-confidence-visibility.json'))
finding_id=os.environ.get('FINDING_ID','1')
def main():
  with sync_playwright() as p:
    b=p.chromium.launch();page=b.new_page(viewport={'width':390,'height':844});page.set_default_timeout(5000)
    page.goto(base+'/',wait_until='domcontentloaded');page.wait_for_timeout(1300)
    if page.locator('#setupModal').evaluate('e=>!!e.getClientRects().length'): page.keyboard.press('Escape')
    trigger=page.locator('#mobileToolSelectUi');trigger.click()
    page.locator('#'+trigger.get_attribute('aria-controls')).get_by_role('option',name='Findings',exact=True).click();page.wait_for_timeout(350)
    page.locator('#findList [data-id="'+finding_id+'"]').click();page.wait_for_timeout(250)
    page.get_by_role('link',name='Review',exact=True).click();page.wait_for_timeout(50)
    page.locator('[data-gap="confidence"]').click();page.wait_for_timeout(100)
    result=page.evaluate('''()=>{const e=document.activeElement,r=e.getBoundingClientRect();return {active:e.id,role:e.getAttribute('role'),top:r.top,bottom:r.bottom,viewport:innerHeight,fullyVisible:r.top>=0&&r.bottom<=innerHeight,reviewTop:document.querySelector('#find-sec-review')?.getBoundingClientRect().top,detailScroll:document.querySelector('#findDetail')?.scrollTop}}''')
    page.screenshot(path=str(out.with_suffix('.png')));b.close()
  result.update(base_url=base,runtime_digest=runtime_digest,script_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
  out.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2));return 0 if result['fullyVisible'] else 1
if __name__=='__main__':sys.exit(main())
