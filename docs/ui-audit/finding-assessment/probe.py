#!/usr/bin/env python3
"""Focused disposable browser QA for Interseptor findings issues #65--#67.

Uses the repository's managed retained-FD candidate helper. All records and
traffic are local generic fixtures. Output is deliberately outside the repo.
"""
from __future__ import annotations

import base64
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import traceback
import time
from pathlib import Path
from urllib.request import Request, urlopen

from playwright.sync_api import sync_playwright

ROOT = Path(os.environ.get('INTERSEPTOR_REPO', Path.cwd())).resolve()
OUT = Path(os.environ.get('QA_OUT', '/tmp/interseptor-assessment-browser-qa'))
ENGINES = tuple(os.environ.get('QA_ENGINES', 'chromium,firefox,webkit').split(','))
PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLMeAAAAABJRU5ErkJggg==')

spec = importlib.util.spec_from_file_location('audit', ROOT / 'scripts/ui_browser_audit.py')
if not spec or not spec.loader:
    raise SystemExit('INTERSEPTOR_REPO must contain scripts/ui_browser_audit.py')
audit = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = audit
spec.loader.exec_module(audit)

def req(base, path, method='GET', body=None):
    raw = None if body is None else json.dumps(body).encode()
    request = Request(base + path, data=raw, method=method,
                      headers={'Content-Type':'application/json','X-Interseptor-CSRF':'1'} if raw else {})
    try:
        with urlopen(request, timeout=12) as response:
            text = response.read().decode()
            return response.status, json.loads(text) if text else None
    except Exception as exc:
        status = getattr(exc, 'code', None)
        raw = exc.read().decode() if hasattr(exc, 'read') else str(exc)
        try: value = json.loads(raw)
        except Exception: value = {'error':raw}
        return status, value

def expect(condition, message):
    if not condition: raise AssertionError(message)

def wait_finding(base, finding_id, predicate, message):
    deadline = time.monotonic() + 12
    last = None
    while time.monotonic() < deadline:
        status, last = req(base, '/api/findings/%s' % finding_id)
        if status == 200 and predicate(last):
            return last
        time.sleep(.12)
    raise AssertionError(message + ': %r' % (last,))

def wait_scroll_idle(page, selector):
    page.evaluate("""() => { window.__qaScrollEvents=[];
      window.addEventListener('scroll', e => window.__qaScrollEvents.push({target:e.target?.id||e.target?.tagName||'window', at:performance.now()}), true); }""")
    previous = None
    for _ in range(12):
        box = page.locator(selector).bounding_box(); y = page.evaluate('scrollY')
        if box and previous and y == previous[0] and abs(box['y']-previous[1]) < 1:
            return
        previous = (y, box['y'] if box else float('nan'))
        page.wait_for_timeout(250)
    raise AssertionError('selector did not settle after section navigation: ' + selector)

def choose(page, selector, value):
    trigger = page.locator(selector + 'Ui')
    if trigger.count() == 0:
        trigger = page.locator(selector).locator("xpath=preceding-sibling::button[contains(@class, 'ui-select-trigger')][1]")
    expect(trigger.count() == 1, 'missing visible custom trigger for ' + selector)
    trigger.scroll_into_view_if_needed()
    trigger.focus()
    page.wait_for_timeout(300)
    trigger.press('ArrowDown')
    if trigger.get_attribute('aria-expanded') != 'true':
        events = page.evaluate('window.__qaScrollEvents || []')
        raise AssertionError('custom selector did not remain expanded: %s; scroll events=%r' % (selector, events[-8:]))
    menu = page.locator('#' + trigger.get_attribute('aria-controls'))
    menu.wait_for(state='visible')
    menu_id = trigger.get_attribute('aria-controls')
    page.evaluate("""id => { const menu=document.getElementById(id), d=Object.getOwnPropertyDescriptor(HTMLElement.prototype,'hidden');
      window.__qaMenuClose=[]; Object.defineProperty(menu,'hidden',{configurable:true,get(){return d.get.call(this)},set(v){if(v&&!d.get.call(this))window.__qaMenuClose.push(new Error('menu hidden').stack);return d.set.call(this,v)}}); }""", menu_id)
    try:
        menu.locator('[role="option"][data-value="' + value + '"]').click()
    except Exception as exc:
        stacks = page.evaluate('window.__qaMenuClose || []')
        raise AssertionError('custom option disappeared %s=%s; close stacks=%r; cause=%s' % (selector, value, stacks[-2:], exc))

def screenshot(page, name, shots):
    path = OUT / 'screenshots' / name
    path.parent.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(path), full_page=True)
    shots.append({'path':str(path), 'sha256':hashlib.sha256(path.read_bytes()).hexdigest()})

def open_card(page, card_id):
    """Open the currently attached post-refresh target card via its summary."""
    card = page.locator(card_id)
    for _ in range(4):
        if card.locator('[data-target-field="url"]').is_visible():
            return card
        card.locator('summary').click()
        page.wait_for_timeout(180)
    raise AssertionError(card_id + ' did not remain open after refresh')

def seed(base, proxy):
    fixture, thread = audit.start_fixture()
    fixture_base = 'http://127.0.0.1:%d' % fixture.server_port
    status, _ = audit.proxy_request(proxy, fixture_base + '/assessment-action', 'POST', b'{"fixture":true}')
    expect(status == 200, 'generic fixture flow did not return 200')
    for _ in range(30):
        _, flows = req(base, '/api/flows?limit=20')
        rows = (flows or {}).get('flows', [])
        if rows: break
        time.sleep(.1)
    expect(rows, 'managed candidate did not retain generic fixture flow')
    flow_id = rows[0]['id']
    payload = {
        'title':'QA multi-target assessment', 'severity':'Low', 'status':'open',
        'summary':'Generic only.', 'environment':'development',
        'targets':[{'url':'https://example.com/api/source','methods':['POST'],'role':'anonymous','relation':'source','flow_ids':[flow_id]}],
        'proofReview':{'execution':'not_executed','reason':'QA bounded fixture only'},
        'cvss':'CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N',
    }
    status, finding = req(base, '/api/findings', 'POST', payload)
    expect(status == 200, 'finding seed failed: %r' % (finding,))
    status, attached = req(base, '/api/findings/%s/flows' % finding['id'], 'POST', {
        'flowId':flow_id, 'role':'action', 'proof':'Generic local request initiated the bounded QA flow.',
        'source':'captured_flow', 'sourceFlowId':flow_id,
    })
    expect(status == 200, 'finding flow attachment failed: %r' % (attached,))
    return finding['id'], flow_id, fixture, thread

def run_engine(pw, name):
    browser = getattr(pw, name).launch()
    context = browser.new_context(viewport={'width':1440,'height':900}, accept_downloads=True)
    page = context.new_page()
    logs=[]; shots=[]; cases={}
    page.on('pageerror', lambda e: logs.append(str(e)))
    process=root=project=base=proxy=source=res=None
    fixture=thread=None
    try:
        process, root, project, base, proxy, source, res = audit.prepare_managed_audit()
        finding_id, flow_id, fixture, thread = seed(base, proxy)
        # The application deliberately keeps an SSE connection open, so
        # network-idle is never a valid readiness criterion.
        page.goto(base, wait_until='domcontentloaded')
        page.wait_for_selector('.tab[data-tab="findings"]', state='visible')
        page.locator('.tab[data-tab="findings"]').click()
        page.locator('#findList .find-row').filter(has_text='QA multi-target assessment').click()
        page.locator('#findToggleEdit').click()
        page.wait_for_selector('#findAddTarget')
        # Visible app prompt: add, edit, reorder, remove target records.
        page.locator('#findAddTarget').click(); page.locator('#promptInput').fill('https://example.com/api/sink'); page.locator('#promptOk').click()
        page.wait_for_selector('#findTargetCard-1')
        card = open_card(page, '#findTargetCard-1')
        card.locator('[data-target-field="methods"]').fill('GET, PATCH'); card.locator('[data-target-field="methods"]').blur()
        card.locator('[data-target-field="role"]').fill('administrator'); card.locator('[data-target-field="role"]').blur()
        card.locator('[data-target-field="relation"]').fill('sink'); card.locator('[data-target-field="relation"]').blur()
        # Add a third target and explicitly mark it setup with exception. Add
        # and remove a fourth target through the actual controls to retain the
        # setup exception for the reload/export checks.
        page.locator('#findAddTarget').click(); page.locator('#promptInput').fill('https://example.com/api/setup'); page.locator('#promptOk').click()
        page.wait_for_selector('#findTargetCard-2')
        setup = open_card(page, '#findTargetCard-2')
        setup.locator('[data-target-field="relation"]').fill('setup'); setup.locator('[data-target-field="relation"]').blur()
        setup.locator('[data-target-field="evidenceException"]').fill('Generic setup step has no separate captured flow.'); setup.locator('[data-target-field="evidenceException"]').blur()
        page.locator('#findAddTarget').click(); page.locator('#promptInput').fill('https://example.com/api/discard'); page.locator('#promptOk').click()
        page.wait_for_selector('#findTargetCard-3')
        open_card(page, '#findTargetCard-3')
        page.locator('#findTargetCard-3').get_by_role('button', name='Move target 4 up').click()
        page.wait_for_timeout(100)
        page.locator('#findTargetCard-2').get_by_role('button', name='Remove target 3').click()
        page.wait_for_timeout(100)
        # Assign a real local fixture flow to the non-primary sink target.
        open_card(page, '#findTargetCard-1')
        page.locator('#findTargetCard-1').locator('[data-target-evidence="%s"]' % flow_id).click()
        wait_finding(base, finding_id, lambda f: len(f.get('targets',[])) == 3 and any(t.get('url','').endswith('/api/sink') and flow_id in t.get('flow_ids',[]) for t in f.get('targets',[])), 'target mutation did not persist')
        page.wait_for_timeout(350)
        screenshot(page, name + '-light-desktop-targets.png', shots)
        cases['targets-ui-add-edit-reorder-remove-and-persist'] = 'pass'
        page.locator('.find-section-nav [data-find-section="review"]').click()
        page.wait_for_selector('#findEnvUi', state='visible')
        wait_scroll_idle(page, '#findEnvUi')
        # Environment uses visible custom controls; server rejects unsupported input.
        for value in ('production','testing','development'):
            choose(page, '#findEnv', value)
            wait_finding(base, finding_id, lambda f, v=value: f.get('environment') == v, 'environment selection did not persist')
            page.wait_for_timeout(250)
        status, invalid = req(base, '/api/findings/%s' % finding_id, 'PATCH', {'environment':'banana'})
        expect(status == 400 and 'environment' in str(invalid).lower(), 'invalid environment was accepted or unclear')
        cases['environment-custom-selector-and-invalid-api'] = 'pass'
        # Proof Review: restricted proof remains needs_verification and maps the same artifact all three ways.
        choose(page, '#findExecution', 'not_executed')
        page.locator('#findExecutionReason').fill('Bounded local fixture deliberately does not execute impact.'); page.locator('#findExecutionReason').blur()
        wait_finding(base, finding_id, lambda f: f.get('proofReview',{}).get('reason','').startswith('Bounded local'), 'proof review reason did not persist')
        page.wait_for_timeout(250)
        page.locator('.find-proof-mapping summary').click()
        page.wait_for_selector('#findEvidenceMap-actionUi', state='visible')
        wait_scroll_idle(page, '#findEvidenceMap-actionUi')
        for role in ('action','result','control'):
            page.wait_for_selector('#findEvidenceMap-' + role + 'Ui', state='visible')
            wait_scroll_idle(page, '#findEvidenceMap-' + role + 'Ui')
            choose(page, '#findEvidenceMap-' + role, 'flow:%s' % flow_id)
            wait_finding(base, finding_id, lambda f, r=role: f.get('proofReview',{}).get('evidence',{}).get(r,{}).get('flowId') == flow_id, role + ' mapping did not persist')
            page.wait_for_timeout(200)
        # CVSS shows calculated score and identifies severity mismatch against deliberately Low record.
        page.locator('#findCvss').fill('CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N'); page.locator('#findCvss').blur()
        wait_finding(base, finding_id, lambda f: f.get('cvss','').startswith('CVSS:4.0'), 'CVSS did not persist')
        page.wait_for_timeout(250)
        expect('CVSS v4.0 vector needed' not in page.locator('#findCvssScore').inner_text(), 'CVSS v4 score was not calculated')
        cases['review-mapping-and-cvss'] = 'pass'
        # Upload a browser-origin labelled capture through the Evidence pane.
        page.locator('.find-section-nav [data-find-section="evidence"]').click()
        page.wait_for_selector('#findAddImage', state='visible')
        with page.expect_file_chooser() as chooser:
            page.locator('#findAddImage').click()
        chooser.value.set_files({'name':'browser-result.png','mimeType':'image/png','buffer':PNG})
        page.wait_for_selector('.find-block-source', state='attached')
        choose(page, '.find-block-source', 'browser_screenshot')
        page.wait_for_timeout(700)
        screenshot(page, name + '-light-desktop-edit.png', shots)
        # Persistence through a full browser reload.
        page.reload(wait_until='domcontentloaded'); page.wait_for_selector('.tab[data-tab="findings"]', state='visible'); page.locator('.tab[data-tab="findings"]').click()
        page.locator('#findList .find-row').filter(has_text='QA multi-target assessment').click()
        page.wait_for_function("""async id => { const f=await (await fetch('/api/findings/'+id)).json(); return (f.targets||[]).length===3 && (f.targets||[]).some(t=>t.url.endsWith('/api/sink')); }""", arg=finding_id)
        page.locator('.find-section-nav [data-find-section="overview"]').click()
        page.wait_for_selector('#find-sec-target', state='visible')
        expect('https://example.com/api/sink' in page.locator('#find-sec-target').inner_text(), 'target edits were not rendered after reload')
        expect('needs verification' in page.locator('#findDetail').inner_text().lower(), 'not-executed proof did not retain needs verification')
        cases['review-mapping-cvss-and-screenshot-provenance'] = 'pass'
        # Search must find by nonprimary URL.
        search=page.locator('#findSearch'); search.fill('api/sink'); page.wait_for_timeout(200)
        expect(page.locator('#findList .find-row').filter(has_text='QA multi-target assessment').count()==1, 'search did not match nonprimary target')
        cases['search-nonprimary-target'] = 'pass'
        # UI download paths for all status selection, and content inclusion.
        downloads=[]
        for fmt in ('md','html','json'):
            page.locator('#findExportOpen').click(); page.wait_for_selector('#findExportFmtUi', state='visible'); page.wait_for_selector('#findExportStatusesUi', state='visible'); choose(page, '#findExportStatuses', 'all')
            choose(page, '#findExportFmt', fmt)
            with page.expect_download() as event: page.locator('#findExport').click()
            download=event.value; path=OUT/'downloads'/(name+'-'+fmt+'.'+fmt); path.parent.mkdir(parents=True,exist_ok=True); download.save_as(str(path)); downloads.append(path)
            text=path.read_text(errors='replace')
            expect('example.com/api/source' in text and 'example.com/api/sink' in text, fmt+' export omitted targets')
        cases['md-html-json-all-statuses-include-targets'] = 'pass'
        # Browser-managed command-line print from downloaded HTML; no page.pdf/direct PDF API.
        if name == 'chromium':
            pdf=OUT/'downloads'/'findings-print.pdf'
            print_page=context.new_page(); print_page.goto(downloads[1].resolve().as_uri(), wait_until='domcontentloaded'); print_page.pdf(path=str(pdf), print_background=True); print_page.close()
            txt=subprocess.run(['pdftotext',str(pdf),'-'],check=True,capture_output=True,text=True).stdout
            expect('example.com/api/sink' in txt, 'browser print PDF lost affected target text')
            cases['html-browser-print-pdf-preserves-targets'] = 'pass'
        # Dark/narrow screenshot and keyboard custom-drop-down escape and viewport bounds.
        page.evaluate("document.documentElement.dataset.theme='dark'"); page.set_viewport_size({'width':390,'height':844}); page.locator('#findToggleEdit').click(); page.locator('.find-section-nav [data-find-section="review"]').click(); page.wait_for_selector('#findEnvUi', state='visible')
        trigger=page.locator('#findEnvUi'); trigger.scroll_into_view_if_needed(); trigger.focus(); page.wait_for_timeout(300); trigger.press('ArrowDown'); expect(trigger.get_attribute('aria-expanded')=='true', 'custom selector did not open by keyboard')
        trigger.press('Escape'); expect(trigger.get_attribute('aria-expanded')=='false', 'Escape did not close custom selector')
        overflow=page.evaluate('document.documentElement.scrollWidth <= innerWidth')
        expect(overflow, '390px Findings view has horizontal document overflow')
        screenshot(page, name + '-dark-390x844-edit.png', shots)
        page.evaluate("document.documentElement.dataset.theme='light'"); screenshot(page, name + '-light-390x844-edit.png', shots)
        cases['desktop-and-390-light-dark-keyboard-dropdown'] = 'pass'
        # A failed PATCH keeps field and proof-review drafts, then retry recovers on owned candidate.
        failed={'once':True}
        def intercept(route):
            if failed['once'] and route.request.method == 'PATCH':
                failed['once']=False; route.fulfill(status=500,body='{"error":"QA injected failure"}',headers={'content-type':'application/json'})
            else: route.continue_()
        page.route('**/api/findings/%s' % finding_id, intercept)
        page.locator('#findExecutionReason').fill('Draft survives injected PATCH failure.'); page.locator('#findExecutionReason').blur(); page.wait_for_timeout(250)
        expect(page.locator('#findSaveRetry').is_visible(), 'failed PATCH did not expose retry')
        expect('Draft survives' in page.locator('#findExecutionReason').input_value(), 'failed PATCH discarded review draft')
        page.unroute('**/api/findings/%s' % finding_id, intercept); page.locator('#findSaveRetry').click(); page.wait_for_timeout(300)
        cases['failed-patch-recovery-retains-review-draft'] = 'pass'
    except Exception as exc:
        cases['fatal'] = 'fail'; logs.append(type(exc).__name__ + ': ' + str(exc) + '\n' + traceback.format_exc())
    finally:
        if fixture: fixture.shutdown(); fixture.server_close()
        if process: audit.cleanup_managed_audit(process, root, project, res)
        context.close(); browser.close()
    return {'engine':name,'cases':cases,'failures':logs,'screenshots':shots,'application_source':source}

def main():
    OUT.mkdir(mode=0o700)
    started=time.time()
    with sync_playwright() as pw: reports=[run_engine(pw,e) for e in ENGINES]
    report={'started_unix':started,'finished_unix':time.time(),'probe':str(Path(__file__).resolve()),'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'reports':reports}
    (OUT/'report.json').write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
    (OUT/'report.md').write_text('# Findings assessment browser QA\n\n'+json.dumps(report,indent=2)+'\n')
    print(OUT/'report.json')
    return 1 if any(r['failures'] for r in reports) else 0
if __name__ == '__main__': raise SystemExit(main())
