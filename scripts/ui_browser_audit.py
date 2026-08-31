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
    console_sink = result.expected_console_errors if expected_console_errors else result.console_errors
    http_sink = result.expected_http_errors if expected_console_errors else result.http_errors
    page.on(
        "console",
        lambda message: console_sink.append(message.text)
        if message.type == "error"
        else None,
    )
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

            def auxiliary_read_only_surfaces() -> None:
                # Exercise the read paths and harmless validators without
                # creating keys, enabling OOB/system proxy, contacting peers,
                # or writing real target data. The full run uses an isolated
                # project, so opening these surfaces is still representative.
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
                if not oob_enabled:
                    result.require(
                        page.locator("#oobDisabledHint").is_visible(),
                        "Settings does not explain that OOB is disabled",
                    )
                page.locator('.tab[data-tab="scanner"]').click()
                # OOB is intentionally disabled on a fresh project.  Verify
                # that state is explicit and safe; if the fixture has OOB
                # enabled, exercise only the local modal and close it again.
                oob_button = page.locator("#oobBtn")
                if oob_enabled:
                    result.require(oob_button.is_visible(), "enabled OOB action is hidden")
                    if oob_button.is_enabled():
                        oob_button.click()
                        page.wait_for_selector("#oobModal", state="visible", timeout=10_000)
                        page.locator("#oobClose").click()
                        page.wait_for_selector("#oobModal", state="hidden", timeout=10_000)
                    else:
                        result.require(
                            oob_button.is_disabled(),
                            "unavailable OOB action is not exposed as disabled",
                        )
                else:
                    result.require(
                        not oob_button.is_visible(),
                        "disabled OOB action remained visible in the Scanner toolbar",
                    )

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
                allow_row.locator("[data-allow-del]").click()
                page.wait_for_selector("#confirmModal", state="visible", timeout=10_000)
                page.locator("#confirmOk").click()
                page.wait_for_function("!document.querySelector('#allowList tr')?.textContent.includes('127.0.0.254/32')", timeout=10_000)
                result.require(page.locator("#sysProxyToggle").get_attribute("aria-pressed") == "false", "read-only audit changed the system proxy")

            result.run("read-only auxiliary surfaces and validators", auxiliary_read_only_surfaces)

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
