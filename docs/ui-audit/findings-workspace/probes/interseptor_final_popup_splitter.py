#!/usr/bin/env python3
"""Read-only Inspector splitter geometry probe for a held Interseptor UI candidate.

Requires BASE_URL, RUNTIME_DIGEST, and optionally FLOW_ID (default 1) and
OUT_PATH. It opens the captured history row through the visible UI only.
"""
import hashlib, importlib.util, json, os
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE = os.environ['BASE_URL'].rstrip('/')
DIGEST = os.environ['RUNTIME_DIGEST']
FLOW_ID = os.environ.get('FLOW_ID', '1')
OUT = Path(os.environ.get('OUT_PATH', '/tmp/interseptor-popup-splitter.json'))
PROBE_PATH = Path(__file__).with_name('interseptor_popup_audit.py')
spec = importlib.util.spec_from_file_location('popup_audit', PROBE_PATH)
audit = importlib.util.module_from_spec(spec); spec.loader.exec_module(audit)

def run_case(page, engine):
    page.locator('.tab[data-tab="proxy"]').click()
    row = page.locator(f'#rows .trow[data-id="{FLOW_ID}"]')
    row.wait_for(state='visible')
    row.click()
    page.wait_for_timeout(400)
    splitter = page.locator('#inspectSplitter')
    inspect = page.locator('#inspect')
    now = int(splitter.get_attribute('aria-valuenow'))
    minimum = int(splitter.get_attribute('aria-valuemin'))
    maximum = int(splitter.get_attribute('aria-valuemax'))
    height = inspect.bounding_box()['height']
    assert minimum <= now <= maximum, {'now': now, 'min': minimum, 'max': maximum}
    assert abs(round(height) - now) <= 1, {'ariaValueNow': now, 'inspectHeight': height}
    return {'name': f'inspector/{engine}/splitter-height', 'status': 'pass',
            'ariaValueNow': now, 'ariaValueMin': minimum, 'ariaValueMax': maximum,
            'inspectHeight': height, 'flowId': FLOW_ID}

def main():
    cases = []
    with sync_playwright() as p:
        for engine in ('chromium', 'firefox', 'webkit'):
            browser = getattr(p, engine).launch()
            page = browser.new_page(viewport={'width': 1024, 'height': 768})
            page.set_default_timeout(7000)
            try:
                audit.wait_ready(page)
                cases.append(run_case(page, engine))
            except Exception as exc:
                cases.append({'name': f'inspector/{engine}/splitter-height', 'status': 'fail',
                              'error': f'{type(exc).__name__}: {exc}', 'flowId': FLOW_ID})
            finally:
                page.close(); browser.close()
    result = {'runtime_digest': DIGEST,
              'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'shared_probe_sha256': hashlib.sha256(PROBE_PATH.read_bytes()).hexdigest(),
              'cases': cases}
    OUT.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps({'out': str(OUT), 'cases': len(cases),
                      'failed': sum(c['status'] == 'fail' for c in cases),
                      'script_sha256': result['script_sha256']}, indent=2))

if __name__ == '__main__':
    main()
