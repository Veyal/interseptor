#!/usr/bin/env python3
"""Read-only 18-dialog shell geometry/focus matrix for a held UI candidate.

Requires BASE_URL and RUNTIME_DIGEST.  OUT_PATH controls the JSON report;
representative animation-disabled Guide PNGs are derived from its filename.
"""
import hashlib, importlib.util, json, os
from pathlib import Path
from playwright.sync_api import sync_playwright

BASE = os.environ['BASE_URL'].rstrip('/')
DIGEST = os.environ['RUNTIME_DIGEST']
OUT = Path(os.environ.get('OUT_PATH', '/tmp/interseptor-popup-modal.json'))
PROBE_PATH = Path(__file__).with_name('interseptor_popup_audit.py')
spec = importlib.util.spec_from_file_location('popup_audit', PROBE_PATH)
audit = importlib.util.module_from_spec(spec); spec.loader.exec_module(audit)

def main():
    result = audit.Audit()
    screenshots = []
    with sync_playwright() as p:
        for engine in ('chromium', 'firefox', 'webkit'):
            browser = getattr(p, engine).launch()
            for width, height in audit.VIEWPORTS:
                page = browser.new_page(viewport={'width': width, 'height': height})
                page.set_default_timeout(7000)
                try:
                    audit.wait_ready(page)
                    for ident in audit.MODALS:
                        audit.modal_check(result, page, engine, (width, height), ident)
                    if engine == 'chromium':
                        audit.modal_open_shell(page, 'findGuideModal')
                        image = OUT.with_name(f'{OUT.stem}-guide-{width}x{height}.png')
                        page.screenshot(path=str(image), animations='disabled')
                        screenshots.append(str(image))
                        audit.close_escape(page)
                except Exception as exc:
                    result.add(f'engine/{engine}/{width}x{height}/bootstrap', 'fail',
                               error=f'{type(exc).__name__}: {exc}')
                finally:
                    page.close()
            browser.close()
    payload = {'base_url': BASE, 'runtime_digest': DIGEST,
               'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
               'shared_probe_sha256': hashlib.sha256(PROBE_PATH.read_bytes()).hexdigest(),
               'entry': 'direct-shell', 'screenshots': screenshots,
               'cases': result.cases, 'defects': result.defects}
    OUT.write_text(json.dumps(payload, indent=2) + '\n')
    print(json.dumps({'out': str(OUT), 'cases': len(result.cases),
                      'failed': len(result.defects), 'script_sha256': payload['script_sha256'],
                      'shared_probe_sha256': payload['shared_probe_sha256']}, indent=2))
    raise SystemExit(1 if result.defects else 0)

if __name__ == '__main__':
    main()
