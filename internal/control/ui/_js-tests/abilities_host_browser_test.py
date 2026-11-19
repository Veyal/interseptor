"""Isolated rendered host tests: python3 internal/control/ui/_js-tests/abilities_host_browser_test.py."""
import os
from pathlib import Path
import unittest
from urllib.parse import quote, urlparse

from playwright.sync_api import expect, sync_playwright

UI = Path(__file__).resolve().parents[1]
SHELL = '''<!doctype html><div id="tabs"></div><main id="canvasPool"></main><div id="inspector"></div>
<script type="module">
import * as host from '/js/abilities-host.js';
import {collectInjectedMenus} from '/js/abilities-model.js';
import {runSseHooks} from '/js/shell-hooks.js';
const ab = {id: 'sample', scope: 'project', digest: 'sha256:' + 'a'.repeat(64),
  trusted: true, enabled: true, manifest: {
    menus: [{id:'panel', label:'Sample', entry:'ui/panel.js'}],
    inspectorTabs: [{id:'sample', label:'Sample', entry:'ui/panel.js'}],
  }};
let list = [ab];
host.configureAbilityHost({ getAbilities: () => list, isLoaded: () => true });
const sync = () => host.syncInjectedMenus(collectInjectedMenus(list));
const panel = () => host.mountAbilityPanel('ability:sample:panel');
const inspector = flow => host.abilityInspectorTabs({id: flow})[0].mount(document.querySelector('#inspector'), {id: flow});
window.qa = {sync, panel, inspector, runSseHooks, enable(on) {list = [{...ab, enabled: on}]; sync();}};
sync();
</script>'''
ASSET = '''import { label } from './label.js';
export async function mount(root, ctx) {
  if (ctx.flow?.id === 1) {
    await new Promise(resolve => { window.finishOld = resolve; });
  }
  const el = document.createElement('p');
  el.dataset.testid = 'sample-result';
  el.textContent = label + ' ' + ctx.esc('<ready>') + ' ' + (ctx.flow?.id ?? 'panel');
  root.replaceChildren(el);
  ctx.onSse('fixture', () => { window.ticks = (window.ticks || 0) + 1; });
}
export function unmount(root) { root.replaceChildren(); }
'''


class AbilitiesHostBrowserTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.addClassCleanup(cls.playwright.stop)
        path = os.getenv('INTERSEPTOR_CHROME')
        mac_chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
        if not path and mac_chrome.is_file():
            path = str(mac_chrome)
        cls.browser = cls.playwright.chromium.launch(executable_path=path, headless=True)
        cls.addClassCleanup(cls.browser.close)

    def setUp(self):
        self.page = self.browser.new_page()
        self.addCleanup(self.page.close)
        self.page.set_default_timeout(4000)
        self.page.route('**/*', self.serve)
        self.page.goto('http://abilities-qa.test/')
        self.page.wait_for_function('window.qa')

    def serve(self, route):
        url = urlparse(route.request.url)
        path = url.path
        if url.netloc == 'abilities-qa.test':
            if path == '/':
                return route.fulfill(content_type='text/html', body=SHELL)
            if path.startswith('/js/') and Path(path).name == path[4:]:
                source = UI / path[1:]
                if source.is_file():
                    return route.fulfill(content_type='text/javascript', body=source.read_text())
            rev_prefix = f'/abilities/sample/{quote("sha256:" + "a" * 64, safe="")}/ui/'
            if path.startswith(rev_prefix):
                body = {'panel.js': ASSET, 'label.js': "export const label = 'Fixture';"}.get(Path(path).name)
                if body:
                    return route.fulfill(content_type='text/javascript', body=body)
        route.fulfill(status=404, body='Not found')

    def test_renders_panel_with_sdk_and_relative_import_after_reenable(self):
        self.page.evaluate('qa.panel()')
        result = self.page.get_by_test_id('sample-result')
        expect(result).to_have_text('Fixture &lt;ready&gt; panel', timeout=2000)
        self.page.evaluate('qa.enable(false)')
        expect(result).to_have_count(0)
        expect(self.page.get_by_role('tab', name='Sample')).to_have_count(0)
        self.page.evaluate('() => { qa.enable(true); return qa.panel(); }')
        expect(result).to_have_text('Fixture &lt;ready&gt; panel')

    def test_late_inspector_mount_and_disposer_cannot_touch_current_slot(self):
        self.page.evaluate('() => { window.older = qa.inspector(1); }')
        self.page.wait_for_function('() => typeof window.finishOld === "function"')
        self.page.evaluate('() => { qa.enable(false); qa.enable(true); }')
        self.page.evaluate('async () => { await qa.inspector(2); }')
        result = self.page.get_by_test_id('sample-result')
        expect(result).to_have_text('Fixture &lt;ready&gt; 2')
        self.page.evaluate('async () => { finishOld(); (await older)(); }')
        expect(result).to_have_text('Fixture &lt;ready&gt; 2')
        self.page.evaluate("qa.runSseHooks({type:'fixture'})")
        self.assertEqual(self.page.evaluate('window.ticks'), 1)


if __name__ == '__main__':
    unittest.main()
