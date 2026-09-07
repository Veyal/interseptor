#!/usr/bin/env python3
"""Fresh-context repairs for portal/menu and WebKit shortcut evidence."""
import hashlib,json,os
from pathlib import Path
from playwright.sync_api import sync_playwright
BASE=os.environ['BASE_URL'].rstrip('/'); OUT=Path(os.environ.get('OUT_PATH','/tmp/interseptor-popup-repairs.json')); DIGEST=os.environ['RUNTIME_DIGEST']; cases=[]
def add(name,fn):
  try:fn();cases.append({'name':name,'status':'pass'})
  except Exception as e:cases.append({'name':name,'status':'fail','error':f'{type(e).__name__}: {e}'})
def boot(page):
  page.goto(BASE,wait_until='domcontentloaded');page.wait_for_timeout(1250);page.keyboard.press('Escape')
def pick(page,trigger_id,value):
  t=page.locator('#'+trigger_id);t.scroll_into_view_if_needed();page.wait_for_timeout(80);t.click();page.locator('#'+t.get_attribute('aria-controls')).locator('[role="option"][data-value="'+value+'"]').click()
with sync_playwright() as p:
  for engine in ('chromium','firefox','webkit'):
    b=getattr(p,engine).launch();page=b.new_page(viewport={'width':1024,'height':768});page.set_default_timeout(7000);boot(page)
    def upstream():
      page.locator('.tab[data-tab="settings"]').click();page.locator('#setNav button[data-sec="proxy"]').click();page.wait_for_timeout(80);pick(page,'setUpstreamSchemeUi','https');page.wait_for_timeout(50);d=page.locator('#upstreamProxyAdvanced');assert d.is_visible();s=d.locator('summary');before=d.evaluate('e=>e.open');s.click();assert d.evaluate('e=>e.open')!=before;s.focus();page.keyboard.press('Space');assert d.evaluate('e=>e.open')==before;pick(page,'setUpstreamSchemeUi','direct');assert not d.is_visible()
    add(f'{engine}/upstream-advanced-fresh-ui-only',upstream);page.close()
    page=b.new_page(viewport={'width':1024,'height':768});page.set_default_timeout(7000);boot(page)
    def packs():
      page.locator('.tab[data-tab="scanner"]').click();page.locator('#checksBtn').click();page.wait_for_timeout(160);d=page.locator('#checksPacks');assert d.is_visible();s=d.locator('summary');before=d.evaluate('e=>e.open');s.click();assert d.evaluate('e=>e.open')!=before;s.focus();page.keyboard.press('Space');assert d.evaluate('e=>e.open')==before;page.keyboard.press('Escape')
    add(f'{engine}/checks-packs-fresh',packs);page.close()
    if engine=='webkit':
      page=b.new_page(viewport={'width':390,'height':844});page.set_default_timeout(7000);boot(page)
      def shortcuts_cmdk():
        page.keyboard.press('Control+k');page.locator('#cmdkInput').fill('Shortcuts');page.keyboard.press('Enter');page.locator('#shortcutsModal').wait_for(state='visible');page.keyboard.press('Escape')
      add('webkit/shortcuts-command-palette-entry',shortcuts_cmdk);page.close()
    b.close()
result={'runtime_digest':DIGEST,'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'cases':cases};OUT.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'out':str(OUT),'cases':len(cases),'failed':sum(c['status']=='fail' for c in cases),'script_sha256':result['script_sha256']},indent=2))
