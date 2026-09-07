#!/usr/bin/env python3
"""Portable disposable browser acceptance probe for History menu and tag colors."""
import hashlib
import json
import os
import sys
import tempfile
import time
from pathlib import Path

from playwright.sync_api import sync_playwright

REPO = Path(os.environ.get('INTERSEPTOR_REPO', Path.cwd())).resolve()
OUT = Path(os.environ.get('INTERSEPTOR_UI_BUG_FINAL_OUT', tempfile.mkdtemp(prefix='interseptor-ui-bug-final-')))
OUT.mkdir(parents=True, exist_ok=True)
sys.path.insert(0, str(REPO / 'scripts'))
import ui_browser_audit as audit

HOST = 'very-long-service-name.example.com'
TAG = 'palette-check'
PALETTE = ('red', 'amber', 'blue', 'violet', 'cyan', 'gray', 'green')

def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def har_fixture():
    entries = []
    for n in range(28):
        entries.append({
            'startedDateTime': f'2026-09-07T00:00:{n:02d}.000Z', 'time': 2,
            'request': {'method': 'GET', 'url': f'https://{HOST}/generic/local/fixture/{n}', 'httpVersion': 'HTTP/1.1', 'headers': [], 'queryString': [], 'cookies': [], 'headersSize': -1, 'bodySize': 0},
            'response': {'status': 200, 'statusText': 'OK', 'httpVersion': 'HTTP/1.1', 'headers': [{'name': 'content-type', 'value': 'text/plain'}], 'cookies': [], 'content': {'size': 2, 'mimeType': 'text/plain', 'text': 'ok'}, 'redirectURL': '', 'headersSize': -1, 'bodySize': 2},
            'cache': {}, 'timings': {'send': 0, 'wait': 1, 'receive': 1},
        })
    return {'log': {'version': '1.2', 'creator': {'name': 'local-ui-probe', 'version': '1'}, 'entries': entries}}

def fetch_json(page, path, method='GET', body=None):
    return page.evaluate("""async ({path,method,body}) => {
      const r=await fetch(path,{method,headers:body?{'content-type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
      return {status:r.status,body:await r.text()};
    }""", {'path': path, 'method': method, 'body': body})

def current_tag(page, tag):
    tags = json.loads(fetch_json(page, '/api/tags')['body']).get('tags', [])
    return next(t for t in tags if t['tag'] == tag)

def right_click_host(page, near_bottom=False):
    rows = page.locator('#rows')
    if near_bottom:
        rows.evaluate('(el) => { el.scrollTop = el.scrollHeight; }')
    row = page.locator('#rows .trow').filter(has_text=HOST).last
    row.scroll_into_view_if_needed()
    if near_bottom:
        rows.evaluate('(el) => { el.scrollTop = el.scrollHeight; }')
    row.locator('[data-field="host"]').click(button='right')
    page.wait_for_selector('#ctxmenu.show')

def menu_metrics(page):
    return page.evaluate("""() => {
      const ctx=document.querySelector('#ctxmenu'), vr={width:innerWidth,height:innerHeight};
      const r=e=>{const x=e.getBoundingClientRect();return {left:x.left,right:x.right,top:x.top,bottom:x.bottom,width:x.width,height:x.height,text:e.textContent.trim()}};
      const painted=e=>{const q=document.createRange();q.selectNodeContents(e);return [...q.getClientRects()].map(x=>({left:x.left,right:x.right,top:x.top,bottom:x.bottom,width:x.width,height:x.height,text:e.textContent.trim()}));};
      const pairs=[...ctx.querySelectorAll('.ctx-item')].map(item=>{const label=item.querySelector('.ctx-label-text')||item.querySelector('.lbl'),value=item.querySelector('.ctx-value,.mono');return {item:r(item),label:r(label),labelPaint:painted(label),value:value?r(value):null};});
      const overlap=pairs.filter(p=>p.value&&p.labelPaint.some(x=>x.right>p.value.left+.1));
      const overflow=pairs.filter(p=>p.labelPaint.some(x=>x.left<p.item.left-.1||x.right>p.item.right+.1)||(p.value&&(p.value.left<p.item.left-.1||p.value.right>p.item.right+.1)));
      return {menu:r(ctx), viewport:vr, scroll:{clientHeight:ctx.clientHeight,scrollHeight:ctx.scrollHeight}, overlap, overflow, pairCount:pairs.length};
    }""")

def verify_menu(page, result, key, screenshot=True, near_bottom=False, reduced=False):
    right_click_host(page, near_bottom=near_bottom)
    motion = page.evaluate("""()=>{const e=document.querySelector('#ctxmenu'),s=getComputedStyle(e);return {side:e.dataset.motionSide||'',animationName:s.animationName,animationDuration:s.animationDuration}}""")
    result.setdefault('motion', {})[key] = motion
    if reduced:
        if motion['animationName'] != 'none':
            raise AssertionError(f'{key}: reduced-motion context menu still animates: {motion}')
    elif motion['animationName'] == 'none' or '0.12' not in motion['animationDuration']:
        raise AssertionError(f'{key}: context menu entrance is not the expected 120ms animation: {motion}')
    page.wait_for_timeout(160)
    metrics = menu_metrics(page)
    result['menus'][key] = metrics
    m, v = metrics['menu'], metrics['viewport']
    if m['left'] < 8 or m['right'] > v['width'] - 8 or m['top'] < 8 or m['bottom'] > v['height'] - 8:
        raise AssertionError(f'{key}: context menu escaped viewport: {m} in {v}')
    if metrics['overlap']:
        raise AssertionError(f'{key}: label/value overlap: {metrics["overlap"]}')
    if metrics['overflow']:
        raise AssertionError(f'{key}: label/value escaped item: {metrics["overflow"]}')
    if screenshot:
        image = OUT / f'{key}.png'
        page.screenshot(path=str(image), full_page=False)
        result['screenshots'][image.name] = digest(image)
    page.keyboard.press('Escape')

def verify_custom_dropdown(page, result):
    trigger = page.locator('#fMethodUi')
    trigger.click()
    menu_id = trigger.get_attribute('aria-controls')
    menu = page.locator('#' + menu_id)
    menu.wait_for(state='visible')
    motion = page.evaluate("""e=>{const s=getComputedStyle(e);return {side:e.dataset.motionSide||'',animationName:s.animationName,animationDuration:s.animationDuration}}""", menu.element_handle())
    result['dropdown_motion'] = motion
    if motion['animationName'] == 'none' or '0.12' not in motion['animationDuration']:
        raise AssertionError(f'custom dropdown did not enter with 120ms motion: {motion}')
    menu.locator('.ui-select-opt[data-value="GET"]').click()
    if page.locator('#fMethod').input_value() != 'GET':
        raise AssertionError('custom dropdown selection was not usable during entrance')

def verify_rapid_navigation(page, result, key):
    page.set_viewport_size({'width': 1440, 'height': 900})
    tabs = page.locator('#tabs .tab[data-tab]').evaluate_all('(els)=>els.map(e=>e.dataset.tab)')
    if len(tabs) != 10:
        raise AssertionError(f'expected 10 main tabs, got {tabs}')
    visited = []
    for tab in tabs:
        page.locator(f'#tabs .tab[data-tab="{tab}"]').click()
        page.wait_for_function("(tab)=>{const p=document.querySelector('[data-panel=\"'+tab+'\"]')||document.querySelector('#panel-'+tab);return !!p?.classList.contains('active')}", arg=tab)
        visited.append(tab)
    result.setdefault('rapid_navigation', {})[key] = visited

def verify_disclosures(page, result):
    page.locator('.tab[data-tab="settings"]').click()
    page.locator('#setNav button[data-sec="tls"]').click()
    sec = page.locator('#settings-section-tls')
    sec.wait_for(state='visible')
    result['settings_motion'] = page.evaluate("""e=>({name:getComputedStyle(e).animationName,duration:getComputedStyle(e).animationDuration})""", sec.element_handle())
    if result['settings_motion']['name'] == 'none' or '0.18' not in result['settings_motion']['duration']:
        raise AssertionError(f'Settings section reveal missing expected 180ms motion: {result["settings_motion"]}')
    page.route('**/api/activity', lambda route: route.fulfill(content_type='application/json', body=json.dumps({'activity':[{'id':'generic-local-activity','tool':'fixture','ok':True,'summary':'generic local activity','result':'opened generic details','ts':1}]})))
    page.locator('.tab[data-tab="activity"]').click()
    row = page.locator('#actFeed .act-expandable').first
    row.wait_for()
    row.click()
    detail = row.locator('.act-detail')
    detail.wait_for(state='visible')
    result['activity_motion'] = page.evaluate("""e=>({name:getComputedStyle(e).animationName,duration:getComputedStyle(e).animationDuration})""", detail.element_handle())
    if result['activity_motion']['name'] == 'none' or '0.18' not in result['activity_motion']['duration']:
        raise AssertionError(f'Activity detail reveal missing expected 180ms motion: {result["activity_motion"]}')
    result['idle_switch_animation'] = page.evaluate("""()=>getComputedStyle(document.querySelector('.uisw.on .uisw-led')||document.querySelector('.uisw .uisw-led')).animationName""")
    if result['idle_switch_animation'] != 'none':
        raise AssertionError(f'idle switch still animates: {result["idle_switch_animation"]}')

def verify_keyboard(page, result, tag):
    chip = page.locator('#tagBar .tagchip').filter(has_text=tag)
    result['keyboard']['escape_repetitions'] = []
    for _ in range(3):
        chip.focus()
        page.keyboard.press('ContextMenu')
        page.wait_for_selector('#ctxmenu.show')
        opened = page.evaluate('document.activeElement?.closest?.("#ctxmenu .ctx-item")?.textContent?.trim() || ""')
        if not opened:
            raise AssertionError('keyboard context menu did not focus a menu item')
        page.keyboard.press('Escape')
        restored = page.evaluate('(tag)=>document.activeElement?.matches?.("#tagBar .tagchip") && document.activeElement.textContent.includes(tag)', tag)
        result['keyboard']['escape_repetitions'].append({'open_focus': opened, 'restored_chip': restored})
        if not restored:
            raise AssertionError('Escape did not restore tag-chip focus')
    result['keyboard']['open_focus'] = result['keyboard']['escape_repetitions'][0]['open_focus']
    result['keyboard']['escape_focus_chip'] = True
    chip.focus()
    page.keyboard.press('ContextMenu')
    page.wait_for_selector('#ctxmenu.show')
    with page.expect_response(lambda r: '/api/tags/' in r.url and r.request.method == 'PUT', timeout=10000) as response_info:
        page.keyboard.press('Enter')
    result['keyboard']['enter_status'] = response_info.value.status
    page.wait_for_timeout(150)
    result['keyboard']['enter_focus_chip'] = page.evaluate('(tag)=>document.activeElement?.matches?.("#tagBar .tagchip") && document.activeElement.textContent.includes(tag)', tag)
    if result['keyboard']['enter_status'] != 204 or not result['keyboard']['enter_focus_chip']:
        raise AssertionError(f'palette Enter did not save and retain focus: {result["keyboard"]}')

def verify_palette(page, result, tag):
    chip = page.locator('#tagBar .tagchip').filter(has_text=tag)
    for color_name in PALETTE:
        chip.click(button='right')
        page.wait_for_selector('#ctxmenu.show')
        item = page.locator('#ctxmenu .ctx-item').filter(has=page.locator('.lbl', has_text=color_name)).first
        expected = item.locator('.mono').text_content().strip()
        if not expected.startswith('#'):
            raise AssertionError(f'{color_name}: menu did not expose a hex value: {expected!r}')
        with page.expect_response(lambda r: '/api/tags/' in r.url and r.request.method == 'PUT', timeout=10000) as response_info:
            item.click()
        response = response_info.value
        request_body = response.request.post_data or ''
        record = {'name': color_name, 'expected': expected, 'status': response.status, 'request_body': request_body}
        result['palette'].append(record)
        if response.status != 204 or json.loads(request_body).get('color') != expected:
            raise AssertionError(f'{color_name}: expected hex PUT 204, got {record}')
        page.wait_for_timeout(120)
        page.reload(wait_until='domcontentloaded')
        chip = page.locator('#tagBar .tagchip').filter(has_text=tag)
        chip.wait_for()
        persisted = current_tag(page, tag).get('color', '')
        style = chip.get_attribute('style') or ''
        record['persisted'] = persisted
        record['chip_style'] = style
        if persisted != expected or not style:
            raise AssertionError(f'{color_name}: chip/color did not persist after reload: {record}')
    return chip

def verify_clear(page, result, tag):
    chip = page.locator('#tagBar .tagchip').filter(has_text=tag)
    chip.click(button='right')
    page.wait_for_selector('#ctxmenu.show')
    clear = page.locator('#ctxmenu .ctx-item').filter(has_text='Clear color')
    with page.expect_response(lambda r: '/api/tags/' in r.url and r.request.method == 'PUT', timeout=10000) as response_info:
        clear.click()
    response = response_info.value
    request_body = response.request.post_data or ''
    record = {'name': 'clear', 'status': response.status, 'request_body': request_body}
    result['palette'].append(record)
    if response.status != 204 or json.loads(request_body).get('color') != '':
        raise AssertionError(f'clear: expected empty-color PUT 204, got {record}')
    page.wait_for_timeout(120)
    page.reload(wait_until='domcontentloaded')
    chip = page.locator('#tagBar .tagchip').filter(has_text=tag)
    chip.wait_for()
    record['persisted'] = current_tag(page, tag).get('color', '')
    record['chip_style'] = chip.get_attribute('style') or ''
    if record['persisted'] or record['chip_style']:
        raise AssertionError(f'clear: chip/color did not persist after reload: {record}')

def main():
    result = {'kind': 'after', 'started_unix': time.time(), 'output_dir': str(OUT), 'application_source': None, 'requests': [], 'palette': [], 'menus': {}, 'keyboard': {}, 'screenshots': {}, 'failures': []}
    process = root = project = reservations = None
    try:
        process, root, project, base, proxy, source, reservations = audit.prepare_managed_audit()
        result.update({'application_source': source, 'base_url': base, 'project': project, 'data_root': str(root), 'probe_sha256': digest(__file__)})
        with sync_playwright() as pw:
            all_engines = {'chromium': pw.chromium, 'firefox': pw.firefox, 'webkit': pw.webkit}
            requested = [x.strip() for x in os.environ.get('INTERSEPTOR_UI_BUG_ENGINES', ','.join(all_engines)).split(',') if x.strip()]
            engines = {name: all_engines[name] for name in requested}
            seeded = False
            for engine_name, engine in engines.items():
                browser = engine.launch()
                try:
                    configs = [('light', 390, 844, 'phone', False), ('dark', 1440, 900, 'desktop', False), ('light', 375, 812, 'reduced-phone', True)]
                    for theme, width, height, form, reduced in configs:
                        context = browser.new_context(viewport={'width': width, 'height': height}, color_scheme=theme, reduced_motion='reduce' if reduced else 'no-preference')
                        page = context.new_page()
                        console, errors = [], []
                        page.on('console', lambda m, sink=console: sink.append(m.text) if m.type == 'error' else None)
                        page.on('pageerror', lambda e, sink=errors: sink.append(str(e)))
                        page.goto(base, wait_until='domcontentloaded')
                        page.wait_for_selector('#rows')
                        if not seeded and theme == 'light' and not reduced:
                            result['import'] = fetch_json(page, '/api/import/har', 'POST', har_fixture())
                            flows = json.loads(fetch_json(page, '/api/flows?limit=100')['body'])['flows']
                            flow = next(f for f in flows if f['host'] == HOST)
                            result['tag_seed'] = fetch_json(page, '/api/flows/tags', 'POST', {'flowIds': [flow['id']], 'add': [TAG]})
                            page.reload(wait_until='domcontentloaded')
                            page.wait_for_selector('#rows .trow')
                            seeded = True
                        key = f'after-{engine_name}-{theme}-{form}'
                        verify_menu(page, result, key, near_bottom=True, reduced=reduced)
                        if engine_name == 'chromium' and theme == 'light' and not reduced:
                            for rw in (768, 1440):
                                page.set_viewport_size({'width': rw, 'height': 900})
                                verify_menu(page, result, f'after-chromium-light-{rw}px-repeat', screenshot=True, near_bottom=True)
                            page.set_viewport_size({'width': 812, 'height': 375})
                            verify_menu(page, result, 'after-chromium-light-landscape', screenshot=True, near_bottom=True)
                            page.set_viewport_size({'width': 390, 'height': 844})
                            verify_custom_dropdown(page, result)
                            page.reload(wait_until='domcontentloaded')
                            page.wait_for_selector('#tagBar .tagchip')
                            verify_keyboard(page, result, TAG)
                            verify_palette(page, result, TAG)
                            chip = page.locator('#tagBar .tagchip').filter(has_text=TAG)
                            result.setdefault('light_readability', {})[engine_name] = page.evaluate("""el=>({chipColor:getComputedStyle(el).color,chipBackground:getComputedStyle(el).backgroundColor,pageBackground:getComputedStyle(document.body).backgroundColor,style:el.getAttribute('style')||''})""", chip.element_handle())
                        elif theme == 'light' and not reduced:
                            page.wait_for_selector('#tagBar .tagchip')
                            verify_keyboard(page, result, TAG)
                            verify_palette(page, result, TAG)
                            chip = page.locator('#tagBar .tagchip').filter(has_text=TAG)
                            result.setdefault('light_readability', {})[engine_name] = page.evaluate("""el=>({chipColor:getComputedStyle(el).color,chipBackground:getComputedStyle(el).backgroundColor,pageBackground:getComputedStyle(document.body).backgroundColor,style:el.getAttribute('style')||''})""", chip.element_handle())
                        if theme == 'dark' and not reduced:
                            chip = page.locator('#tagBar .tagchip').filter(has_text=TAG)
                            result.setdefault('dark_readability', {})[engine_name] = page.evaluate("""el=>({chipColor:getComputedStyle(el).color,chipBackground:getComputedStyle(el).backgroundColor,pageBackground:getComputedStyle(document.body).backgroundColor,style:el.getAttribute('style')||''})""", chip.element_handle())
                            if not result['dark_readability'][engine_name]['style']:
                                raise AssertionError('dark theme lost the persisted tag-chip color')
                            verify_clear(page, result, TAG)
                            if engine_name == 'chromium':
                                verify_disclosures(page, result)
                        if engine_name == 'chromium':
                            verify_rapid_navigation(page, result, key)
                        result['console_' + key] = console
                        result['page_errors_' + key] = errors
                        if errors:
                            raise AssertionError(f'{key}: page errors {errors}')
                        context.close()
                finally:
                    browser.close()
        result['finished_unix'] = time.time()
    except Exception as exc:
        result['failures'].append(f'{type(exc).__name__}: {exc}')
        result['finished_unix'] = time.time()
    finally:
        if process is not None:
            try:
                audit.cleanup_managed_audit(process, root, project, reservations)
                result['cleanup'] = 'ok'
            except Exception as exc:
                result['cleanup'] = f'{type(exc).__name__}: {exc}'
    (OUT / 'after-report.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps({'output_dir': str(OUT), 'failures': result['failures'], 'runtime': result['application_source'], 'screenshots': result['screenshots'], 'palette': result['palette'], 'cleanup': result.get('cleanup')}, indent=2))
    return 0 if not result['failures'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
