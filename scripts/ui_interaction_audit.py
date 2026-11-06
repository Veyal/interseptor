#!/usr/bin/env python3
"""Repeatable, self-seeding browser interaction audit for Interseptor's control UI.

Drives the *system* Chrome (Playwright browsers are not assumed) against a throwaway
instance and seeds its own traffic through that instance's proxy — a loopback target
server is started when --target is omitted. Usage and coverage: ui_interaction_readme.md.
Exit codes: 0 = all checks passed, 1 = at least one failed, 2 = refused to run.
"""

from __future__ import annotations

import argparse
import http.client
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - operator guidance
    raise SystemExit("Playwright is required: python3 -m pip install playwright")

DEFAULT_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
# The product defaults belong to a real user instance; the audit seeds flows and
# rewrites persisted state, so those ports are always refused.
FORBIDDEN_PORTS = {9966, 8080}
LOOPBACK = {"127.0.0.1", "localhost", "::1", "[::1]"}
# Chrome blocks the Render view's sandboxed srcdoc frame from running its script.
EXPECTED_CONSOLE = ("Blocked script execution in 'about:srcdoc'",
                    'Blocked script execution in "about:srcdoc"')
TABS = ("proxy", "intercept", "repeater", "intruder", "identities", "collections",
        "scanner", "map", "findings", "notes", "activity", "settings")
VIEWPORTS = ((1440, 900), (1024, 768), (390, 844))
HTML_PAGE = (
    "<!doctype html><html><head><meta charset='utf-8'><title>Audit Target</title></head>"
    "<body><h1>Hello world</h1><p>seeded by ui_interaction_audit.py</p>"
    "<script>document.title='script-ran';</script></body></html>"
).encode()

OVERFLOW_JS = """() => { const d = document.documentElement;
    const extra = Math.max(d.scrollWidth, document.body ? document.body.scrollWidth : 0) - d.clientWidth;
    return extra > 1 ? extra : 0; }"""
PANEL_BOX_JS = """(tab) => { const p = document.getElementById('panel-' + tab); if (!p) return null;
    const r = p.getBoundingClientRect();
    return {w: Math.round(r.width), h: Math.round(r.height), text: (p.innerText || '').trim().length}; }"""
RAIL_JS = """() => ({tabs: [...document.querySelectorAll('.tab[data-tab]')].map(t => t.dataset.tab),
    disabled: [...document.querySelectorAll('.tab[data-tab][disabled]')].map(t => t.dataset.tab)})"""
DUP_IDS_JS = """() => { const seen = new Set(), dup = [];
    for (const el of document.querySelectorAll('[id]')) { if (seen.has(el.id)) dup.push(el.id); else seen.add(el.id); }
    return dup.slice(0, 8); }"""
RENDER_FRAME_JS = """() => { const f = document.querySelector('#resView iframe');
    return f ? {srcdoc: f.getAttribute('srcdoc') || '', sandbox: f.getAttribute('sandbox') || ''} : null; }"""
CTXMENU_JS = """() => [...document.querySelectorAll('#ctxmenu.show button, #ctxmenu.show [role="menuitem"]')]
    .map(e => (e.innerText || '').trim()).filter(Boolean).slice(0, 20)"""
PALETTE_JS = """() => { const m = document.querySelector('#cmdk');
    return !!m && !m.hidden && getComputedStyle(m).display !== 'none'; }"""

UNDO_LABEL_JS = """() => { const b = [...document.querySelectorAll('#toast .toast-item button')]
    .find(el => (el.innerText || '').trim() === 'Undo'); return b ? b.innerText.trim() : ''; }"""
UNDO_CLICK_JS = """() => { const b = [...document.querySelectorAll('#toast .toast-item button')]
    .find(el => (el.innerText || '').trim() === 'Undo'); if (b) b.click(); }"""
DRAWER_JS = """() => { const d = document.querySelector('#flowDrawer');
    return d ? {open: d.classList.contains('is-open'),
                text: (d.innerText || '').replace(/\\s+/g, ' ').slice(0, 160)} : null; }"""

results: list = []
console_problems: list = []
network_errors: list = []
target_hits: list = []          # (method, path?query, headers, body) as the target saw it
target_lock = threading.Lock()


def check(name, ok, detail=""):
    results.append({"check": name, "ok": bool(ok), "detail": str(detail)[:400]})
    print(("PASS  " if ok else "FAIL  ") + name + ("" if ok else "  — " + str(detail)[:400]))
    return bool(ok)


def expected_console(entry):
    return any(sig in entry["text"] for sig in EXPECTED_CONSOLE)


class _Target(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send(self, code, body=b"", ctype="text/plain; charset=utf-8", extra=None):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for key, value in (extra or {}).items():
            self.send_header(key, value)
        self.end_headers()
        if body:
            self.wfile.write(body)

    def _record(self, method, body=b""):
        with target_lock:
            target_hits.append((method, self.path, dict(self.headers), body))

    def do_GET(self):  # noqa: N802 - BaseHTTPRequestHandler API
        self._record("GET")
        path = urlsplit(self.path).path
        routes = {  # no CSP/XFO/HSTS headers on purpose (the Scanner check needs them)
            "/json": (200, json.dumps({"ok": True, "path": self.path}).encode(), "application/json"),
            "/echo": (200, ("echo:" + urlsplit(self.path).query).encode(), None),
            "/missing": (404, b"<html><body>not found</body></html>", "text/html; charset=utf-8"),
            "/big": (200, b"B" * (300 * 1024), None),
            "/html": (200, HTML_PAGE, "text/html; charset=utf-8"),
        }
        if path == "/redirect":
            self._send(302, b"", extra={"Location": "/html"})
        elif path in routes:
            code, body, ctype = routes[path]
            self._send(code, body, ctype or "text/plain; charset=utf-8")
        else:
            self._send(200, b"ok")

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        self._record("POST", body)
        self._send(200, json.dumps({"ok": True, "bytes": len(body)}).encode(), "application/json")

    def log_message(self, *args):
        pass


def start_target():
    server = ThreadingHTTPServer(("127.0.0.1", 0), _Target)
    threading.Thread(target=server.serve_forever, daemon=True, name="ui-audit-target").start()
    return server


class Proxy:
    def __init__(self, addr):
        host, _, port = addr.rpartition(":")
        self.host = host.strip("[]") or "127.0.0.1"
        self.port = int(port)
        self.url = f"http://{self.host}:{self.port}"

    def request(self, method, url, body=None, headers=None, timeout=30):
        """One absolute-URI request through the proxy; redirects are not followed."""
        conn = http.client.HTTPConnection(self.host, self.port, timeout=timeout)
        try:
            conn.request(method, url, body=body, headers=headers or {})
            resp = conn.getresponse()
            return resp.status, resp.read()
        finally:
            conn.close()


def seed_traffic(proxy, target_base):
    """Generate one flow per shape the UI has to render."""
    json_h = {"Content-Type": "application/json"}
    form_h = {"Content-Type": "application/x-www-form-urlencoded"}
    sends = [
        ("GET", "/json?probe=1&q=alpha", None, {}),
        ("POST", "/echo", json.dumps({"hello": "audit"}), json_h),
        ("POST", "/form", "field=value&other=two", form_h),
        ("GET", "/missing", None, {}),
        ("GET", "/redirect", None, {}),
        ("GET", "/big", None, {}),
        ("GET", "/html", None, {}),
    ]
    statuses = []
    for method, path, body, headers in sends:
        try:
            status, _ = proxy.request(method, target_base + path, body, headers)
        except Exception as exc:  # noqa: BLE001 - surfaced as the check detail
            status = f"error:{exc}"
        statuses.append((method, path, status))
    return statuses


def fire(proxy, url, sink, timeout=30):
    """Send one request through the proxy on a background thread."""
    def run():
        try:
            sink.append(proxy.request("GET", url, timeout=timeout)[0])
        except Exception as exc:  # noqa: BLE001
            sink.append(f"error:{exc}")
    thread = threading.Thread(target=run, daemon=True)
    thread.start()
    return thread


def settle(page, ms=500):
    page.wait_for_timeout(ms)


def dismiss_setup(page, tries=40):
    for _ in range(tries):
        skip = page.query_selector("#setupSkip")
        if skip and skip.is_visible():
            skip.click()
            return True
        page.wait_for_timeout(200)
    return False


def boot(page, base):
    page.goto(base + "/", wait_until="load", timeout=45000)
    dismiss_setup(page)
    page.wait_for_function(
        "() => { const t = document.querySelector('.tab[data-tab=\"proxy\"]'); return t && !t.disabled; }",
        timeout=45000)
    settle(page, 1400)


def reload_page(page):
    page.reload(wait_until="load")
    dismiss_setup(page)
    settle(page, 2000)


def go(page, tab, wait=500):
    """Activate a panel through its rail tab (JS click, so it works at phone width too)."""
    page.evaluate("(t) => document.querySelector('.tab[data-tab=\"' + t + '\"]')?.click()", tab)
    settle(page, wait)


def count(page, selector):
    return page.evaluate("(s) => document.querySelectorAll(s).length", selector)


def text(page, selector):
    el = page.query_selector(selector)
    return el.inner_text() if el else ""


def visible(page, selector):
    return page.evaluate("(s) => { const el = document.querySelector(s); return !!el && el.offsetParent !== null; }",
                        selector)


def api(page, path):
    return page.evaluate("(p) => fetch(p).then(r => r.json())", path)


def poll(fn, timeout, interval=0.5):
    """Return the first truthy value fn() yields, or None after the timeout."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        value = fn()
        if value:
            return value
        time.sleep(interval)
    return None


def wait_held(page, timeout=20):
    """Wait for the intercept hold queue to receive an item."""
    return poll(lambda: count(page, "#heldList .icpt-item"), timeout, 0.3) or 0


def check_overflow(page, width, height):
    """Every panel must fit the viewport horizontally at this size; keeps that size."""
    page.set_viewport_size({"width": width, "height": height})
    settle(page, 500)
    offenders = []
    for tab in TABS:
        go(page, tab, 320)
        over = page.evaluate(OVERFLOW_JS)
        if over:
            offenders.append(f"{tab}+{over}px")
    check(f"layout: no horizontal overflow at {width}x{height}", not offenders, ", ".join(offenders))


def panel_tour(page):
    """Visit every panel at 1440x900 and report what broke (or overflows)."""
    broken, empty, wide = [], [], []
    for tab in TABS:
        go(page, tab, 420)
        box = page.evaluate(PANEL_BOX_JS, tab)
        if not box or box["w"] <= 0 or box["h"] <= 0:
            broken.append(tab + ":not-visible")
        elif box["text"] == 0:
            empty.append(tab)
        over = page.evaluate(OVERFLOW_JS)
        if over:
            wide.append(f"{tab}+{over}px")
    unexpected = [c for c in console_problems if not expected_console(c)]
    rail = page.evaluate(RAIL_JS)
    check("boot: every rail tab is present and enabled",
          rail["tabs"] == list(TABS) and not rail["disabled"],
          f"tabs={rail['tabs']} disabled={rail['disabled']}")
    check("boot: every panel activates visibly", not broken, ", ".join(broken))
    check("boot: every panel renders content", not empty, ", ".join(empty))
    check("boot: no uncaught exceptions or unexpected console errors",
          not unexpected, "; ".join(f"{c['kind']}: {c['text'][:140]}" for c in unexpected[:3]))
    check("layout: no horizontal overflow at 1440x900", not wide, ", ".join(wide))


def run(args, run_id):
    proxy = Proxy(args.proxy)
    target_server = None
    if args.target:
        target_base = args.target.rstrip("/")
    else:
        target_server = start_target()
        target_base = f"http://127.0.0.1:{target_server.server_address[1]}"
        print(f"seed target: {target_base}")
    target_host = urlsplit(target_base).netloc
    statuses = seed_traffic(proxy, target_base)
    seed_ok = all(isinstance(st[2], int) and st[2] < 500 for st in statuses)
    check("seed: traffic generated through the proxy without 5xx", seed_ok,
          "; ".join(f"{m} {p} -> {st}" for m, p, st in statuses))

    finding_name = f"UI audit finding {run_id}"
    collection_name = f"Audit Collection {run_id}"
    identity_name = f"audit-identity-{run_id}"

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=not args.headed, executable_path=args.chrome)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda m: m.type == "error" and
                console_problems.append({"kind": "console", "text": m.text}))
        page.on("pageerror", lambda e: console_problems.append({"kind": "pageerror", "text": str(e)}))
        page.on("response", lambda r: r.status >= 500 and network_errors.append(f"{r.status} {r.url[:120]}"))

        boot(page, args.base_url)
        panel_tour(page)
        go(page, "proxy", 600)
        for width, height in VIEWPORTS[1:]:
            check_overflow(page, width, height)
        page.set_viewport_size({"width": 1440, "height": 900})
        go(page, "proxy", 500)

        dups = page.evaluate(DUP_IDS_JS)
        check("dom: no duplicate element ids", not dups, ", ".join(dups))

        flows = (api(page, "/api/flows?limit=200") or {}).get("flows") or []
        paths = [f.get("path", "") for f in flows]
        needed = ["/json?probe=1&q=alpha", "/echo", "/form", "/missing", "/redirect", "/big", "/html"]
        missing = [n for n in needed if not any(path.startswith(n) for path in paths)]
        check("seed: every seeded flow was captured", not missing, "missing " + ", ".join(missing))
        five_xx = [f"{f['method']} {f['path']} {f.get('status')}" for f in flows
                   if isinstance(f.get("status"), int) and f["status"] >= 500]
        check("seed: no 5xx among captured flows", not five_xx, "; ".join(five_xx[:5]))

        n_rows = count(page, "#rows .trow")
        check("history: list renders the captured flows", n_rows >= 7, f"rows={n_rows}")
        page.locator("#rows .trow").filter(has_text="/json").first.click()
        settle(page, 900)
        req_text, res_text = text(page, "#reqView"), text(page, "#resView")
        check("history: the inspector shows the request line and response body",
              "GET /json" in req_text and '"ok"' in res_text and "alpha" in res_text,
              f"req={req_text[:90]!r} res={res_text[:90]!r}")

        view_failures = []
        for view in ("raw", "headers", "json", "decoded", "hex", "pretty"):
            btn = page.query_selector(f'#inspect .seg[data-side="res"] button[data-view="{view}"]')
            if not btn:
                view_failures.append(view + ":missing")
                continue
            btn.click()
            settle(page, 260)
            if not text(page, "#resView").strip():
                view_failures.append(view + ":empty")
        check("history: every response view renders", not view_failures, ", ".join(view_failures))

        page.locator("#rows .trow").filter(has_text="/html").first.click()
        settle(page, 800)
        render_btn = page.query_selector('#inspect .seg[data-side="res"] button[data-view="render"]')
        if render_btn:
            render_btn.click()
            settle(page, 1200)
        mounted = page.evaluate(RENDER_FRAME_JS)
        check("render: the Render view mounts a sandboxed frame", bool(mounted), "no iframe")
        check("render: the frame carries the page content",
              bool(mounted) and "Hello world" in mounted["srcdoc"], (mounted or {}).get("srcdoc", "")[:120])
        check("render: the frame cannot execute scripts",
              bool(mounted) and "allow-scripts" not in mounted["sandbox"],
              f"sandbox={(mounted or {}).get('sandbox')}")

        go(page, "proxy", 500)
        page.evaluate("""() => { const s = document.querySelector('#fStatus'); s.value = '4'; s.onchange({ target: s }); }""")
        settle(page, 900)
        n4 = count(page, "#rows .trow")
        only_4xx = page.evaluate("() => [...document.querySelectorAll('#rows .trow')].every(r => /\\b4\\d\\d\\b/.test(r.innerText))")
        check("history: status filter 4xx narrows the list", n4 >= 1 and n4 < n_rows and only_4xx,
              f"rows={n4}/{n_rows} only4xx={only_4xx}")
        page.evaluate("""() => { const s = document.querySelector('#fStatus'); s.value = ''; s.onchange({ target: s }); }""")
        settle(page, 700)
        page.fill("#fSearch", "missing")
        settle(page, 900)
        ns = count(page, "#rows .trow")
        hit = page.evaluate("() => [...document.querySelectorAll('#rows .trow')].every(r => r.innerText.includes('/missing'))")
        check("history: search narrows to the matching flow", 0 < ns < n_rows and hit,
              f"rows={ns}/{n_rows} hit={hit}")
        page.fill("#fSearch", "")
        settle(page, 800)
        check("history: clearing search restores the list", count(page, "#rows .trow") == n_rows,
              f"rows={count(page, '#rows .trow')} want={n_rows}")

        page.locator("#rows .trow").filter(has_text="/json").first.click()
        settle(page, 500)
        page.click("#inspectCopyAs")
        settle(page, 350)
        menu_items = page.evaluate(CTXMENU_JS)
        check("history: copy-as menu lists formats", len(menu_items) >= 5, str(menu_items)[:200])
        page.keyboard.press("Escape")
        settle(page, 250)
        page.locator("#rows .trow").first.click(button="right")
        settle(page, 350)
        ctx_items = count(page, '#ctxmenu.show button, #ctxmenu.show [role="menuitem"]')
        check("history: row context menu opens", ctx_items >= 3, f"items={ctx_items}")
        page.keyboard.press("Escape")
        settle(page, 250)

        page.click("#inspectSendRepeater")
        settle(page, 900)
        go(page, "repeater", 700)
        rep_url = page.input_value("#repUrl")
        page.click("#repSend")
        settle(page, 2000)
        rep_res = text(page, "#repResView")
        check("repeater: receives the inspector flow and sends it",
              "/json" in rep_url and "200" in rep_res and "ok" in rep_res,
              f"url={rep_url[:80]!r} res={rep_res[:80]!r}")

        go(page, "intruder", 600)
        page.fill("#intrTarget", target_base)
        page.click('#intrType button[data-t="sniper"]')
        settle(page, 400)
        page.evaluate("(tpl) => { const ta = document.querySelector('#intrTemplate'); ta.value = tpl;"
                      " ta.dispatchEvent(new Event('input', {bubbles: true})); }",
                      f"GET /echo?p=\u00a7p\u00a7 HTTP/1.1\r\nHost: {target_host}\r\n\r\n")
        page.evaluate("(t) => { const ta = document.querySelector('#intrPayloadsWrap textarea[data-pos=\"s\"]');"
                      " ta.focus(); ta.value = t; ta.dispatchEvent(new InputEvent('input',"
                      " {inputType: 'insertFromPaste', bubbles: true})); ta.blur(); }", "one\ntwo\nthree")
        settle(page, 500)
        page.click("#intrStart")

        def finished():
            st = api(page, "/api/intruder/state") or {}
            return st if (not st.get("running") and st.get("done")) else None

        state = poll(finished, 60) or {}
        settle(page, 800)
        intr_rows = count(page, "#intrResults .intr-row")
        check("intruder: a 3-payload sniper run completes and lists every payload",
              state.get("done") == 3 and not state.get("running") and intr_rows == 3,
              f"state={json.dumps({k: state.get(k) for k in ('running', 'done', 'total')})} rows={intr_rows}")

        go(page, "scanner", 600)
        page.click("#scanRun")

        def issue_count():
            payload = api(page, "/api/scanner/issues") or {}
            return len(payload.get("issues", payload) or [])

        issues = poll(issue_count, 90, 1.0) or 0
        check("scanner: a run produces issues for the seeded flows", issues > 0, f"issues={issues}")
        settle(page, 900)
        scan_rows = count(page, '#scanList [data-issue], #scanList .scan-row, #scanList [role=option]')
        detail = ""
        if scan_rows:
            page.locator("#scanList [role=option], #scanList [data-issue]").first.click()
            settle(page, 800)
            detail = text(page, "#panel-scanner")
        check("scanner: the panel lists issues and opens the selected one",
              scan_rows > 0 and len(detail) > 120, f"rows={scan_rows} detail={detail[:120]!r}")

        go(page, "findings", 700)
        page.evaluate("""() => { const t = document.querySelector('#findingsUIEditingToggle');
            if (t && !t.classList.contains('on')) t.click(); }""")
        settle(page, 800)
        ok_btn = page.query_selector("#confirmOk")
        if ok_btn and ok_btn.is_visible():
            ok_btn.click()
            settle(page, 700)
        go(page, "findings", 600)
        check("findings: the editing gate turns on",
              page.evaluate("() => !!document.querySelector('#findingsUIEditingToggle.on')"),
              "toggle still off")
        page.click("#findNew")
        settle(page, 350)
        page.fill("#findInlineTitle", finding_name)
        page.click("#findInlineCreate")
        settle(page, 1600)
        check("findings: a new finding is created", finding_name in text(page, "#panel-findings"),
              "not listed")
        page.locator(".find-row").first.click()
        settle(page, 800)
        # Inline create opens the document in edit mode already; only toggle the
        # per-finding Edit control when rename is not on screen yet.
        if not page.query_selector("#findRename") and page.query_selector("#findToggleEdit"):
            page.click("#findToggleEdit")
            settle(page, 800)
        rename = page.query_selector("#findRename")
        if rename:
            page.click("#findRename")
            settle(page, 400)
            page.fill("#promptInput", finding_name + " renamed")
            page.click("#promptOk")
            settle(page, 1600)
        check("findings: the title renames through its edit-mode affordance",
              bool(rename) and finding_name + " renamed" in text(page, "#panel-findings"),
              f"rename_button={bool(rename)} panel={text(page, '#panel-findings')[:120]!r}")

        go(page, "collections", 700)
        page.click("#collNew")
        settle(page, 450)
        prompt_open = page.evaluate("() => { const m = document.querySelector('#promptModal'); return !!m && !m.hidden; }")
        check("collections: New asks for a name", prompt_open, "prompt did not open")
        if prompt_open:
            page.fill("#promptInput", collection_name)
            page.click("#promptOk")
            settle(page, 1400)
        check("collections: the collection is created",
              collection_name in text(page, "#panel-collections"), "not listed")

        go(page, "notes", 700)
        page.click('#notesSeg button[data-m="edit"]')
        settle(page, 400)
        page.click("#notesEdit")
        page.keyboard.type("Audit notes line one")
        settle(page, 800)
        page.click('#notesSeg button[data-m="view"]')
        settle(page, 700)
        check("notes: View mode renders the typed text",
              "Audit notes line one" in text(page, "#notesPreview"), text(page, "#notesPreview")[:120])

        go(page, "proxy", 800)
        page.locator("#rows .trow").filter(has_text="/missing").first.click()
        settle(page, 600)
        page.fill("#noteInput", "audit note 42")
        page.keyboard.press("Enter")
        settle(page, 800)
        before_theme = page.evaluate("() => document.documentElement.getAttribute('data-theme') || ''")
        page.click("#themeToggle")
        settle(page, 500)
        after_theme = page.evaluate("() => document.documentElement.getAttribute('data-theme') || ''")
        check("theme: the toggle changes the theme", after_theme != before_theme,
              f"{before_theme} -> {after_theme}")

        reload_page(page)
        go(page, "findings", 900)
        check("findings: the renamed title survives a reload",
              finding_name + " renamed" in text(page, "#panel-findings"),
              text(page, "#panel-findings")[:160])
        go(page, "collections", 900)
        check("collections: the collection survives a reload",
              collection_name in text(page, "#panel-collections"), "not listed")
        go(page, "proxy", 900)
        page.locator("#rows .trow").filter(has_text="/missing").first.click()
        settle(page, 800)
        held_note = page.input_value("#noteInput")
        check("notes: the per-flow note survives a reload", held_note == "audit note 42", held_note)
        check("theme: the choice survives a reload",
              page.evaluate("() => document.documentElement.getAttribute('data-theme') || ''") == after_theme,
              "theme reverted")

        go(page, "identities", 700)
        page.click("#identNew")
        settle(page, 700)
        page.fill("#identEdName", identity_name)
        page.fill("#identEdHeaders", "Authorization: Bearer audit-token")
        page.click("#identEdSave")
        settle(page, 400)
        saved = False
        for _ in range(20):
            if not visible(page, "#identEdName"):
                saved = True
                break
            settle(page, 300)
        if not saved:  # a rejected save keeps the sheet open; close it for the next step
            page.keyboard.press("Escape")
            settle(page, 500)
        listed = identity_name in text(page, "#panel-identities")
        check("identities: a new identity saves and the sheet closes", saved and listed,
              f"saved={saved} listed={listed}")
        page.click("#identNew")
        settle(page, 700)
        sheet_open = visible(page, "#identEdName")
        page.keyboard.press("Escape")
        settle(page, 600)
        check("identities: Escape closes the identity sheet",
              sheet_open and not visible(page, "#identEdName"),
              f"opened={sheet_open} closed={not visible(page, '#identEdName')}")

        go(page, "intercept", 600)
        page.click("#interceptToggle")
        settle(page, 600)
        pressed = page.get_attribute("#interceptToggle", "aria-pressed")
        check("intercept: interception toggles on", pressed == "true", f"aria-pressed={pressed}")

        client = []
        thread = fire(proxy, target_base + "/json?held=1", client)
        held = wait_held(page)
        check("intercept: the request is held in the queue", held >= 1, f"items={held}")
        if held:
            page.locator("#heldList .icpt-item").first.click()
            settle(page, 600)
            raw = page.evaluate("() => document.querySelector('#heldRaw')?.value || ''")
            check("intercept: the held editor shows the message", "GET /json?held=1" in raw, raw[:120])
            page.evaluate("(value) => { const ta = document.querySelector('#heldRaw'); ta.focus();"
                          " ta.value = value; ta.dispatchEvent(new Event('input', {bubbles: true})); ta.blur(); }",
                          raw.replace("/json?held=1", "/json?held=audited"))
            settle(page, 400)
            page.click("#forwardBtn")
            settle(page, 1500)
            thread.join(timeout=25)
            with target_lock:
                saw_edit = any("/json?held=audited" in hit[1] for hit in target_hits)
            check("intercept: forwarding the edited request reaches the target",
                  bool(client) and client[0] == 200 and saw_edit,
                  f"client={client} target_saw_edit={saw_edit}")
            check("intercept: the forwarded item leaves the queue",
                  count(page, "#heldList .icpt-item") == 0,
                  f"remaining={count(page, '#heldList .icpt-item')}")

        # Drop is deferred behind an Undo toast; Undo must keep the request held.
        drop_sink = []
        drop_thread = fire(proxy, target_base + "/json?held=drop", drop_sink)
        held = wait_held(page)
        undo_label = ""
        if held:
            page.locator("#heldList .icpt-item").first.click()
            settle(page, 500)
            page.click("#dropBtn")
            settle(page, 900)
            undo_label = page.evaluate(UNDO_LABEL_JS)
            if undo_label:
                page.evaluate(UNDO_CLICK_JS)
                settle(page, 1500)
        remaining = count(page, "#heldList .icpt-item")
        check("intercept: drop offers Undo, and Undo keeps the request on hold",
              undo_label == "Undo" and remaining >= 1, f"label={undo_label!r} items={remaining}")
        page.evaluate("() => document.querySelector('#forwardBtn')?.click()")
        settle(page, 1200)
        page.click("#interceptToggle")   # turning intercept off releases anything held
        settle(page, 800)
        drop_thread.join(timeout=20)

        go(page, "map", 800)
        page.click('#mapViewSeg button[data-v="graph"]')
        settle(page, 1400)
        nodes = page.evaluate("() => document.querySelector('#mapGraphG')?.childElementCount || 0")
        check("map: the graph view renders endpoint nodes", nodes > 0, f"nodes={nodes}")

        go(page, "proxy", 600)

        def palette_open(keys):
            page.keyboard.press(keys)
            settle(page, 500)
            return page.evaluate(PALETTE_JS)

        ctrl_open = palette_open("Control+k")
        page.keyboard.press("Escape")
        settle(page, 300)
        meta_open = palette_open("Meta+k")
        check("palette: Ctrl+K and Cmd+K both open the command palette",
              ctrl_open and meta_open, f"ctrl={ctrl_open} meta={meta_open}")
        if not meta_open and ctrl_open:
            page.keyboard.press("Control+k")
            settle(page, 300)
        page.keyboard.type("f:json")
        settle(page, 700)
        hits = count(page, '#cmdkList [role=option], #cmdkList .cmdk-row, #cmdkList li')
        page.keyboard.press("Enter")
        settle(page, 1400)
        drawer = page.evaluate(DRAWER_JS)
        check("palette: f: lists flows and Enter opens the Flow Drawer for one",
              hits > 0 and bool(drawer and drawer["open"] and "/json" in drawer["text"]),
              f"hits={hits} drawer={str(drawer)[:160]}")
        # The drawer is a bottom sheet over the rail: close it before phone nav.
        closed_by = ""
        drawer_open = lambda: page.evaluate("() => !!document.querySelector('#flowDrawer.is-open')")
        if drawer_open():
            page.keyboard.press("Escape")
            settle(page, 500)
            closed_by = "Escape"
            if drawer_open():
                page.click("#fdClose")
                settle(page, 500)
                closed_by = "close button"
        check("palette: the opened Flow Drawer closes again", bool(closed_by), "drawer stuck open")

        page.set_viewport_size({"width": 390, "height": 844})
        settle(page, 700)
        tabs_display = page.evaluate("() => getComputedStyle(document.querySelector('#tabs')).display")
        check("phone: the rail is hidden at phone width", tabs_display == "none", tabs_display)
        page.locator("#dock .dock-btn").filter(has_text="Report").first.click()
        settle(page, 700)
        seg_buttons = count(page, "#dockSeg button")
        if seg_buttons:
            page.locator("#dockSeg button").filter(has_text="Findings").first.click()
            settle(page, 700)
        active = page.evaluate("() => document.querySelector('.tab.active')?.dataset.tab || ''")
        height = page.evaluate("() => document.getElementById('panel-findings')?.getBoundingClientRect().height || 0")
        check("phone: the dock navigates the panels and the target panel renders",
              active == "findings" and seg_buttons > 0 and height > 80,
              f"active={active} seg={seg_buttons} height={height}")

        unexpected = [c for c in console_problems if not expected_console(c)]
        check("console: no unexpected console errors", not unexpected,
              "; ".join(f"{c['kind']}: {c['text'][:140]}" for c in unexpected[:3]))
        check("network: no HTTP 5xx from the control UI", not network_errors,
              "; ".join(network_errors[:5]))
        browser.close()

    if target_server:
        target_server.shutdown()


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Self-seeding browser interaction audit for the Interseptor control UI.")
    parser.add_argument("--base-url", required=True, help="control UI origin, e.g. http://127.0.0.1:19176")
    parser.add_argument("--proxy", required=True, help="proxy listener to seed traffic through, host:port")
    parser.add_argument("--target", help="target origin; a loopback seed server starts when omitted")
    parser.add_argument("--chrome", help=f"Chrome binary (default: $UI_AUDIT_CHROME or {DEFAULT_CHROME})")
    parser.add_argument("--json", dest="json_out", help="write the JSON report to this path")
    parser.add_argument("--headed", action="store_true", help="run Chrome with a visible window")
    parser.add_argument("--allow-remote", action="store_true", help="allow a non-loopback --base-url")
    args = parser.parse_args(argv)

    base = args.base_url.rstrip("/")
    parts = urlsplit(base)
    if parts.scheme not in ("http", "https"):
        print(f"error: --base-url must be an http(s) URL, got {args.base_url!r}", file=sys.stderr)
        return 2
    try:
        base_port = parts.port or (443 if parts.scheme == "https" else 80)
        proxy_port = int(args.proxy.rpartition(":")[2])
        target_port = (urlsplit(args.target).port or 80) if args.target else 0
    except ValueError:
        print("error: --base-url/--proxy/--target need explicit, valid ports", file=sys.stderr)
        return 2
    clash = sorted({p for p in (base_port, proxy_port, target_port) if p in FORBIDDEN_PORTS})
    if clash:
        print("error: refusing to run against port(s) " + ", ".join(str(p) for p in clash)
              + ". 9966/8080 belong to a real Interseptor instance; this audit seeds flows and "
                "rewrites persisted state (findings, notes, identities, settings). Start a "
                "throwaway instance on spare ports and point --base-url/--proxy at those.",
              file=sys.stderr)
        return 2
    if parts.hostname not in LOOPBACK and not args.allow_remote:
        print(f"error: --base-url host {parts.hostname!r} is not loopback. The audit mutates the "
              "project it drives; pass --allow-remote only for a disposable instance you own.",
              file=sys.stderr)
        return 2
    chrome = args.chrome or os.environ.get("UI_AUDIT_CHROME") or DEFAULT_CHROME
    if not os.path.exists(chrome):
        print(f"error: Chrome not found at {chrome}. Pass --chrome PATH or set UI_AUDIT_CHROME.",
              file=sys.stderr)
        return 2
    args.chrome = chrome

    started = time.time()
    run_id = time.strftime("%H%M%S")
    try:
        run(args, run_id)
    except Exception as exc:  # noqa: BLE001 - a crash is a failed audit, not a traceback wall
        check("audit: run completed without an exception", False, f"{type(exc).__name__}: {exc}")
    duration = round(time.time() - started, 1)

    failed = [r for r in results if not r["ok"]]
    summary = {
        "base_url": base, "proxy": args.proxy, "target": args.target or "self-seeded",
        "chrome": args.chrome, "duration_s": duration, "total": len(results),
        "passed": len(results) - len(failed), "failed": len(failed), "ok": not failed,
        "checks": results,
        "unexpected_console": [c for c in console_problems if not expected_console(c)],
        "expected_console": [c for c in console_problems if expected_console(c)],
        "http_5xx": network_errors,
    }
    print()
    print(f"{summary['passed']}/{summary['total']} checks passed in {duration}s")
    for failure in failed:
        print(f"FAILED: {failure['check']} — {failure['detail']}")
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as fh:
            json.dump(summary, fh, indent=2)
        print(f"report: {args.json_out}")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
