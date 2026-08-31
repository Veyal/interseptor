#!/usr/bin/env python3
"""Repeatable Playwright + Chrome DevTools Protocol audit for Interseptor's UI.

Run against a fresh, isolated Interseptor project. The default smoke pass is
read-only. ``--full`` creates generic loopback traffic and temporary project
records to verify real pending/success states, cross-tool workflows, burst
behavior, Repeater history ownership, screenshots, and performance metrics.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import http.client
import json
import sys
import threading
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple
from urllib.parse import urlsplit

try:
    from playwright.sync_api import Browser, BrowserContext, Page, sync_playwright
except ImportError as exc:  # pragma: no cover - operator guidance
    raise SystemExit(
        "Playwright is required for this verification tool. Install it outside "
        "the application runtime, then run `playwright install chromium`."
    ) from exc


TOP_LEVEL_TABS = (
    "proxy",
    "intercept",
    "repeater",
    "intruder",
    "scanner",
    "map",
    "findings",
    "notes",
    "activity",
    "settings",
)
VIEWPORTS = ((1440, 900), (1024, 768), (390, 844))
BASELINE_SCREENSHOTS = {
    "1440x900": "docs/ui-audit/before-1440x900-proxy.png",
    "1024x768": "docs/ui-audit/before-1024x768-map.png",
    "390x844": "docs/ui-audit/before-390x844-scanner.png",
}


class FixtureHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _respond(self) -> None:
        length = int(self.headers.get("Content-Length", "0") or "0")
        body_in = self.rfile.read(length) if length else b""
        body = json.dumps(
            {
                "ok": True,
                "method": self.command,
                "path": self.path,
                "received": len(body_in),
            },
            separators=(",", ":"),
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    do_GET = _respond
    do_POST = _respond
    do_PUT = _respond
    do_PATCH = _respond
    do_DELETE = _respond
    do_HEAD = _respond

    def log_message(self, _format: str, *_args: Any) -> None:
        return


@dataclass
class AuditResult:
    cases: Dict[str, str] = field(default_factory=dict)
    failures: List[str] = field(default_factory=list)
    console_errors: List[str] = field(default_factory=list)
    expected_console_errors: List[str] = field(default_factory=list)
    expected_console_request_urls: List[str] = field(default_factory=list)
    page_errors: List[str] = field(default_factory=list)
    http_errors: List[str] = field(default_factory=list)
    expected_http_errors: List[str] = field(default_factory=list)
    external_requests: List[str] = field(default_factory=list)
    metrics: Dict[str, Any] = field(default_factory=dict)

    def run(self, name: str, operation: Callable[[], None]) -> None:
        try:
            operation()
            self.cases[name] = "pass"
            print(f"PASS  {name}")
        except Exception as exc:  # keep the loop exhaustive
            message = f"{name}: {type(exc).__name__}: {exc}"
            self.cases[name] = "fail"
            self.failures.append(message)
            print(f"FAIL  {message}", file=sys.stderr)

    def require(self, condition: bool, message: str) -> None:
        if not condition:
            raise AssertionError(message)


def parse_host_port(value: str) -> Tuple[str, int]:
    host, separator, port = value.rpartition(":")
    if not separator or not host:
        raise argparse.ArgumentTypeError("expected HOST:PORT")
    try:
        return host, int(port)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("port must be an integer") from exc


def proxy_request(proxy: Tuple[str, int], url: str, method: str = "GET", body: bytes = b"") -> Tuple[int, bytes]:
    connection = http.client.HTTPConnection(proxy[0], proxy[1], timeout=20)
    headers = {"Connection": "close", "X-Interseptor-Audit": "generic-loopback"}
    if body:
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = str(len(body))
    try:
        connection.request(method, url, body=body, headers=headers)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


def start_fixture() -> Tuple[ThreadingHTTPServer, threading.Thread]:
    server = ThreadingHTTPServer(("127.0.0.1", 0), FixtureHandler)
    thread = threading.Thread(target=server.serve_forever, name="ui-audit-fixture", daemon=True)
    thread.start()
    return server, thread


def png_dimensions(path: Path) -> Tuple[int, int]:
    """Read PNG dimensions without adding an image-processing dependency."""
    data = path.read_bytes()
    if data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR" or len(data) < 24:
        raise ValueError(f"{path} is not a valid PNG with an IHDR")
    return int.from_bytes(data[16:20], "big"), int.from_bytes(data[20:24], "big")


def percentile(values: List[float], percentile_value: float) -> Optional[float]:
    if not values:
        return None
    ordered = sorted(float(value) for value in values)
    index = min(len(ordered) - 1, max(0, int(round((percentile_value / 100) * len(ordered) - 1))))
    return round(ordered[index], 3)


def wait_ready(page: Page) -> None:
    page.wait_for_selector('#tabs[aria-busy="false"]', timeout=20_000)
    page.wait_for_function("[...document.querySelectorAll('.tab')].every(tab=>!tab.disabled)")
    # A brand-new isolated project intentionally opens the setup wizard. The
    # audit verifies workstation panels, so record the local "Skip" choice and
    # leave system-proxy/CA state untouched.
    page.wait_for_timeout(500)
    setup = page.locator("#setupModal")
    if setup.is_visible():
        page.locator("#setupSkip").click()


def attach_observers(page: Page, result: AuditResult, base_netloc: str, expected_console_errors: bool = False) -> None:
    http_sink = result.expected_http_errors if expected_console_errors else result.http_errors

    def record_console(message: Any) -> None:
        if message.type != "error":
            return
        location = message.location or {}
        request_url = str(location.get("url") or "")
        if expected_console_errors or request_url in result.expected_console_request_urls:
            result.expected_console_errors.append(message.text)
        else:
            result.console_errors.append(message.text)

    page.on("console", record_console)
    page.on("pageerror", lambda error: result.page_errors.append(str(error)))
    page.on(
        "response",
        lambda response: http_sink.append(
            f"{response.status} {response.request.method} {response.url}"
        )
        if response.status >= 400
        else None,
    )

    def record_request(request: Any) -> None:
        parsed = urlsplit(request.url)
        if parsed.scheme in ("http", "https") and parsed.netloc != base_netloc:
            result.external_requests.append(request.url)

    page.on("request", record_request)


def active_panel_state(page: Page, name: str) -> Dict[str, Any]:
    return page.evaluate(
        """name => {
          const tab=document.querySelector(`.tab[data-tab="${name}"]`);
          const panel=document.querySelector(`.panel[data-panel="${name}"]`);
          return {
            tabSelected:tab?.getAttribute('aria-selected'),
            tabActive:tab?.classList.contains('active'),
            tabIndex:tab?.tabIndex,
            panelActive:panel?.classList.contains('active'),
            panelVisible:panel?getComputedStyle(panel).display!=='none':false,
            activePanels:document.querySelectorAll('.panel.active').length,
            labelledBy:panel?.getAttribute('aria-labelledby'),
          };
        }""",
        name,
    )


def activate_with_motion_sample(page: Page, name: str) -> List[Dict[str, Any]]:
    return page.evaluate(
        """name => {
          document.querySelector(`.tab[data-tab="${name}"]`).click();
          const panel=document.querySelector(`.panel[data-panel="${name}"]`);
          return panel.getAnimations().map(animation=>({
            duration:animation.effect?.getTiming().duration,
            frames:animation.effect?.getKeyframes().map(frame=>({
              opacity:frame.opacity,transform:frame.transform
            }))||[]
          }));
        }""",
        name,
    )


def indexed_history_count(page: Page) -> int:
    return page.evaluate(
        """async () => await new Promise((resolve,reject)=>{
          const request=indexedDB.open('interseptor-repeater-history',1);
          request.onupgradeneeded=()=>{
            const db=request.result;
            const store=db.objectStoreNames.contains('entries')
              ? request.transaction.objectStore('entries')
              : db.createObjectStore('entries',{keyPath:'key'});
            if(!store.indexNames.contains('tabKey'))store.createIndex('tabKey','tabKey',{unique:false});
          };
          request.onerror=()=>reject(request.error);
          request.onsuccess=()=>{
            const db=request.result;
            if(!db.objectStoreNames.contains('entries')){resolve(0);return;}
            const count=db.transaction('entries','readonly').objectStore('entries').count();
            count.onsuccess=()=>resolve(count.result);
            count.onerror=()=>reject(count.error);
          };
        })"""
    )


def cdp_metrics(session: Any) -> Dict[str, float]:
    raw = session.send("Performance.getMetrics").get("metrics", [])
    return {item["name"]: item["value"] for item in raw}


def metric_delta(before: Dict[str, float], after: Dict[str, float], name: str) -> float:
    return round(after.get(name, 0.0) - before.get(name, 0.0), 6)


def run_audit(args: argparse.Namespace) -> AuditResult:
    result = AuditResult()
    base = args.base_url.rstrip("/")
    base_netloc = urlsplit(base).netloc
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=True)
    fixture: Optional[ThreadingHTTPServer] = None
    baseline_artifacts: Dict[str, Dict[str, Any]] = {}

    def verify_baseline_artifacts() -> None:
        repo_root = Path(__file__).resolve().parents[1]
        for viewport, relative in BASELINE_SCREENSHOTS.items():
            path = repo_root / relative
            result.require(path.is_file(), f"missing before screenshot baseline: {relative}")
            width, height = png_dimensions(path)
            expected_width, expected_height = (int(viewport.split("x")[0]), int(viewport.split("x")[1]))
            result.require((width, height) == (expected_width, expected_height), f"{relative} is {width}x{height}, expected {viewport}")
            baseline_artifacts[viewport] = {"path": relative, "width": width, "height": height}

    with sync_playwright() as playwright:
        result.run("before screenshot baselines are present and dimensioned", verify_baseline_artifacts)
        browser: Browser = playwright.chromium.launch(headless=not args.headed)
        context: BrowserContext = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        page.set_default_timeout(10_000)
        attach_observers(page, result, base_netloc)
        page.goto(base, wait_until="domcontentloaded")
        wait_ready(page)

        def navigation_semantics() -> None:
            sampled = []
            for name in TOP_LEVEL_TABS:
                animations = activate_with_motion_sample(page, name)
                state = active_panel_state(page, name)
                result.require(state["tabSelected"] == "true", f"{name} tab is not selected")
                result.require(state["tabActive"] and state["panelActive"], f"{name} tab/panel diverged")
                result.require(state["panelVisible"], f"{name} panel is hidden")
                result.require(state["activePanels"] == 1, f"{name} left multiple active panels")
                result.require(state["tabIndex"] == 0, f"{name} is not the roving tab stop")
                result.require(state["labelledBy"] == f"tab-{name}", f"{name} panel label is stale")
                sampled.extend(animations)
            durations = [float(item["duration"]) for item in sampled if isinstance(item.get("duration"), (int, float))]
            result.require(bool(durations), "main panel transitions did not run in normal-motion mode")
            result.require(all(160 <= duration <= 200 for duration in durations), f"panel durations outside 160–200ms: {durations}")
            transforms = [frame.get("transform", "") for item in sampled for frame in item.get("frames", [])]
            result.require(not any("7px" in value or "8px" in value for value in transforms), "panel travel exceeds 6px")
            result.metrics["panel_transition_ms"] = durations

            proxy_tab = page.locator('.tab[data-tab="proxy"]')
            proxy_tab.click()
            proxy_tab.focus()
            proxy_tab.press("ArrowDown")
            result.require(page.locator('.tab[data-tab="intercept"]').get_attribute("aria-selected") == "true", "ArrowDown did not activate Intercept")
            result.require(page.evaluate("document.activeElement?.dataset.tab") == "intercept", "tab focus did not follow ArrowDown")
            page.keyboard.press("End")
            result.require(page.evaluate("document.activeElement?.dataset.tab") == "settings", "End did not focus Settings")
            page.keyboard.press("Home")
            result.require(page.evaluate("document.activeElement?.dataset.tab") == "proxy", "Home did not focus Proxy")
            result.require(page.locator('.tab[data-tab="proxy"]').evaluate("el=>el.matches(':focus-visible')"), "keyboard focus is not visible on main navigation")

        result.run("main navigation, motion, and keyboard semantics", navigation_semantics)

        def inner_tabs_and_settings() -> None:
            for name, panel_id, bar_id in (
                ("repeater", "repTabPanel", "repTabs"),
                ("intruder", "intrTabPanel", "intrTabs"),
            ):
                page.locator(f'.tab[data-tab="{name}"]').click()
                selected = page.locator(f"#{bar_id} .rt-select[aria-selected='true']")
                result.require(selected.count() == 1, f"{name} has no single selected task tab")
                result.require(page.locator(f"#{panel_id}").get_attribute("aria-labelledby") == selected.get_attribute("id"), f"{name} inner tabpanel has the wrong label")

            page.locator('.tab[data-tab="settings"]').click()
            sections = page.locator("#setNav button[data-sec]").evaluate_all("buttons=>buttons.map(button=>button.dataset.sec)")
            result.require(len(sections) == 8, f"expected 8 Settings sections, found {sections}")
            for section in sections:
                page.locator(f'#setNav button[data-sec="{section}"]').click()
                visible = page.locator(f'.set-sec[data-sec="{section}"]').evaluate("el=>!el.hidden&&getComputedStyle(el).display!=='none'")
                result.require(visible, f"Settings section {section} did not open")
            search = page.locator("#setSearch")
            search.fill("certificate")
            result.require(page.locator("#setNav button[data-sec]:visible").count() > 0, "Settings search hid every result")
            search.fill("")

        result.run("Repeater/Intruder inner tabs and every Settings section", inner_tabs_and_settings)

        def viewport_and_reduced_motion() -> None:
            for width, height in VIEWPORTS:
                page.set_viewport_size({"width": width, "height": height})
                page.locator('.tab[data-tab="proxy"]').click()
                overflow = page.evaluate("document.documentElement.scrollWidth-document.documentElement.clientWidth")
                result.require(overflow == 0, f"{width}x{height} has {overflow}px document overflow")

            reduced = browser.new_context(
                viewport={"width": 1024, "height": 768}, reduced_motion="reduce"
            )
            reduced_page = reduced.new_page()
            reduced_page.set_default_timeout(10_000)
            attach_observers(reduced_page, result, base_netloc)
            reduced_page.goto(base, wait_until="domcontentloaded")
            wait_ready(reduced_page)
            reduced_page.locator('.tab[data-tab="intercept"]').click()
            state = reduced_page.evaluate(
                """() => {
                  const panels=[...document.querySelectorAll('.panel')];
                  const style=getComputedStyle(document.querySelector('.panel.active'));
                  const animated=[...document.querySelectorAll('*')].filter(el=>{
                    const s=getComputedStyle(el);
                    return s.animationDuration!=='0s' || s.transitionDuration!=='0s';
                  });
                  return {
                    animations:document.getAnimations().length,
                    transitionDuration:style.transitionDuration,
                    animationDuration:style.animationDuration,
                    animatedElements:animated.length,
                    panelTransitions:panels.map(panel=>getComputedStyle(panel).transitionDuration),
                    selected:document.querySelector('.tab.active')?.getAttribute('aria-selected')
                  };
                }"""
            )
            result.require(state["animations"] == 0, f"reduced motion left {state['animations']} active animations")
            result.require(state["transitionDuration"] in ("0s", "0ms"), f"reduced transition remained {state['transitionDuration']}")
            result.require(state["animationDuration"] in ("0s", "0ms"), f"reduced animation remained {state['animationDuration']}")
            result.require(state["animatedElements"] == 0, f"reduced motion left {state['animatedElements']} animated elements")
            result.require(all(value in ("0s", "0ms") for value in state["panelTransitions"]), f"reduced panel transitions remained {state['panelTransitions']}")
            result.require(state["selected"] == "true", "reduced motion lost selected state")
            motion_state = reduced_page.evaluate(
                """async () => {
                  const motion=await import('./js/motion.js');
                  const panel=document.querySelector('.panel.active');
                  const returned=motion.animateOnce(panel,[{opacity:0},{opacity:1}],{duration:260});
                  await returned;
                  return {
                    prefersReducedMotion:motion.prefersReducedMotion(),
                    returnedAnimation:!!returned && typeof returned.cancel==='function',
                    activeAnimations:document.getAnimations().length
                  };
                }"""
            )
            result.require(motion_state["prefersReducedMotion"], "reduced context did not reach motion.js reduced-motion branch")
            result.require(not motion_state["returnedAnimation"], "animateOnce returned an Animation under reduced motion")
            result.require(motion_state["activeAnimations"] == 0, "animateOnce created a reduced-motion animation")
            # Trigger state changes that normally use one-shot motion. These
            # controls remain meaningful under reduced motion even without a
            # captured request or a graph node.
            reduced_page.locator('.tab[data-tab="map"]').click()
            reduced_page.locator("#mapHideNoise").click()
            reduced_page.locator("#mapHideNoise").click()
            reduced_page.locator('.tab[data-tab="intercept"]').click()
            reduced_page.wait_for_timeout(80)
            reduced_page.evaluate("""() => {
              const active=document.querySelector('.panel.active');
              const s=getComputedStyle(active);
              if(document.getAnimations().length)throw new Error('state event created reduced-motion animation');
              if(s.transitionDuration!=='0s'&&s.transitionDuration!=='0ms')throw new Error('state event retained panel transition');
            }""")
            reduced.close()

        result.run("required viewports and reduced motion", viewport_and_reduced_motion)
        page.set_viewport_size({"width": 1440, "height": 900})

        def fault_states() -> None:
            fault = browser.new_context(viewport={"width": 1024, "height": 768})
            fault_page = fault.new_page()
            fault_page.set_default_timeout(10_000)
            attach_observers(fault_page, result, base_netloc, expected_console_errors=True)
            fault_page.route(
                "**/api/activity*",
                lambda route: route.fulfill(status=503, content_type="application/json", body='{"error":"audit unavailable"}'),
            )
            fault_page.route(
                "**/api/flows?*",
                lambda route: route.fulfill(status=503, content_type="application/json", body='{"error":"audit unavailable"}'),
            )
            try:
                fault_page.goto(base, wait_until="domcontentloaded")
                wait_ready(fault_page)
                fault_page.locator('.tab[data-tab="activity"]').click()
                fault_page.wait_for_selector("#actFeed [data-load-retry]", timeout=10_000)
                fault_page.locator('.tab[data-tab="proxy"]').click()
                fault_page.wait_for_selector("#flowCapRetry:not([hidden])", timeout=10_000)
            finally:
                fault.close()

        result.run("persistent retry states under request failure", fault_states)

        if args.full:
            fixture, _fixture_thread = start_fixture()
            fixture_base = f"http://127.0.0.1:{fixture.server_port}"
            proxy = args.proxy

            def setup_action_ack_lock() -> None:
                """A held setup-scope acknowledgement must lock dismissal/navigation."""
                setup_context = browser.new_context(viewport={"width": 1024, "height": 768})
                setup_page = setup_context.new_page()
                setup_page.set_default_timeout(10_000)
                attach_observers(setup_page, result, base_netloc, expected_console_errors=True)
                held_scope: List[Any] = []

                def hold_scope_add(route: Any) -> None:
                    if route.request.method == "POST":
                        held_scope.append(route)
                    else:
                        route.continue_()

                setup_page.route("**/api/scope", hold_scope_add)
                try:
                    setup_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(setup_page)
                    setup_page.locator('.tab[data-tab="settings"]').click()
                    setup_page.locator('#setNav button[data-sec="project"]').click()
                    setup_page.wait_for_selector("#runSetupBtn", state="visible", timeout=10_000)
                    setup_page.locator("#runSetupBtn").click()
                    setup_page.wait_for_selector("#setupModal", state="visible", timeout=10_000)
                    result.require(
                        setup_page.locator("#setupReadiness").get_attribute("role") == "status"
                        and setup_page.locator("#setupReadiness").get_attribute("aria-live") == "polite",
                        "setup readiness changes were not exposed as a polite status",
                    )
                    setup_page.locator("#setupNext").click()
                    setup_page.wait_for_function("document.querySelector('#setupStep')?.textContent.trim()==='2 / 4'")
                    copy_command = setup_page.locator("#setupCopyCmd")
                    if copy_command.count():
                        result.require(
                            copy_command.get_attribute("aria-label") == "Copy trust command",
                            "setup trust-command copy control had no direct accessible name",
                        )
                    setup_page.locator("#setupBack").click()
                    setup_page.wait_for_function("document.querySelector('#setupStep')?.textContent.trim()==='1 / 4'")
                    result.require(
                        setup_page.locator("#setupNext").is_enabled(),
                        "setup CA trust gate remained attached after navigating Back",
                    )
                    setup_page.locator("#setupNext").click()
                    setup_page.wait_for_function("document.querySelector('#setupStep')?.textContent.trim()==='2 / 4'")
                    setup_page.locator("#setupTrusted").check()
                    setup_page.locator("#setupNext").click()
                    setup_page.wait_for_function("document.querySelector('#setupStep')?.textContent.trim()==='3 / 4'")
                    result.require(
                        setup_page.locator("#setupScopeHost").get_attribute("aria-label") == "Scope host",
                        "setup scope textbox had no direct accessible name",
                    )
                    setup_page.locator("#setupScopeHost").fill("example.com")
                    setup_page.locator("#setupScopeAdd").click()
                    deadline = time.monotonic() + 2.0
                    while not held_scope and time.monotonic() < deadline:
                        setup_page.wait_for_timeout(10)
                    result.require(held_scope, "setup Add-to-scope did not issue its POST")
                    result.require(
                        setup_page.evaluate(
                            """() => ['setupNext','setupBack','setupSkip','setupScopeAdd'].every(id=>{
                              const el=document.querySelector('#'+id);
                              return el?.disabled===true && el?.getAttribute('aria-busy')==='true';
                            })"""
                        ),
                        "setup navigation/action controls were not locked while Add-to-scope was pending",
                    )
                    result.require(
                        setup_page.locator("#setupScopeHost").is_disabled()
                        and setup_page.locator("#setupScopeHost").get_attribute("aria-busy") == "true",
                        "setup scope input remained editable while Add-to-scope was pending",
                    )
                    setup_page.keyboard.press("Escape")
                    setup_page.wait_for_timeout(80)
                    result.require(setup_page.locator("#setupModal").is_visible(), "Escape dismissed setup during a held action")
                    setup_page.locator("#setupModal").click(position={"x": 5, "y": 5})
                    setup_page.wait_for_timeout(80)
                    result.require(setup_page.locator("#setupModal").is_visible(), "backdrop dismissed setup during a held action")
                    held_scope[0].fulfill(status=200, content_type="application/json", body='{"ok":true}')
                    setup_page.wait_for_function(
                        """() => {
                          const add=document.querySelector('#setupScopeAdd');
                          const msg=document.querySelector('#setupScopeMsg')?.textContent||'';
                          return add?.disabled===false && !add?.hasAttribute('aria-busy') && msg.includes('added example.com to scope');
                        }""",
                        timeout=10_000,
                    )
                    result.require(
                        setup_page.locator("#setupNext").is_enabled()
                        and setup_page.locator("#setupBack").is_enabled()
                        and setup_page.locator("#setupSkip").is_enabled(),
                        "setup navigation did not restore after Add-to-scope acknowledgement",
                    )
                    result.require(
                        setup_page.locator("#setupScopeHost").is_enabled()
                        and setup_page.locator("#setupScopeHost").get_attribute("aria-busy") is None,
                        "setup scope input did not restore after Add-to-scope acknowledgement",
                    )
                    setup_page.wait_for_timeout(80)
                    result.require(len(held_scope) == 1, "setup Add-to-scope issued duplicate POST requests")
                    setup_page.locator("#setupSkip").click()
                    setup_page.wait_for_selector("#setupModal", state="hidden", timeout=10_000)
                finally:
                    for route in held_scope:
                        try:
                            route.fulfill(status=200, content_type="application/json", body='{"ok":true}')
                        except Exception:
                            pass
                    try:
                        setup_page.unroute("**/api/scope", hold_scope_add)
                    except Exception:
                        pass
                    setup_context.close()

            result.run("setup Add-to-scope locks dismissal until acknowledgement", setup_action_ack_lock)

            def custom_check_load_failure() -> None:
                """A failed custom-check GET clears stale source and blocks writes."""
                check_context = browser.new_context(viewport={"width": 1024, "height": 768})
                check_page = check_context.new_page()
                check_page.set_default_timeout(10_000)
                attach_observers(check_page, result, base_netloc, expected_console_errors=True)
                check_id = "ui-audit-load-failure"
                source = "def check(flow):\n    return []\n"
                save_requests: List[str] = []
                load_requests = {"count": 0}
                created = False

                def observe_requests(request: Any) -> None:
                    if request.url.endswith("/api/checks/" + check_id) and request.method == "PUT":
                        save_requests.append(request.url)

                check_page.on("request", observe_requests)

                def reject_check_load(route: Any) -> None:
                    if route.request.method == "GET":
                        load_requests["count"] += 1
                        route.fulfill(status=503, content_type="application/json", body='{"error":"check source unavailable"}')
                    else:
                        route.continue_()

                try:
                    check_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(check_page)
                    check_page.locator('.tab[data-tab="scanner"]').click()
                    check_page.locator("#checksBtn").click()
                    check_page.wait_for_selector("#checksModal", state="visible", timeout=10_000)
                    check_page.locator("#checkNew").click()
                    check_page.locator("#checkId").fill(check_id)
                    check_page.locator("#checkSrc").fill(source)
                    with check_page.expect_response(
                        lambda response: response.url.endswith("/api/checks/" + check_id)
                        and response.request.method == "PUT",
                        timeout=10_000,
                    ) as check_saved:
                        check_page.locator("#checkSave").click()
                    result.require(check_saved.value.ok, f"custom-check create returned {check_saved.value.status}")
                    # Record the server acknowledgement before waiting on any
                    # UI rendering so a later assertion cannot leak the row.
                    created = True
                    check_page.wait_for_function(
                        "document.querySelector('#checkOut')?.textContent.includes('Saved')",
                        timeout=10_000,
                    )
                    row = check_page.locator(f'#checksList .checks-custom[data-id="{check_id}"]')
                    check_page.wait_for_selector(f'#checksList .checks-custom[data-id="{check_id}"]', timeout=10_000)
                    row.locator(".checks-edit-target").click()
                    check_page.wait_for_function("value=>document.querySelector('#checkSrc')?.value===value", arg=source)
                    check_page.route(f"**/api/checks/{check_id}", reject_check_load)
                    row.locator(".checks-edit-target").click()
                    check_page.wait_for_selector("#checkOut [data-check-retry]", timeout=10_000)
                    result.require(check_page.locator("#checkSrc").input_value() == "", "failed custom-check load retained stale source")
                    result.require(
                        check_page.evaluate(
                            """() => ['checkTest','checkSave','checkDelete'].every(id=>document.querySelector('#'+id)?.disabled===true)"""
                        ),
                        "custom-check actions remained enabled after a failed load",
                    )
                    result.require(check_page.locator("#checkOut [data-check-retry]").is_visible(), "custom-check load failure did not expose Retry")
                    result.require(
                        check_page.locator("#checkOut").get_attribute("role") == "alert"
                        and check_page.locator("#checkOut").get_attribute("aria-live") == "assertive",
                        "custom-check load failure was not announced",
                    )
                    retry = check_page.locator("#checkOut [data-check-retry]")
                    retry.focus()
                    retry.press("Enter")
                    retry_deadline = time.monotonic() + 2.0
                    while load_requests["count"] < 2 and time.monotonic() < retry_deadline:
                        check_page.wait_for_timeout(10)
                    result.require(load_requests["count"] >= 2, "custom-check Retry did not issue another GET")
                    check_page.wait_for_function(
                        "document.activeElement?.matches('#checkOut [data-check-retry]')",
                        timeout=10_000,
                    )
                    result.require(
                        check_page.locator("#checkOut [data-check-retry]").evaluate("el => document.activeElement === el"),
                        "keyboard Retry did not restore focus after a repeated custom-check load failure",
                    )
                    check_page.wait_for_timeout(120)
                    result.require(len(save_requests) == 1, "failed custom-check load issued an unexpected save")
                finally:
                    try:
                        check_page.unroute(f"**/api/checks/{check_id}", reject_check_load)
                    except Exception:
                        pass
                    if created:
                        try:
                            cleanup_status = check_page.evaluate(
                                """async id => {
                                  const response = await fetch('/api/checks/'+encodeURIComponent(id), {method:'DELETE'});
                                  return response.status;
                                }""",
                                check_id,
                            )
                            # A 404 means the UI already removed it while
                            # recovering; all other non-success statuses are
                            # real isolation failures and must be visible.
                            if cleanup_status not in (200, 204, 404):
                                raise RuntimeError(f"DELETE returned {cleanup_status}")
                        except Exception as exc:
                            result.failures.append(
                                f"custom-check audit cleanup failed: {type(exc).__name__}: {exc}"
                            )
                    check_context.close()

            result.run("custom Scanner check load failure clears stale source", custom_check_load_failure)

            def allowlist_initial_failure() -> None:
                """Initial allowlist failure must remain actionable and recover."""
                allow_context = browser.new_context(viewport={"width": 1024, "height": 768})
                allow_page = allow_context.new_page()
                allow_page.set_default_timeout(10_000)
                attach_observers(allow_page, result, base_netloc, expected_console_errors=True)
                calls = {"count": 0}

                def fail_once(route: Any) -> None:
                    if route.request.method != "GET":
                        route.continue_()
                        return
                    calls["count"] += 1
                    if calls["count"] == 1:
                        route.fulfill(status=503, content_type="application/json", body='{"error":"allowlist unavailable"}')
                    else:
                        route.fulfill(status=200, content_type="application/json", body='{"entries":[],"clientIP":"127.0.0.1"}')

                allow_page.route("**/api/allowlist", fail_once)
                try:
                    allow_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(allow_page)
                    allow_page.locator('.tab[data-tab="settings"]').click()
                    allow_page.locator('#setNav button[data-sec="api"]').click()
                    allow_page.locator('#apiSub button[data-s="allowlist"]').click()
                    allow_page.wait_for_selector("#allowListLoadState", state="visible", timeout=10_000)
                    allow_page.wait_for_function(
                        "document.querySelector('#allowListLoadState')?.textContent.includes('Retry')",
                        timeout=10_000,
                    )
                    result.require(allow_page.locator("#allowListLoadState").get_attribute("role") == "status", "allowlist failure state is not announced")
                    result.require("Retry" in allow_page.locator("#allowListLoadState").inner_text(), "initial allowlist failure did not expose Retry")
                    allow_page.locator("#allowListLoadState [data-load-retry]").click()
                    allow_page.wait_for_function(
                        """() => {
                          const el=document.querySelector('#allowListLoadState');
                          return !!el && (getComputedStyle(el).display==='none' || !el.textContent.trim());
                        }""",
                        timeout=10_000,
                    )
                    result.require(calls["count"] >= 2, "allowlist Retry did not issue a second GET")

                    # A failed mutation must replace an invalidated, still-held
                    # initial GET with an authoritative reload and persistent
                    # recovery feedback instead of leaving "Loading…" stuck.
                    allow_page.unroute("**/api/allowlist", fail_once)
                    held_gets: List[Any] = []
                    mutation_gets = {"count": 0}

                    def fail_add_during_load(route: Any) -> None:
                        if route.request.method == "POST":
                            route.fulfill(status=409, content_type="application/json", body='{"error":"allowlist audit rejected"}')
                            return
                        if route.request.method == "GET":
                            mutation_gets["count"] += 1
                            if mutation_gets["count"] == 1:
                                held_gets.append(route)
                            else:
                                route.fulfill(status=200, content_type="application/json", body='{"entries":[],"clientIP":"127.0.0.1"}')
                            return
                        route.continue_()

                    allow_page.route("**/api/allowlist", fail_add_during_load)
                    try:
                        allow_page.locator('#apiSub button[data-s="allowlist"]').click()
                        held_deadline = time.monotonic() + 2.0
                        while not held_gets and time.monotonic() < held_deadline:
                            allow_page.wait_for_timeout(10)
                        result.require(held_gets, "allowlist concurrency fixture did not hold the initial GET")
                        allow_page.locator("#allowCIDR").fill("127.0.0.253/32")
                        allow_page.locator("#allowLabel").fill("ui-audit-rejected")
                        allow_page.locator("#allowAdd").click()
                        allow_page.wait_for_function(
                            "document.querySelector('#allowListLoadState')?.textContent.includes('Allowlist update failed')",
                            timeout=10_000,
                        )
                        result.require(
                            "Refresh list" in allow_page.locator("#allowListLoadState").inner_text(),
                            "failed allowlist mutation did not expose safe persistent recovery",
                        )
                        result.require(allow_page.locator("#allowAdd").is_enabled(), "allowlist controls stayed locked after mutation failure")
                        held_gets[0].fulfill(status=200, content_type="application/json", body='{"entries":[],"clientIP":"127.0.0.1"}')
                        allow_page.wait_for_timeout(80)
                        result.require(
                            "Allowlist update failed" in allow_page.locator("#allowListLoadState").inner_text(),
                            "stale held allowlist GET erased the mutation failure",
                        )
                        allow_page.locator("#allowListLoadState [data-load-retry]").click()
                        allow_page.wait_for_function(
                            "getComputedStyle(document.querySelector('#allowListLoadState')).display==='none'",
                            timeout=10_000,
                        )
                    finally:
                        for pending in held_gets:
                            try:
                                pending.fulfill(status=200, content_type="application/json", body='{"entries":[],"clientIP":"127.0.0.1"}')
                            except Exception:
                                pass
                        allow_page.unroute("**/api/allowlist", fail_add_during_load)
                finally:
                    try:
                        allow_page.unroute("**/api/allowlist", fail_once)
                    except Exception:
                        pass
                    allow_context.close()

            result.run("initial allowlist failure exposes persistent Retry", allowlist_initial_failure)

            def seed_flow() -> None:
                status, _ = proxy_request(proxy, fixture_base + "/audit/seed?host=example")
                result.require(status == 200, f"seed request returned {status}")
                page.locator('.tab[data-tab="proxy"]').click()
                page.wait_for_selector("#rows .trow", timeout=10_000)

            result.run("generic loopback capture seed", seed_flow)

            def repeater_history_contract() -> None:
                def wait_stage(label: str, expression: str, **kwargs: Any) -> None:
                    try:
                        page.wait_for_function(expression, **kwargs)
                    except Exception as exc:
                        state = page.evaluate("""() => ({tabs:document.querySelectorAll('#repTabs .rep-tab').length, addDisabled:document.querySelector('#repTabsAdd')?.disabled, active:document.querySelector('#repTabs .rep-tab.on')?.dataset.tid, history:document.querySelectorAll('#repHistory .h').length})""")
                        raise AssertionError(f"Repeater stage {label} did not settle ({state}): {exc}") from exc

                # The source server may retain the project-scoped editor from
                # an earlier audit run. Close every existing tab first; the
                # manager replaces the final close with one fresh blank tab,
                # and its close hook removes the corresponding history rows.
                page.locator('.tab[data-tab="repeater"]').click()
                while page.locator("#repTabs .rep-tab").count() > 1:
                    before_tabs = page.locator("#repTabs .rep-tab").count()
                    page.locator("#repTabs .rep-tab").first.locator(".rt-close").click()
                    page.wait_for_function("expected => document.querySelectorAll('#repTabs .rep-tab').length===expected", arg=before_tabs - 1)
                page.locator("#repTabs .rep-tab").first.locator(".rt-close").click()
                page.wait_for_function("document.querySelectorAll('#repTabs .rep-tab').length===1")
                page.wait_for_function(
                    """async () => await new Promise(resolve=>{
                      const request=indexedDB.open('interseptor-repeater-history',1);
                      request.onupgradeneeded=()=>{
                        const db=request.result;
                        const store=db.objectStoreNames.contains('entries')
                          ? request.transaction.objectStore('entries')
                          : db.createObjectStore('entries',{keyPath:'key'});
                        if(!store.indexNames.contains('tabKey'))store.createIndex('tabKey','tabKey',{unique:false});
                      };
                      request.onsuccess=()=>{
                        const count=request.result.transaction('entries','readonly').objectStore('entries').count();
                        count.onsuccess=()=>resolve(count.result===0);
                      };
                      request.onerror=()=>resolve(false);
                    })""",
                    timeout=10_000,
                )
                page.locator('.tab[data-tab="proxy"]').click()
                row = page.locator("#rows .trow").first
                row.click()
                page.wait_for_selector("#inspectSendRepeater:not([disabled])")
                page.locator("#inspectSendRepeater").click()
                page.wait_for_selector('.tab[data-tab="repeater"].active')
                page.locator("#repSend").click()
                wait_stage("first send", "document.querySelector('#repSend')?.dataset.state!=='pending'")
                result.require(page.locator("#repSend").get_attribute("data-state") == "success", "Repeater did not reach success")
                page.locator("#repHistToggle").click()
                page.wait_for_selector("#repHistory .h")
                history_count = page.locator("#repHistory .h").count()
                result.require(history_count == 1, f"expected one tab-owned send, found {history_count}")
                result.require(page.locator("#repHistory .h").first.get_attribute("aria-current") == "true", "Repeater history selection is not exposed")
                tab_a = page.locator("#repTabs .rep-tab.on").get_attribute("data-tid")
                result.require(bool(tab_a), "Repeater active tab has no stable identity")

                changed_url = fixture_base + "/audit/repeater-edited"
                page.locator("#repMethod").select_option("POST")
                page.locator("#repUrl").fill(changed_url)
                page.locator("#repHeaders").fill("Content-Type: application/json\nX-Audit: stable")
                page.locator("#repBody").fill('{"changed":true}')
                result.require(page.locator("#repHistory .h").count() == history_count, "request edits cleared Repeater history")
                page.locator('.tab[data-tab="scanner"]').click()
                page.locator('.tab[data-tab="repeater"]').click()
                result.require(page.locator("#repHistory .h").count() == history_count, "top-level navigation cleared Repeater history")
                page.wait_for_timeout(900)
                page.reload(wait_until="domcontentloaded")
                wait_ready(page)
                result.require(page.locator('.tab[data-tab="repeater"]').get_attribute("aria-selected") == "true", "Repeater tab was not restored")
                result.require(page.locator("#repUrl").input_value() == changed_url, "edited Repeater URL was not restored")
                if page.locator("#repHistory").evaluate("el=>getComputedStyle(el).display==='none'"):
                    page.locator("#repHistToggle").click()
                page.wait_for_selector("#repHistory .h")
                result.require(page.locator("#repHistory .h").count() == history_count, "reload cleared tab-owned Repeater history")
                result.require(indexed_history_count(page) == history_count, "durable Repeater history count diverged")

                # A second tab is an important boundary: history is keyed by
                # tab identity, not by whichever request happens to be visible
                # in the shared editor. Close A only after B has its own send.
                tabs_before_add = page.locator("#repTabs .rep-tab").count()
                page.locator("#repTabsAdd").click()
                wait_stage(
                    "second tab creation",
                    "expected=>document.querySelectorAll('#repTabs .rep-tab').length===expected",
                    arg=tabs_before_add + 1,
                )
                tab_b = page.locator("#repTabs .rep-tab.on").get_attribute("data-tid")
                result.require(tab_b and tab_b != tab_a, "new Repeater tab did not receive a distinct identity")
                page.locator("#repUrl").fill(fixture_base + "/audit/repeater-tab-b")
                page.locator("#repMethod").select_option("GET")
                page.locator("#repHeaders").fill("")
                page.locator("#repBody").fill("")
                page.locator("#repSend").click()
                wait_stage("second send", "document.querySelector('#repSend')?.dataset.state!=='pending'", timeout=10_000)
                result.require(page.locator("#repSend").get_attribute("data-state") == "success", "second Repeater tab did not reach success")
                if page.locator("#repHistory").evaluate("el=>getComputedStyle(el).display==='none'"):
                    page.locator("#repHistToggle").click()
                page.wait_for_selector("#repHistory .h")
                result.require(page.locator("#repHistory .h").count() == 1, "second Repeater tab inherited the first tab's history")
                result.require(indexed_history_count(page) == history_count + 1, "durable Repeater histories were not isolated")

                page.locator(f'#repTabs .rep-tab[data-tid="{tab_a}"] .rt-select').click()
                wait_stage("switch back to first tab", "id=>document.querySelector('#repTabs .rep-tab.on')?.dataset.tid===id", arg=tab_a)
                if page.locator("#repHistory").evaluate("el=>getComputedStyle(el).display==='none'"):
                    page.locator("#repHistToggle").click()
                page.wait_for_selector("#repHistory .h")
                result.require(page.locator("#repHistory .h").count() == history_count, "switching tabs changed tab A history")
                page.locator(f'#repTabs .rep-tab[data-tid="{tab_a}"] .rt-close').click()
                wait_stage("close first tab", "id=>document.querySelector('#repTabs .rep-tab.on')?.dataset.tid===id", arg=tab_b)
                wait_stage(
                    "first-tab history cleanup",
                    """async expected => await new Promise(resolve=>{
                      const request=indexedDB.open('interseptor-repeater-history',1);
                      request.onupgradeneeded=()=>{
                        const db=request.result;
                        const store=db.objectStoreNames.contains('entries')
                          ? request.transaction.objectStore('entries')
                          : db.createObjectStore('entries',{keyPath:'key'});
                        if(!store.indexNames.contains('tabKey'))store.createIndex('tabKey','tabKey',{unique:false});
                      };
                      request.onsuccess=()=>{
                        const count=request.result.transaction('entries','readonly').objectStore('entries').count();
                        count.onsuccess=()=>resolve(count.result===expected);
                      };
                      request.onerror=()=>resolve(false);
                    })""",
                    arg=history_count,
                )
                if page.locator("#repHistory").evaluate("el=>getComputedStyle(el).display==='none'"):
                    page.locator("#repHistToggle").click()
                page.wait_for_selector("#repHistory .h")
                result.require(page.locator("#repHistory .h").count() == 1, "closing tab A removed tab B history")

                page.evaluate(
                    """() => {
                      window.__uiAuditStorageGetItem=Storage.prototype.getItem;
                      Storage.prototype.getItem=function(key){
                        if(String(key).includes('rep.history.cleanup')){
                          throw new DOMException('audit cleanup ledger unavailable','SecurityError');
                        }
                        return window.__uiAuditStorageGetItem.call(this,key);
                      };
                    }"""
                )
                try:
                    page.locator("#repTabs .rep-tab.on .rt-close").click()
                    wait_stage("second-tab history cleanup without localStorage ledger",
                        """async () => await new Promise(resolve=>{
                          const request=indexedDB.open('interseptor-repeater-history',1);
                          request.onupgradeneeded=()=>{
                            const db=request.result;
                            const store=db.objectStoreNames.contains('entries')
                              ? request.transaction.objectStore('entries')
                              : db.createObjectStore('entries',{keyPath:'key'});
                            if(!store.indexNames.contains('tabKey'))store.createIndex('tabKey','tabKey',{unique:false});
                          };
                          request.onsuccess=()=>{
                            const count=request.result.transaction('entries','readonly').objectStore('entries').count();
                            count.onsuccess=()=>resolve(count.result===0);
                          };
                          request.onerror=()=>resolve(false);
                        })""",
                        timeout=10_000,
                    )
                finally:
                    page.evaluate(
                        """() => {
                          if(window.__uiAuditStorageGetItem){
                            Storage.prototype.getItem=window.__uiAuditStorageGetItem;
                            delete window.__uiAuditStorageGetItem;
                          }
                        }"""
                    )

            result.run("tab-owned Repeater history across every request edit", repeater_history_contract)

            def notes_and_findings() -> None:
                page.locator('.tab[data-tab="notes"]').click()
                page.wait_for_function("getComputedStyle(document.querySelector('#notesLoadState')).display==='none'", timeout=10_000)
                current_notes = page.locator("#notesEdit").input_value()
                marker = "A" if not current_notes.endswith("pass A.") else "B"
                notes = f"# UI audit\n\nGeneric loopback verification pass {marker}."
                with page.expect_response(
                    lambda response: response.url.endswith("/api/notes")
                    and response.request.method == "PUT",
                    timeout=10_000,
                ) as saved:
                    page.locator("#notesEdit").fill(notes)
                result.require(saved.value.ok, f"Notes save returned {saved.value.status}")
                page.locator('#notesSeg button[data-m="preview"]').click()
                result.require(page.locator("#notesPreview").is_visible(), "Notes preview did not open")
                page.locator('#notesSeg button[data-m="edit"]').click()

                page.locator('.tab[data-tab="findings"]').click()
                try:
                    page.locator("#findNew").click()
                    page.wait_for_function("document.activeElement?.id==='fcTitle'")
                    page.locator("#fcTitle").fill("Generic UI audit finding")
                    page.locator("#fcSeverity").select_option("Info")
                    page.locator("#fcSave").click()
                    page.wait_for_selector("#findCreateModal", state="hidden", timeout=10_000)
                finally:
                    if page.locator("#findCreateModal").is_visible():
                        page.locator("#fcClose").click()
                page.wait_for_selector("#findList .find-row", timeout=10_000)
                current = page.locator("#findList .find-row[aria-current='true']")
                result.require(current.count() == 1, "Finding selection does not expose one current row")
                result.require("Generic UI audit finding" in page.locator("#findDetail").inner_text(), "created finding did not open")
                guide = page.locator("#findGuide")
                guide.click()
                page.keyboard.press("Escape")
                result.require(page.evaluate("document.activeElement?.id") == "findGuide", "Finding guide did not restore focus")

            result.run("Notes save/preview and Findings creation/focus", notes_and_findings)

            def scanner_run() -> None:
                page.locator('.tab[data-tab="scanner"]').click()
                page.locator("#scanRun").click()
                page.wait_for_function("document.querySelector('#scanRun')?.dataset.state!=='pending'", timeout=20_000)
                result.require(page.locator("#scanRun").get_attribute("data-state") == "success", "Scanner did not reach success")
                result.require(page.locator("#scanRun").get_attribute("aria-busy") == "false", "Scanner remained busy")

            result.run("Scanner real pending-to-success lifecycle", scanner_run)

            def scanner_empty_results_restore_focus() -> None:
                """Removing the focused issue must leave focus on a stable action."""
                scan_context = browser.new_context(viewport={"width": 1024, "height": 768})
                scan_page = scan_context.new_page()
                scan_page.set_default_timeout(10_000)
                attach_observers(scan_page, result, base_netloc)
                empty_results = {"value": False}

                def serve_scanner_issues(route: Any) -> None:
                    if route.request.method != "GET":
                        route.continue_()
                        return
                    issues = [] if empty_results["value"] else [{
                        "id": 9001,
                        "flowId": 0,
                        "severity": "Info",
                        "title": "UI audit focus issue",
                        "target": "https://example.com/",
                        "detail": "Generic accessibility fixture.",
                        "evidence": "",
                        "fix": "",
                    }]
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body=json.dumps({"issues": issues}),
                    )

                scan_page.route("**/api/scanner/issues*", serve_scanner_issues)
                try:
                    scan_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(scan_page)
                    scan_page.locator('.tab[data-tab="scanner"]').click()
                    scan_page.wait_for_selector("#scanList .scan-item", timeout=10_000)
                    issue = scan_page.locator("#scanList .scan-item").first
                    issue.focus()
                    result.require(
                        issue.evaluate("el => document.activeElement === el"),
                        "scanner issue fixture could not receive keyboard focus",
                    )
                    empty_results["value"] = True
                    scan_page.evaluate("() => import('./js/scanner.js').then(module => module.loadIssues())")
                    scan_page.wait_for_selector("#scanList .state-empty", timeout=10_000)
                    scan_page.wait_for_function("document.activeElement?.id==='scanRun'", timeout=10_000)
                    result.require(
                        scan_page.locator("#scanRun").evaluate("el => document.activeElement === el"),
                        "scanner did not restore focus when its focused issue disappeared",
                    )
                finally:
                    try:
                        scan_page.unroute("**/api/scanner/issues*", serve_scanner_issues)
                    except Exception:
                        pass
                    scan_context.close()

            result.run("Scanner empty results restore focused issue context", scanner_empty_results_restore_focus)

            def auxiliary_reversible_surfaces() -> None:
                # Exercise auxiliary paths and validators with only reversible
                # mutations in the isolated project. Never enable system proxy,
                # contact peers, or write real target data.
                page.locator('.tab[data-tab="proxy"]').click()
                page.locator("#flowSearchScripts summary").click()
                page.wait_for_selector("#flowSearchScriptEditor")
                page.locator("#flowSearchScriptName").fill("ui-audit")
                page.locator("#flowSearchScriptEditor").fill("def match(flow): return True")
                page.locator("#flowSearchScriptTest").click()
                page.wait_for_function("document.querySelector('#flowSearchScriptStatus')?.textContent.trim()!=='' || document.querySelector('#flowSearchScriptError')?.textContent.trim()!==''", timeout=10_000)
                result.require(page.locator("#flowSearchScriptStatus").text_content().strip() or page.locator("#flowSearchScriptError").text_content().strip(), "saved flow-search validator did not report a result")

                page.locator('.tab[data-tab="scanner"]').click()
                for button_id, modal_id, close_id, docs_id in (("checksBtn", "checksModal", "checksClose", "checkModeDocs"), ("codecsBtn", "codecsModal", "codecsClose", "codecModeDocs")):
                    page.locator(f"#{button_id}").click()
                    page.wait_for_selector(f"#{modal_id}", state="visible", timeout=10_000)
                    page.locator(f"#{docs_id}").click()
                    page.wait_for_timeout(120)
                    page.locator(f"#{close_id}").click()
                    page.wait_for_selector(f"#{modal_id}", state="hidden", timeout=10_000)
                page.locator('.tab[data-tab="settings"]').click()
                page.locator('#setNav button[data-sec="scanner"]').click()
                page.wait_for_selector('.set-sec[data-sec="scanner"]', state="visible", timeout=10_000)
                page.wait_for_selector("#settingsLoadState", state="hidden", timeout=10_000)
                oob_enabled = page.locator("#setOobEnabled").is_checked()
                restore_oob_disabled = not oob_enabled
                if not oob_enabled:
                    result.require(
                        page.locator("#oobDisabledHint").is_visible(),
                        "Settings does not explain that OOB is disabled",
                    )
                    with page.expect_response(
                        lambda response: response.url.endswith("/api/settings")
                        and response.request.method == "PUT",
                        timeout=10_000,
                    ) as enabled_response:
                        page.locator("#setOobEnabled").check()
                    result.require(enabled_response.value.ok, f"OOB enable returned {enabled_response.value.status}")
                    page.wait_for_function("!document.documentElement.classList.contains('oob-disabled')")
                    oob_enabled = True
                try:
                    page.locator('.tab[data-tab="scanner"]').click()
                    oob_button = page.locator("#oobBtn")
                    result.require(oob_button.is_visible(), "enabled OOB action is hidden")
                    if oob_button.is_enabled():
                        oob_button.click()
                        page.wait_for_selector("#oobModal", state="visible", timeout=10_000)
                        draft = "http://127.0.0.1:9/audit-draft"
                        page.locator("#oobBase").fill(draft)
                        page.locator("#oobBase").blur()
                        page.locator("#oobClear").click()
                        page.wait_for_selector("#confirmModal", state="visible", timeout=10_000)
                        with page.expect_response(
                            lambda response: response.url.endswith("/api/oob/interactions")
                            and response.request.method == "DELETE",
                            timeout=10_000,
                        ) as cleared_response:
                            page.locator("#confirmOk").click()
                        result.require(cleared_response.value.ok, f"OOB clear returned {cleared_response.value.status}")
                        page.wait_for_function("document.querySelector('#oobClear')?.getAttribute('aria-busy')==='false'", timeout=10_000)
                        result.require(page.locator("#oobBase").input_value() == draft, "OOB interaction refresh overwrote a blurred base-URL draft")
                        page.locator("#oobClose").click()
                        page.wait_for_selector("#oobModal", state="hidden", timeout=10_000)
                    else:
                        result.require(
                            oob_button.is_disabled(),
                            "unavailable OOB action is not exposed as disabled",
                        )
                finally:
                    if page.locator("#oobModal").is_visible():
                        page.locator("#oobClose").click()
                    if restore_oob_disabled:
                        page.locator('.tab[data-tab="settings"]').click()
                        page.locator('#setNav button[data-sec="scanner"]').click()
                        if page.locator("#setOobEnabled").is_checked():
                            with page.expect_response(
                                lambda response: response.url.endswith("/api/settings")
                                and response.request.method == "PUT",
                                timeout=10_000,
                            ) as disabled_response:
                                page.locator("#setOobEnabled").uncheck()
                            result.require(disabled_response.value.ok, f"OOB disable returned {disabled_response.value.status}")
                            page.wait_for_function("document.documentElement.classList.contains('oob-disabled')")

                page.locator("#cmdkBtn").click()
                page.wait_for_selector("#cmdkInput", state="visible")
                page.locator("#cmdkInput").fill("Open Decoder")
                page.wait_for_timeout(120)
                page.locator("#cmdkInput").press("Enter")
                page.wait_for_selector("#decModal", state="visible", timeout=10_000)
                page.locator("#decIn").fill("aGVsbG8=")
                page.locator("#decOps button").first.click()
                page.wait_for_timeout(180)
                result.require(page.locator("#decOut").is_visible(), "Decoder output surface is not reachable")
                page.locator("#decClose").click()
                page.wait_for_selector("#decModal", state="hidden", timeout=10_000)

                page.locator('.tab[data-tab="settings"]').click()
                for section in ("proxy", "tls", "devices", "session", "project", "scope", "scanner", "api"):
                    page.locator(f'#setNav button[data-sec="{section}"]').click()
                    page.wait_for_selector(f'.set-sec[data-sec="{section}"]', state="visible", timeout=10_000)
                page.locator('#setNav button[data-sec="api"]').click()
                vault_remote_seen = {"value": False}

                vault_put_routes: List[Any] = []

                def mock_unconfigured_vault(route: Any) -> None:
                    if route.request.method == "GET":
                        route.fulfill(status=200, content_type="application/json", body='{"url":"","hasKey":false}')
                    else:
                        vault_put_routes.append(route)

                def observe_vault_remote(route: Any) -> None:
                    vault_remote_seen["value"] = True
                    route.fulfill(status=200, content_type="application/json", body='{"projects":[]}')

                page.route("**/api/vault/config", mock_unconfigured_vault)
                page.route("**/api/vault/remote", observe_vault_remote)
                try:
                    for subpane in ("keys", "allowlist", "share", "rest", "mcp"):
                        page.locator(f'#apiSub button[data-s="{subpane}"]').click()
                        page.wait_for_selector(f"#api{ {'keys':'Keys','allowlist':'Allowlist','share':'Share','rest':'Rest','mcp':'Mcp'}[subpane] }", state="visible", timeout=10_000)
                        if subpane == "share":
                            share_console_errors = len(result.console_errors)
                            page.wait_for_timeout(250)
                            result.require(not vault_remote_seen["value"], "unconfigured Share pane probed the remote vault")
                            result.require(
                                len(result.console_errors) == share_console_errors,
                                "unconfigured Share pane produced a console error",
                            )
                            page.locator("#vaultUrl").fill("http://127.0.0.1:9")
                            page.locator("#vaultKey").fill("audit-token-a")
                            page.locator("#vaultSaveCfg").click()
                            page.wait_for_function("document.querySelector('#vaultSaveCfg')?.disabled===true", timeout=2_000)
                            route_deadline = time.monotonic() + 2.0
                            while not vault_put_routes and time.monotonic() < route_deadline:
                                page.wait_for_timeout(10)
                            result.require(vault_put_routes, "vault Save did not issue its PUT request")
                            page.locator("#vaultKey").fill("audit-token-b")
                            vault_put_routes[0].fulfill(status=200, content_type="application/json", body="{}")
                            page.wait_for_function("document.querySelector('#vaultSaveCfg')?.disabled===false", timeout=10_000)
                            result.require(page.locator("#vaultKey").input_value() == "audit-token-b", "vault save acknowledgement overwrote a newer token draft")
                finally:
                    for pending_route in vault_put_routes:
                        try:
                            pending_route.fulfill(status=200, content_type="application/json", body="{}")
                        except Exception:
                            pass
                    page.unroute("**/api/vault/config", mock_unconfigured_vault)
                    page.unroute("**/api/vault/remote", observe_vault_remote)
                page.locator('#setNav button[data-sec="tls"]').click()
                page.wait_for_selector("#tlsDiagPanel", state="visible", timeout=10_000)
                page.locator('#setNav button[data-sec="devices"]').click()
                page.wait_for_selector("#androidDeviceValue", state="visible", timeout=10_000)
                for refresh_id in ("androidRefreshBtn", "iosRefreshBtn"):
                    refresh = page.locator(f"#{refresh_id}")
                    refresh.scroll_into_view_if_needed()
                    result.require(refresh.is_visible() and refresh.is_enabled(), f"{refresh_id} is not reachable")
                    refresh.click()
                    page.wait_for_timeout(180)
                page.locator('#setNav button[data-sec="session"]').click()
                page.wait_for_selector("#sessionLoadState", state="attached", timeout=10_000)
                login_test = page.locator("#loginMacroTest")
                if login_test.is_visible() and login_test.is_enabled():
                    # Test uses the saved macro and never persists the draft.
                    # The isolated project starts with no target, so this
                    # cannot contact a real service or alter session state.
                    login_test.click()
                    page.wait_for_function(
                        "document.querySelector('#loginMacroTestOut')?.textContent.trim()!=='' || document.querySelector('#loginMacroState')?.textContent.trim()!==''",
                        timeout=10_000,
                    )
                page.locator('#setNav button[data-sec="project"]').click()
                page.wait_for_selector("#projectLoadState", state="attached", timeout=10_000)
                project_badge = page.locator("#projBadge")
                if project_badge.is_visible():
                    project_badge.click()
                    page.wait_for_selector("#projModal", state="visible", timeout=10_000)
                    page.locator("#pmClose").click()
                    page.wait_for_selector("#projModal", state="hidden", timeout=10_000)

                page.locator('#setNav button[data-sec="api"]').click()
                page.locator('#apiSub button[data-s="keys"]').click()
                page.locator("#keyLabel").fill("ui-audit")
                page.locator("#keyScope").select_option("read")
                with page.expect_response(
                    lambda response: response.url.endswith("/api/keys") and response.request.method == "POST",
                    timeout=10_000,
                ) as key_created:
                    page.locator("#keyCreate").click()
                result.require(key_created.value.ok, f"API key create returned {key_created.value.status}")
                page.wait_for_selector("#keyList tr", timeout=10_000)
                key_row = page.locator("#keyList tr").filter(has_text="ui-audit").first
                result.require(key_row.count() == 1, "created audit API key was not listed")
                key_row.locator("[data-revoke]").click()
                page.wait_for_selector("#confirmModal", state="visible", timeout=10_000)
                page.locator("#confirmOk").click()
                page.wait_for_function("!document.querySelector('#keyList tr')?.textContent.includes('ui-audit')", timeout=10_000)

                page.locator('#apiSub button[data-s="allowlist"]').click()
                page.locator("#allowCIDR").fill("127.0.0.254/32")
                page.locator("#allowLabel").fill("ui-audit")
                page.locator("#allowAdd").click()
                page.wait_for_function(
                    """() => [...document.querySelectorAll('#allowList tr')]
                      .some(row=>row.textContent.includes('127.0.0.254/32'))""",
                    timeout=10_000,
                )
                allow_row = page.locator("#allowList tr").filter(has_text="127.0.0.254/32").first
                result.require(allow_row.count() == 1, "audit allowlist entry was not listed")
                result.require(
                    allow_row.locator("[data-allow-del]").get_attribute("aria-label")
                    == "Remove allowlist entry 127.0.0.254/32",
                    "allowlist Remove action did not identify its entry",
                )
                allow_row.locator("[data-allow-del]").click()
                page.wait_for_selector("#confirmModal", state="visible", timeout=10_000)
                page.locator("#confirmOk").click()
                page.wait_for_function("!document.querySelector('#allowList tr')?.textContent.includes('127.0.0.254/32')", timeout=10_000)
                result.require(page.locator("#sysProxyToggle").get_attribute("aria-pressed") == "false", "read-only audit changed the system proxy")

            result.run("auxiliary surfaces, reversible actions, and validators", auxiliary_reversible_surfaces)

            def rejected_scope_and_rule_mutations() -> None:
                # Create generic rows in the isolated project, then hold both
                # mutation acknowledgements and reject them. This exercises the
                # real DOM event path and proves that a newer draft is not
                # removed by an older rejected PUT when DELETE is queued.
                def wait_route(routes: List[Any], label: str) -> None:
                    deadline = time.monotonic() + 2.0
                    while not routes and time.monotonic() < deadline:
                        page.wait_for_timeout(10)
                    result.require(routes, f"{label} request was not issued")

                def wait_toast(text: str, label: str) -> None:
                    page.wait_for_function(
                        "needle => [...document.querySelectorAll('#toast .toast-item')].some(el=>el.textContent.includes(needle))",
                        arg=text,
                        timeout=10_000,
                    )
                    result.require(text in page.locator("#toast").inner_text(), f"{label} error feedback was not visible")
                    error_toast = page.locator("#toast .toast-item").filter(has_text=text).last
                    result.require("error" in (error_toast.get_attribute("class") or "").split(), f"{label} rejection was not announced as an error")

                case_http_start = len(result.http_errors)
                expected_rejection_items: List[str] = []
                settled_rejection_routes: set[int] = set()

                def absorb_expected_rejections() -> None:
                    # Only classify response records emitted by this case.
                    # Keep earlier identical records and all console errors;
                    # exact browser console wording is not a stable contract.
                    remaining = list(expected_rejection_items)
                    for index in range(len(result.http_errors) - 1, case_http_start - 1, -1):
                        item = result.http_errors[index]
                        if item in remaining:
                            remaining.remove(item)
                            result.expected_http_errors.append(item)
                            del result.http_errors[index]

                scope_put: List[Any] = []
                scope_delete: List[Any] = []

                def reject_scope(route: Any) -> None:
                    if route.request.method == "PUT":
                        scope_put.append(route)
                    elif route.request.method == "DELETE":
                        scope_delete.append(route)
                    else:
                        route.continue_()

                def fulfill_rejection(route: Any, label: str) -> None:
                    route_key = id(route)
                    if route_key in settled_rejection_routes:
                        return
                    item = f"409 {route.request.method} {route.request.url}"
                    # The corresponding Chromium network-console error is
                    # expected only for this exact injected request URL.
                    result.expected_console_request_urls.append(route.request.url)
                    route.fulfill(status=409, content_type="application/json", body=f'{{"error":"{label} audit rejected"}}')
                    settled_rejection_routes.add(route_key)
                    expected_rejection_items.append(item)

                def cleanup_scope() -> None:
                    if not scope_id:
                        return
                    try:
                        row = page.locator(f'#scopeBody tr[data-id="{scope_id}"]')
                        if row.count():
                            row.locator("[data-del]").click()
                            page.wait_for_function(
                                "id => !document.querySelector(`#scopeBody tr[data-id=\\\"${id}\\\"]`)",
                                arg=scope_id,
                                timeout=10_000,
                            )
                    except Exception as exc:
                        result.failures.append(
                            f"scope audit cleanup failed for {scope_id}: {type(exc).__name__}: {exc}"
                        )

                page.route("**/api/scope/*", reject_scope)
                scope_id = None
                scope_host = "ui-audit-scope.example.com"
                try:
                    page.locator('.tab[data-tab="settings"]').click()
                    page.locator('#setNav button[data-sec="scope"]').click()
                    page.wait_for_selector('.set-sec[data-sec="scope"]', state="visible", timeout=10_000)
                    result.require(
                        page.locator(f'#scopeBody input[data-k="host"][value="{scope_host}"]').count() == 0,
                        "isolated scope fixture already contained the audit marker",
                    )
                    page.locator("#newScopeAction").select_option("include")
                    page.locator("#newScopeHost").fill(scope_host)
                    page.locator("#newScopePath").fill("/audit-scope")
                    with page.expect_response(
                        lambda response: response.url.endswith("/api/scope") and response.request.method == "POST",
                        timeout=10_000,
                    ) as scope_created:
                        page.locator("#addScopeBtn").click()
                    scope_payload = scope_created.value.json()
                    scope_id = str(scope_payload.get("id") or 0)
                    result.require(scope_id != "0", "scope create response did not return its authoritative id")
                    page.wait_for_selector(f'#scopeBody tr[data-id="{scope_id}"]', timeout=10_000)
                    scope_row = page.locator(f'#scopeBody tr[data-id="{scope_id}"]')
                    result.require(scope_row.count() == 1, "audit scope rule was not created")
                    result.require(scope_row.locator('[data-k="host"]').input_value() == scope_host, "scope create selected the wrong row")
                    result.require(
                        scope_row.locator("[data-del]").get_attribute("aria-label")
                        == f"Delete scope rule {scope_id}",
                        "scope Delete action did not identify its rule",
                    )
                    scope_row.locator('[data-k="host"]').fill("changed.example.com")
                    scope_row.locator('[data-k="host"]').blur()
                    wait_route(scope_put, "scope PUT")
                    page.locator(f'#scopeBody tr[data-id="{scope_id}"] [data-del]').click()
                    result.require(page.locator(f'#scopeBody tr[data-id="{scope_id}"]').count() == 1, "rejected scope DELETE removed the row before acknowledgement")
                    fulfill_rejection(scope_put[0], "scope")
                    wait_route(scope_delete, "queued scope DELETE")
                    fulfill_rejection(scope_delete[0], "scope")
                    wait_toast("scope audit rejected", "scope")
                    page.wait_for_function(
                        "id => document.querySelector(`#scopeBody tr[data-id=\\\"${id}\\\"] [data-k=\\\"host\\\"]`)?.value==='ui-audit-scope.example.com'",
                        arg=scope_id,
                        timeout=10_000,
                    )
                    result.require(page.locator(f'#scopeBody tr[data-id="{scope_id}"]').count() == 1, "rejected scope DELETE did not restore the authoritative row")
                    page.wait_for_function(
                        "id => document.activeElement === document.querySelector(`#scopeBody tr[data-id=\"${id}\"] [data-del]`)",
                        arg=scope_id,
                        timeout=10_000,
                    )
                    result.require(
                        page.locator(f'#scopeBody tr[data-id="{scope_id}"] [data-del]').evaluate("el => document.activeElement === el"),
                        "rejected scope DELETE did not restore focus to its Delete action",
                    )
                    absorb_expected_rejections()
                finally:
                    for pending in scope_put + scope_delete:
                        try:
                            if id(pending) not in settled_rejection_routes:
                                fulfill_rejection(pending, "scope")
                        except Exception:
                            result.failures.append("scope audit cleanup could not settle a held rejection route")
                    try:
                        page.unroute("**/api/scope/*", reject_scope)
                    except Exception as exc:
                        result.failures.append(f"scope audit route cleanup failed: {type(exc).__name__}: {exc}")
                    cleanup_scope()
                    absorb_expected_rejections()

                rule_put: List[Any] = []
                rule_delete: List[Any] = []

                def reject_rule(route: Any) -> None:
                    if route.request.method == "PUT":
                        rule_put.append(route)
                    elif route.request.method == "DELETE":
                        rule_delete.append(route)
                    else:
                        route.continue_()

                def cleanup_rule() -> None:
                    if not rule_id:
                        return
                    try:
                        row = page.locator(f'#rulesBody tr[data-id="{rule_id}"]')
                        if row.count():
                            row.locator("[data-del]").click()
                            page.wait_for_function(
                                "id => !document.querySelector(`#rulesBody tr[data-id=\\\"${id}\\\"]`)",
                                arg=rule_id,
                                timeout=10_000,
                            )
                    except Exception as exc:
                        result.failures.append(
                            f"rule audit cleanup failed for {rule_id}: {type(exc).__name__}: {exc}"
                        )

                page.route("**/api/rules/*", reject_rule)
                rule_id = None
                rule_match = "^X-UiAudit:"
                try:
                    page.locator('.tab[data-tab="intercept"]').click()
                    details = page.locator("details.icpt-mr")
                    if not details.get_attribute("open"):
                        details.locator("summary").click()
                    page.wait_for_selector("#rulesBody", state="visible", timeout=10_000)
                    page.locator("#newRuleType").select_option("req-header")
                    result.require(
                        page.locator('#rulesBody input[data-k="match"]').evaluate_all(
                            "(inputs, marker) => !inputs.some(input => input.value === marker)",
                            rule_match,
                        ),
                        "isolated Match & Replace fixture already contained the audit marker",
                    )
                    page.locator("#newRuleMatch").fill(rule_match)
                    page.locator("#newRuleReplace").fill("X-UiAudit: clean")
                    with page.expect_response(
                        lambda response: response.url.endswith("/api/rules") and response.request.method == "POST",
                        timeout=10_000,
                    ) as rule_created:
                        page.locator("#addRuleBtn").click()
                    rule_payload = rule_created.value.json()
                    rule_id = str(rule_payload.get("id") or 0)
                    result.require(rule_id != "0", "rule create response did not return its authoritative id")
                    page.wait_for_selector(f'#rulesBody tr[data-id="{rule_id}"]', timeout=10_000)
                    rule_row = page.locator(f'#rulesBody tr[data-id="{rule_id}"]')
                    result.require(rule_row.count() == 1, "audit Match & Replace rule was not created")
                    result.require(rule_row.locator('[data-k="match"]').input_value() == rule_match, "rule create selected the wrong row")
                    result.require(
                        rule_row.locator("[data-del]").get_attribute("aria-label")
                        == f"Delete interception rule {rule_id}",
                        "Match & Replace Delete action did not identify its rule",
                    )
                    rule_row.locator('[data-k="match"]').fill("^X-UiAudit-Changed:")
                    rule_row.locator('[data-k="match"]').blur()
                    wait_route(rule_put, "rule PUT")
                    page.locator(f'#rulesBody tr[data-id="{rule_id}"] [data-del]').click()
                    result.require(page.locator(f'#rulesBody tr[data-id="{rule_id}"]').count() == 1, "rejected rule DELETE removed the row before acknowledgement")
                    fulfill_rejection(rule_put[0], "rule")
                    wait_route(rule_delete, "queued rule DELETE")
                    fulfill_rejection(rule_delete[0], "rule")
                    wait_toast("rule audit rejected", "rule")
                    page.wait_for_function(
                        "id => document.querySelector(`#rulesBody tr[data-id=\\\"${id}\\\"] [data-k=\\\"match\\\"]`)?.value==='^X-UiAudit:'",
                        arg=rule_id,
                        timeout=10_000,
                    )
                    result.require(page.locator(f'#rulesBody tr[data-id="{rule_id}"]').count() == 1, "rejected rule DELETE did not restore the authoritative row")
                    page.wait_for_function(
                        "id => document.activeElement === document.querySelector(`#rulesBody tr[data-id=\"${id}\"] [data-del]`)",
                        arg=rule_id,
                        timeout=10_000,
                    )
                    result.require(
                        page.locator(f'#rulesBody tr[data-id="{rule_id}"] [data-del]').evaluate("el => document.activeElement === el"),
                        "rejected rule DELETE did not restore focus to its Delete action",
                    )
                    absorb_expected_rejections()
                finally:
                    for pending in rule_put + rule_delete:
                        try:
                            if id(pending) not in settled_rejection_routes:
                                fulfill_rejection(pending, "rule")
                        except Exception:
                            result.failures.append("rule audit cleanup could not settle a held rejection route")
                    try:
                        page.unroute("**/api/rules/*", reject_rule)
                    except Exception as exc:
                        result.failures.append(f"rule audit route cleanup failed: {type(exc).__name__}: {exc}")
                    cleanup_rule()
                    absorb_expected_rejections()

            result.run("rejected Scope and Match & Replace PUT/DELETE ownership", rejected_scope_and_rule_mutations)

            def relative_project_path_is_ui_only() -> None:
                page.locator('.tab[data-tab="settings"]').click()
                switch_requests: List[Any] = []

                def block_project_switch(route: Any) -> None:
                    switch_requests.append(route)
                    route.fulfill(status=400, content_type="application/json", body='{"error":"switch must not be attempted"}')

                page.route("**/api/project/switch", block_project_switch)
                try:
                    page.locator("#projBadge").click()
                    page.wait_for_selector("#projModal", state="visible", timeout=10_000)
                    page.locator("#pmNew").fill("ui-audit-relative")
                    page.locator("#pmNewPath").fill("relative/audit")
                    page.locator("#pmNewBtn").click()
                    page.wait_for_function(
                        "needle => [...document.querySelectorAll('#toast .toast-item')].some(el=>el.textContent.includes(needle))",
                        arg="absolute folder path",
                        timeout=10_000,
                    )
                    page.wait_for_function(
                        "document.querySelector('#pmSwitchNote')?.textContent.includes('absolute folder path')",
                        timeout=10_000,
                    )
                    result.require(page.locator("#pmNewPath").get_attribute("aria-invalid") == "true", "relative project path was not associated with its field")
                    result.require(page.locator("#pmSwitchNote").get_attribute("role") == "alert", "relative project path error was not persistently announced")
                    result.require(not switch_requests, "relative project path attempted a project switch request")
                finally:
                    page.unroute("**/api/project/switch", block_project_switch)
                    if page.locator("#projModal").is_visible():
                        page.locator("#pmClose").click()
                        page.wait_for_selector("#projModal", state="hidden", timeout=10_000)

            result.run("relative project path validation stays UI-only", relative_project_path_is_ui_only)

            def intruder_run() -> None:
                page.locator('.tab[data-tab="intruder"]').click()
                page.locator("#intrTarget").fill(fixture_base)
                page.locator("#intrTemplate").fill(
                    f"GET /audit/intruder HTTP/1.1\nHost: 127.0.0.1:{fixture.server_port}\nConnection: close\n\n"
                )
                page.locator('#intrType button[data-t="repeat"]').click()
                page.locator("#intrRepeat").fill("3")
                page.locator("#intrThreads").fill("2")
                page.locator("#intrStart").click()
                page.wait_for_function(
                    "document.querySelectorAll('#intrResults .intr-row').length===3 && document.querySelector('#intrStart')?.dataset.state!=='pending'",
                    timeout=20_000,
                )
                result.require(page.locator("#intrResults .intr-row").count() == 3, "Intruder did not render three real results")
                page.locator("#intrHistToggle").click()
                result.require(page.locator("#intrHistory .h[data-hid]").count() == 1, "Intruder run was not added to this tab's history")

            result.run("Intruder real run and per-tab history", intruder_run)

            def intercept_request_forward_delayed_ack() -> None:
                page.locator('.tab[data-tab="intercept"]').click()
                toggle = page.locator("#interceptToggle")
                if toggle.get_attribute("aria-pressed") != "true":
                    toggle.click()
                page.wait_for_function("document.querySelector('#interceptToggle')?.getAttribute('aria-pressed')==='true'")
                response: Dict[str, Any] = {}

                def held_request() -> None:
                    try:
                        response["status"], response["body"] = proxy_request(proxy, fixture_base + "/audit/intercept")
                    except Exception as exc:
                        response["error"] = str(exc)

                thread = threading.Thread(target=held_request, name="ui-audit-held", daemon=True)
                started = time.perf_counter()
                thread.start()
                held_routes: List[Any] = []
                request_id = None
                try:
                    page.wait_for_selector("#heldList .icpt-item", timeout=10_000)
                    held = page.locator("#heldList .icpt-item").first
                    result.require(held.get_attribute("aria-current") == "true", "held selection is not exposed")
                    request_id = held.get_attribute("data-id")

                    def delayed_forward(route: Any) -> None:
                        held_routes.append(route)

                    page.route(f"**/api/intercept/{request_id}/forward", delayed_forward)
                    page.locator("#forwardBtn").click()
                    route_deadline = time.monotonic() + 2.0
                    while not held_routes and time.monotonic() < route_deadline:
                        page.wait_for_timeout(10)
                    result.require(held_routes, "Forward did not issue its acknowledgement request")
                    page.wait_for_timeout(90)
                    result.require(page.locator(f'#heldList .icpt-item[data-id="{request_id}"][data-side="req"]').count() == 1, "Forward removed the request before acknowledgement")
                    held_routes[0].continue_()
                    page.wait_for_function("document.querySelectorAll('#heldList .icpt-item').length===0", timeout=10_000)
                    page.wait_for_function('id=>!document.querySelector("#heldList .icpt-item[data-id=\\\""+id+"\\\"][data-side=\\\"resp\\\"]")', arg=request_id, timeout=10_000)
                    thread.join(timeout=10)
                    result.require(not thread.is_alive(), "forwarded request did not continue")
                    result.require(response.get("status") == 200, f"forwarded response was {response}")
                    result.metrics["intercept_ack_ms"] = round((time.perf_counter() - started) * 1000, 1)
                finally:
                    for pending_route in held_routes:
                        try:
                            pending_route.continue_()
                        except Exception:
                            pass
                    # A failed assertion must not leave the fixture request
                    # blocked across later cases. Route continuation above is
                    # the normal release; the bounded join covers races where
                    # the upstream request settled just after the assertion.
                    if thread.is_alive():
                        thread.join(timeout=22)
                    if thread.is_alive():
                        result.failures.append("Forward audit cleanup left its fixture request thread alive")
                    try:
                        page.unroute(f"**/api/intercept/{request_id}/forward", delayed_forward)
                    except (UnboundLocalError, TypeError):
                        pass
                    if toggle.get_attribute("aria-pressed") == "true":
                        toggle.click()

            result.run("Intercept request Forward waits for delayed acknowledgement", intercept_request_forward_delayed_ack)

            def intercept_response_drop_delayed_ack() -> None:
                page.locator('.tab[data-tab="intercept"]').click()
                toggle = page.locator("#respInterceptToggle")
                if toggle.get_attribute("aria-pressed") != "true":
                    toggle.click()
                page.wait_for_function("document.querySelector('#respInterceptToggle')?.getAttribute('aria-pressed')==='true'")
                response: Dict[str, Any] = {}

                def held_response() -> None:
                    try:
                        response["status"], response["body"] = proxy_request(proxy, fixture_base + "/audit/intercept-response")
                    except Exception as exc:
                        response["error"] = str(exc)

                thread = threading.Thread(target=held_response, name="ui-audit-held-response", daemon=True)
                thread.start()
                request_id = None
                delayed_drop = None
                held_routes: List[Any] = []
                try:
                    page.wait_for_selector('#heldList .icpt-item[data-side="resp"]', timeout=10_000)
                    held = page.locator('#heldList .icpt-item[data-side="resp"]').first
                    request_id = held.get_attribute("data-id")
                    result.require(held.get_attribute("aria-current") == "true", "response hold selection is not exposed")

                    def delay_drop(route: Any) -> None:
                        held_routes.append(route)

                    delayed_drop = delay_drop
                    page.route(f"**/api/intercept/response/{request_id}/drop", delayed_drop)
                    page.locator("#dropBtn").click()
                    route_deadline = time.monotonic() + 2.0
                    while not held_routes and time.monotonic() < route_deadline:
                        page.wait_for_timeout(10)
                    result.require(held_routes, "Drop did not issue its acknowledgement request")
                    page.wait_for_timeout(90)
                    result.require(page.locator(f'#heldList .icpt-item[data-id="{request_id}"][data-side="resp"]').count() == 1, "Drop removed the response before acknowledgement")
                    held_routes[0].continue_()
                    page.wait_for_function('id=>!document.querySelector("#heldList .icpt-item[data-id=\\\""+id+"\\\"][data-side=\\\"resp\\\"]")', arg=request_id, timeout=10_000)
                    thread.join(timeout=10)
                    result.require(not thread.is_alive(), "dropped response did not settle the upstream request")
                finally:
                    for pending_route in held_routes:
                        try:
                            pending_route.continue_()
                        except Exception:
                            pass
                    if thread.is_alive():
                        thread.join(timeout=22)
                    if thread.is_alive():
                        result.failures.append("Drop audit cleanup left its fixture response thread alive")
                    if request_id and delayed_drop:
                        try:
                            page.unroute(f"**/api/intercept/response/{request_id}/drop", delayed_drop)
                        except Exception:
                            pass
                    if toggle.get_attribute("aria-pressed") == "true":
                        toggle.click()

            result.run("Intercept response Drop waits for delayed acknowledgement", intercept_response_drop_delayed_ack)

            def authz_context_target() -> None:
                page.locator('.tab[data-tab="proxy"]').click()
                page.wait_for_function("document.querySelectorAll('#rows .trow').length>=3", timeout=10_000)
                rows = page.locator("#rows .trow")
                selected_id = rows.nth(0).get_attribute("data-id")
                explicit_id = rows.nth(1).get_attribute("data-id")
                changed_id = rows.nth(2).get_attribute("data-id")
                page.locator(f'#rows .trow[data-id="{selected_id}"]').click(force=True)
                page.locator(f'#rows .trow[data-id="{explicit_id}"]').click(button="right", force=True)
                page.locator("#ctxmenu .ctx-item", has_text="Authz test").click(force=True)
                page.wait_for_selector("#authzModal", state="visible")
                try:
                    result.require(page.locator("#authzFlow").inner_text() == f"#{explicit_id}", "Authz ignored the context-menu flow")
                    page.evaluate(
                        "id=>document.querySelector(`#rows .trow[data-id=\\\"${id}\\\"]`)?.click()",
                        changed_id,
                    )
                    page.wait_for_function("id=>document.querySelector('#authzFlow')?.textContent===`#${id}`", arg=changed_id)
                    page.evaluate(
                        "id=>document.querySelector(`#rows .trow[data-id=\\\"${id}\\\"]`)?.click()",
                        selected_id,
                    )
                    page.wait_for_function("id=>document.querySelector('#authzFlow')?.textContent===`#${id}`", arg=selected_id)
                finally:
                    if page.locator("#authzModal").is_visible():
                        page.locator("#authzClose").click()

            result.run("Authz explicit context and A-to-B-to-A retargeting", authz_context_target)

            def burst_and_map_performance() -> None:
                page.set_viewport_size({"width": 1440, "height": 900})
                page.locator('.tab[data-tab="proxy"]').click()
                page.evaluate(
                    """() => {
                      window.__uiAuditLongTasks=[];
                      if(window.PerformanceObserver){
                        window.__uiAuditObserver=new PerformanceObserver(list=>{
                          window.__uiAuditLongTasks.push(...list.getEntries().map(entry=>entry.duration));
                        });
                        try{window.__uiAuditObserver.observe({entryTypes:['longtask']});}catch(_error){}
                      }
                    }"""
                )
                cdp = context.new_cdp_session(page)
                cdp.send("Performance.enable")
                before = cdp_metrics(cdp)
                network_samples: List[float] = []
                all_long_tasks: List[float] = []
                for run in range(args.perf_runs):
                    page.evaluate("window.__uiAuditLongTasks=[]")
                    started = time.perf_counter()
                    offset = run * args.burst
                    urls = [fixture_base + f"/audit/burst/{index % 24}?n={offset + index}" for index in range(args.burst)]
                    with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
                        statuses = list(pool.map(lambda url: proxy_request(proxy, url)[0], urls))
                    network_ms = round((time.perf_counter() - started) * 1000, 1)
                    network_samples.append(network_ms)
                    result.require(all(status == 200 for status in statuses), f"burst {run + 1} contained non-200 responses")
                    page.wait_for_timeout(1500)
                    run_long_tasks = page.evaluate("window.__uiAuditLongTasks||[]")
                    all_long_tasks.extend(run_long_tasks)
                    result.require(max(run_long_tasks or [0]) < 200, f"burst {run + 1} produced a blocking long task: {run_long_tasks}")
                page.locator('.tab[data-tab="proxy"]').click()
                rows = page.locator("#rows .trow").count()
                nodes = page.evaluate("document.getElementsByTagName('*').length")
                result.require(0 < rows <= 160, f"History DOM was not bounded after burst: {rows} rows")
                result.require(nodes < 5000, f"History DOM grew unexpectedly: {nodes} nodes")

                rows_box = page.locator("#rows")
                rows_box.evaluate("el=>{el.scrollTop=Math.min(500,el.scrollHeight-el.clientHeight)}")
                saved_scroll = rows_box.evaluate("el=>el.scrollTop")
                page.locator('.tab[data-tab="map"]').click()
                map_started = time.perf_counter()
                page.wait_for_selector("#mapTree .map-host", timeout=20_000)
                map_ready_ms = round((time.perf_counter() - map_started) * 1000, 1)
                page.locator('#mapViewSeg button[data-v="graph"]').click()
                page.wait_for_selector("#mapGraphG .g-node", timeout=10_000)
                selected = page.locator("#mapGraphG .g-node[aria-selected='true']")
                result.require(selected.count() == 1, "Map graph has no single accessible selection")
                map_before = cdp_metrics(cdp)
                page.locator("#mapFit").click()
                page.wait_for_timeout(320)
                transform_before_gesture = page.locator("#mapGraphG").get_attribute("transform") or ""
                svg = page.locator("#mapGraphSvg")
                bounds = svg.bounding_box()
                map_interaction_samples: List[float] = []
                if bounds:
                    center_x = bounds["x"] + bounds["width"] / 2
                    center_y = bounds["y"] + bounds["height"] / 2
                    started = time.perf_counter()
                    page.mouse.move(center_x, center_y)
                    page.mouse.wheel(0, 280)
                    page.mouse.down()
                    page.mouse.move(center_x + 24, center_y + 12, steps=4)
                    page.mouse.up()
                    page.wait_for_timeout(320)
                    map_interaction_samples.append(round((time.perf_counter() - started) * 1000, 1))
                    transform_after_gesture = page.locator("#mapGraphG").get_attribute("transform") or ""
                    result.require(
                        transform_after_gesture != transform_before_gesture,
                        "Map wheel/drag did not change graph transform",
                    )
                else:
                    result.failures.append("Map SVG has no layout box for wheel/drag performance check")
                map_after = cdp_metrics(cdp)
                page.locator('.tab[data-tab="proxy"]').click()
                result.require(abs(rows_box.evaluate("el=>el.scrollTop") - saved_scroll) < 2, "panel navigation lost History scroll state")

                after = cdp_metrics(cdp)
                transition_samples = page.evaluate(
                    """async names => {
                      const samples=[];
                      for(const name of names){
                        const tab=document.querySelector(`.tab[data-tab="${name}"]`);
                        const panel=document.querySelector(`.panel[data-panel="${name}"]`);
                        if(!tab||!panel||tab.getAttribute('aria-selected')==='true')continue;
                        const start=performance.now();
                        tab.click();
                        const animations=panel.getAnimations();
                        await Promise.all(animations.map(animation=>animation.finished.catch(()=>{})));
                        samples.push(performance.now()-start);
                      }
                      return samples;
                    }""",
                    list(TOP_LEVEL_TABS),
                )
                result.metrics.update(
                    {
                        "burst_requests": args.burst,
                        "burst_runs": args.perf_runs,
                        "burst_network_ms": network_samples[0],
                        "burst_network_p95_ms": percentile(network_samples, 95),
                        "history_rendered_rows": rows,
                        "history_dom_nodes": nodes,
                        "long_tasks_ms": [round(value, 2) for value in all_long_tasks],
                        "long_task_p95_ms": percentile(all_long_tasks, 95) or 0.0,
                        "panel_interaction_ms": [round(value, 2) for value in transition_samples],
                        "panel_interaction_p95_ms": percentile(transition_samples, 95),
                        "map_ready_ms": map_ready_ms,
                        "map_interaction_ms": map_interaction_samples,
                        "map_interaction_p95_ms": percentile(map_interaction_samples, 95),
                        "cdp_task_duration_s": metric_delta(before, after, "TaskDuration"),
                        "cdp_script_duration_s": metric_delta(before, after, "ScriptDuration"),
                        "cdp_layout_duration_s": metric_delta(before, after, "LayoutDuration"),
                        "cdp_map_task_duration_s": metric_delta(map_before, map_after, "TaskDuration"),
                        "cdp_map_script_duration_s": metric_delta(map_before, map_after, "ScriptDuration"),
                        "cdp_map_layout_duration_s": metric_delta(map_before, map_after, "LayoutDuration"),
                    }
                )

            result.run("high-volume History, Map render/Fit, scroll, and CDP performance", burst_and_map_performance)

            def mobile_dense_reachability() -> None:
                page.set_viewport_size({"width": 390, "height": 844})
                key_controls = {
                    "proxy": "#fSearch",
                    "intercept": "#interceptToggle",
                    "repeater": "#repUrl",
                    "intruder": "#intrTarget",
                    "scanner": "#scanRun",
                    "map": "#mapDomain",
                    "findings": "#findNew",
                    "notes": "#notesEdit",
                    "activity": "#actIntentFilter",
                    "settings": "#setSearch",
                }
                for name in TOP_LEVEL_TABS:
                    page.locator(f'.tab[data-tab="{name}"]').click()
                    panel = page.locator(f'.panel[data-panel="{name}"]')
                    result.require(panel.is_visible(), f"mobile {name} panel is not visible")
                    box = panel.bounding_box()
                    result.require(box is not None and box["width"] > 0 and box["height"] > 0, f"mobile {name} panel has no reachable layout box")
                    controls = panel.locator("button, input, select, textarea, [tabindex]").count()
                    result.require(controls > 0, f"mobile {name} panel has no keyboard/pointer controls")
                    result.require(
                        page.evaluate("document.documentElement.scrollWidth===document.documentElement.clientWidth"),
                        f"mobile {name} panel introduces document overflow",
                    )
                    key = page.locator(key_controls[name])
                    key.scroll_into_view_if_needed()
                    result.require(key.is_visible(), f"mobile {name} key control is not reachable")
                page.locator('.tab[data-tab="scanner"]').click()
                for button_id, modal_id, close_id in (("checksBtn", "checksModal", "checksClose"), ("codecsBtn", "codecsModal", "codecsClose")):
                    button = page.locator(f"#{button_id}")
                    result.require(button.is_visible() and not button.is_disabled(), f"mobile {button_id} is not reachable")
                    button.click()
                    page.wait_for_selector(f"#{modal_id}[style*='display'], #{modal_id}", state="visible", timeout=10_000)
                    result.require(page.locator(f"#{modal_id} [role='dialog']").is_visible(), f"mobile {modal_id} dialog did not open")
                    result.require(page.locator(f"#{modal_id} button, #{modal_id} input, #{modal_id} textarea").count() > 0, f"mobile {modal_id} has no reachable controls")
                    page.locator(f"#{close_id}").click()
                    page.wait_for_selector(f"#{modal_id}", state="hidden", timeout=10_000)
                result.require(page.evaluate("document.documentElement.scrollWidth===document.documentElement.clientWidth"), "mobile dense panels have document overflow")

            result.run("mobile reachability for every dense panel and Checks/Codecs", mobile_dense_reachability)

            def screenshots() -> None:
                screenshot_meta: Dict[str, Dict[str, Any]] = {}

                def capture(name: str, path: Path, dimensions: Tuple[int, int]) -> None:
                    page.screenshot(path=str(path))
                    width, height = png_dimensions(path)
                    result.require((width, height) == dimensions, f"{path.name} is {width}x{height}, expected {dimensions[0]}x{dimensions[1]}")
                    screenshot_meta[name] = {"path": str(path), "width": width, "height": height}

                page.set_viewport_size({"width": 1440, "height": 900})
                page.locator('.tab[data-tab="proxy"]').click()
                page.locator("#flowSearchScripts").evaluate("element=>{element.open=false}")
                page.evaluate(
                    """() => {
                      const completed=[...document.querySelectorAll('#rows .trow')]
                        .find(row=>/^\d{3}$/.test(row.querySelector('.tr-st')?.textContent.trim()||''));
                      completed?.click();
                    }"""
                )
                page.wait_for_timeout(220)
                page.mouse.move(170, 20)
                capture("1440x900", output / "after-1440x900-proxy.png", (1440, 900))

                page.set_viewport_size({"width": 1024, "height": 768})
                page.locator('.tab[data-tab="map"]').click()
                page.locator('#mapViewSeg button[data-v="graph"]').click()
                page.wait_for_selector("#mapGraphG .g-node", timeout=10_000)
                page.locator("#mapFit").click()
                page.wait_for_timeout(320)
                page.mouse.move(170, 20)
                capture("1024x768", output / "after-1024x768-map.png", (1024, 768))

                page.set_viewport_size({"width": 390, "height": 844})
                page.locator('.tab[data-tab="scanner"]').click()
                page.wait_for_function("!document.querySelector('#scanRescanState')?.textContent.startsWith('Loading')", timeout=10_000)
                result.require(page.evaluate("document.documentElement.scrollWidth===document.documentElement.clientWidth"), "mobile Scanner screenshot has document overflow")
                page.mouse.move(170, 20)
                capture("390x844", output / "after-390x844-scanner.png", (390, 844))
                result.metrics["screenshots"] = screenshot_meta

            result.run("required screenshots", screenshots)

        if result.console_errors:
            result.failures.append("unexpected console errors: " + " | ".join(result.console_errors))
        if result.page_errors:
            result.failures.append("unexpected page errors: " + " | ".join(result.page_errors))
        if result.http_errors:
            result.failures.append("unexpected HTTP errors: " + " | ".join(result.http_errors))
        unique_external = sorted(set(result.external_requests))
        if unique_external:
            result.failures.append("external browser requests: " + " | ".join(unique_external))
        result.external_requests = unique_external

        context.close()
        browser.close()

    if fixture is not None:
        fixture.shutdown()
        fixture.server_close()

    report = {
        "base_url": base,
        "mode": "full" if args.full else "smoke",
        "viewports": [list(viewport) for viewport in VIEWPORTS],
        "cases": result.cases,
        "metrics": result.metrics,
        "console_errors": result.console_errors,
        "expected_console_errors": result.expected_console_errors,
        "page_errors": result.page_errors,
        "http_errors": result.http_errors,
        "expected_http_errors": result.expected_http_errors,
        "external_requests": result.external_requests,
        "before_screenshots": baseline_artifacts,
        "after_screenshots": result.metrics.get("screenshots", {}),
        "failures": result.failures,
    }
    (output / "browser-audit.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:9966")
    parser.add_argument("--proxy", type=parse_host_port, default=("127.0.0.1", 8080), metavar="HOST:PORT")
    parser.add_argument("--output-dir", default="/tmp/interseptor-ui-audit")
    parser.add_argument("--full", action="store_true", help="mutate only the isolated project supplied by the operator")
    parser.add_argument("--burst", type=int, default=240, help="requests in the full high-volume pass")
    parser.add_argument("--perf-runs", type=int, default=3, help="repeat the high-volume profile this many times")
    parser.add_argument("--headed", action="store_true")
    args = parser.parse_args()
    if args.burst < 120:
        parser.error("--burst must be at least 120 to exercise History virtualization")
    if args.perf_runs < 1 or args.perf_runs > 10:
        parser.error("--perf-runs must be between 1 and 10")
    result = run_audit(args)
    print(json.dumps({"cases": result.cases, "metrics": result.metrics, "failures": result.failures}, indent=2, sort_keys=True))
    return 1 if result.failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
