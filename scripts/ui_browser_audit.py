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
import hashlib
import http.client
import json
import os
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple
from urllib.parse import quote, urlsplit

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
AUDIT_SENTINEL_NAME = ".interseptor-ui-audit-sentinel"
AUDIT_PROJECT_PREFIX = "ui-audit-"
ENCODE_URI_COMPONENT_SAFE = "~()*!.'-_"


def browser_project_storage_key(base: str, project: str) -> str:
    return f"{base}.v2.{quote(str(project), safe=ENCODE_URI_COMPONENT_SAFE)}"


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
        payload = response.read(256 * 1024 + 1)
        if len(payload) > 256 * 1024:
            raise ValueError("direct proxy response exceeds 256 KiB")
        return response.status, payload
    finally:
        connection.close()


def bounded_response_diagnostic(status: int, body: bytes) -> str:
    """Keep direct-proxy failures useful without dumping captured data."""
    digest = hashlib.sha256(body).hexdigest()[:16]
    return f"status {status}; response bytes {len(body)}; sha256 {digest}"


def _json_get(base: str, path: str) -> Tuple[int, Dict[str, Any], str]:
    parsed = urlsplit(base)
    if parsed.scheme != "http" or not parsed.hostname or parsed.port is None:
        raise ValueError("audit base URL must be an explicit HTTP HOST:PORT")
    connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=5)
    try:
        connection.request("GET", path, headers={"Connection": "close"})
        response = connection.getresponse()
        raw = response.read(256 * 1024 + 1)
        if len(raw) > 256 * 1024:
            raise ValueError(f"{path} response exceeds 256 KiB")
        text = raw.decode("utf-8", errors="replace")
        if response.status < 200 or response.status >= 300:
            raise ValueError(f"{path} returned status {response.status}")
        value = json.loads(text)
        if not isinstance(value, dict):
            raise ValueError(f"{path} returned a non-object JSON response")
        return response.status, value, text
    finally:
        connection.close()


def expected_fallback_version() -> str:
    source = Path(__file__).resolve().parents[1] / "internal/version/version.go"
    match = re.search(r'(?m)^\s*(?:var|const)\s+Version\s*=\s*"([^"\\]+)"', source.read_text())
    if not match:
        raise ValueError("could not read fallback version from internal/version/version.go")
    return match.group(1)


def validate_expected_data_dir(raw: str) -> Path:
    """Accept only a real, direct child audit root under the OS temp dir."""
    candidate = Path(raw).expanduser()
    temp_root = Path(tempfile.gettempdir()).resolve()
    if candidate.is_symlink() or not candidate.is_dir():
        raise ValueError("--expected-data-dir must be an existing non-symlink directory")
    resolved = candidate.resolve()
    if resolved == temp_root or resolved.parent != temp_root or not resolved.name.startswith("interseptor-ui-audit-"):
        raise ValueError("--expected-data-dir must be a direct interseptor-ui-audit-* directory under the OS temp dir")
    return resolved


def validate_expected_project(name: str) -> str:
    if not re.fullmatch(r"ui-audit-[a-z0-9][a-z0-9-]{0,47}", name or ""):
        raise ValueError("--expected-project must be a safe bare project name")
    return name


def reserve_loopback_listener() -> socket.socket:
    """Bind and retain one listener that the managed child will inherit."""
    if os.name != "posix":
        raise RuntimeError("managed full audits require POSIX listener descriptor passing")
    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        listener.bind(("127.0.0.1", 0))
        listener.listen(socket.SOMAXCONN)
        listener.set_inheritable(True)
        return listener
    except Exception:
        listener.close()
        raise


def runtime_source_paths(repo_root: Path) -> List[str]:
    listed = subprocess.run(
        [
            "git",
            "ls-files",
            "--cached",
            "--others",
            "--exclude-standard",
            "-z",
            "--",
            "go.mod",
            "go.sum",
            "cmd",
            "internal",
        ],
        cwd=repo_root,
        check=True,
        capture_output=True,
    ).stdout
    paths = []
    for relative_bytes in listed.split(b"\0"):
        if not relative_bytes:
            continue
        relative = Path(relative_bytes.decode("utf-8", errors="surrogateescape"))
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("runtime source manifest contains an unsafe path")
        path = repo_root / relative
        if path.is_file() and not path.name.endswith("_test.go"):
            paths.append(relative.as_posix())
    return sorted(set(paths))


def runtime_source_digest(source_root: Path, relative_paths: List[str]) -> str:
    digest = hashlib.sha256()
    for relative in relative_paths:
        digest.update(relative.encode("utf-8") + b"\0")
        digest.update(hashlib.sha256((source_root / relative).read_bytes()).digest())
    return digest.hexdigest()


def source_base_commit(repo_root: Path) -> str:
    completed = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=repo_root,
        check=True,
        capture_output=True,
        text=True,
    )
    return completed.stdout.strip()


def source_identity(source_root: Path, relative_paths: List[str], base_commit: str) -> Dict[str, Any]:
    return {
        "worktree_base_commit": base_commit,
        "runtime_sha256": runtime_source_digest(source_root, relative_paths),
        "runtime_files": len(relative_paths),
        "audit_harness_sha256": hashlib.sha256(Path(__file__).resolve().read_bytes()).hexdigest(),
    }


def create_runtime_source_snapshot(repo_root: Path, snapshot: Path) -> Tuple[Dict[str, Any], List[str]]:
    source_paths = runtime_source_paths(repo_root)
    base_commit = source_base_commit(repo_root)
    snapshot.mkdir(mode=0o700)
    for relative in source_paths:
        destination = snapshot / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes((repo_root / relative).read_bytes())
    return source_identity(snapshot, source_paths, base_commit), source_paths


def managed_build_env(root: Path) -> Dict[str, str]:
    build_home = root / "build-home"
    build_cache = root / "go-build-cache"
    module_cache = root / "go-module-cache"
    build_temp = root / "build-temp"
    for path in (build_home, build_cache, module_cache, build_temp):
        path.mkdir(mode=0o700)
    env = {
        key: os.environ[key]
        for key in (
            "HTTP_PROXY",
            "HTTPS_PROXY",
            "NO_PROXY",
            "ALL_PROXY",
            "http_proxy",
            "https_proxy",
            "no_proxy",
            "all_proxy",
            "SSL_CERT_FILE",
            "SSL_CERT_DIR",
        )
        if key in os.environ
    }
    env.update({
        "HOME": str(build_home),
        "PATH": os.defpath,
        "TMPDIR": str(build_temp),
        "CGO_ENABLED": "0",
        "GO111MODULE": "on",
        "GOENV": "off",
        "GOFLAGS": "",
        "GOWORK": "off",
        "GOTOOLCHAIN": "local",
        "GOPATH": str(root / "go-path"),
        "GOMODCACHE": str(module_cache),
        "GOCACHE": str(build_cache),
        "GOTELEMETRY": "off",
    })
    return env


def managed_candidate_env(control_fd: int, proxy_fd: int) -> Dict[str, str]:
    """Keep the host toolchain environment but remove product-specific drift."""
    env = {key: value for key, value in os.environ.items() if not key.startswith("INTERSEPTOR_")}
    env.update({
        "INTERSEPTOR_UI_AUDIT_MANAGED": "1",
        "INTERSEPTOR_UI_AUDIT_CONTROL_FD": str(control_fd),
        "INTERSEPTOR_UI_AUDIT_PROXY_FD": str(proxy_fd),
        "INTERSEPTOR_NO_UPDATE_CHECK": "1",
        "INTERSEPTOR_NO_BROWSER": "1",
    })
    return env


def stop_managed_process(process: subprocess.Popen[bytes]) -> None:
    """Stop only the candidate process created by this audit."""
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def close_managed_reservations(reservations: List[socket.socket]) -> None:
    for listener in reservations:
        listener.close()


def remove_managed_root(root: Path, project: str) -> None:
    """Remove only the exact sentinel-owned root created for this run."""
    validated = validate_expected_data_dir(str(root))
    project = validate_expected_project(project)
    sentinel = validated / AUDIT_SENTINEL_NAME
    expected = f"interseptor-ui-audit\nproject={project}\n"
    try:
        owned = (
            sentinel.is_file()
            and not sentinel.is_symlink()
            and sentinel.stat().st_size == len(expected.encode("utf-8"))
            and sentinel.read_text(encoding="utf-8") == expected
        )
    except (OSError, UnicodeError):
        owned = False
    if not owned:
        raise RuntimeError("managed audit cleanup refused an unowned root")
    shutil.rmtree(validated)


def prepare_managed_audit() -> Tuple[subprocess.Popen[bytes], Path, str, str, Tuple[str, int], Dict[str, Any], List[socket.socket]]:
    """Build and start one disposable candidate owned by this audit process."""
    repo_root = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix="interseptor-ui-audit-"))
    project = f"{AUDIT_PROJECT_PREFIX}{secrets.token_hex(6)}"
    validate_expected_project(project)
    (root / "projects" / project).mkdir(parents=True)
    (root / AUDIT_SENTINEL_NAME).write_text(f"interseptor-ui-audit\nproject={project}\n", encoding="utf-8")
    binary = root / "interseptor-audit"
    try:
        snapshot = root / "runtime-source"
        application_source, source_paths = create_runtime_source_snapshot(repo_root, snapshot)
        go_binary = shutil.which("go")
        if not go_binary:
            raise RuntimeError("Go toolchain is unavailable")
        subprocess.run(
            [str(Path(go_binary).resolve()), "build", "-mod=readonly", "-modcacherw", "-o", str(binary), "./cmd/interseptor"],
            cwd=snapshot,
            env=managed_build_env(root),
            check=True,
            timeout=300,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if runtime_source_digest(snapshot, source_paths) != application_source["runtime_sha256"]:
            raise RuntimeError("managed audit source snapshot changed during build")
        reservations: List[socket.socket] = []
        reservations.append(reserve_loopback_listener())
        reservations.append(reserve_loopback_listener())
        control_listener, proxy_listener = reservations
        control_port = int(control_listener.getsockname()[1])
        proxy_port = int(proxy_listener.getsockname()[1])
        process = subprocess.Popen(
            [str(binary), "--data-dir", str(root), "--project", project, "--control-port", str(control_port), "--proxy-port", str(proxy_port)],
            cwd=repo_root,
            env=managed_candidate_env(control_listener.fileno(), proxy_listener.fileno()),
            pass_fds=(control_listener.fileno(), proxy_listener.fileno()),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        base = f"http://127.0.0.1:{control_port}"
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("managed audit candidate exited before readiness")
            try:
                _json_get(base, "/api/version")
                return process, root, project, base, ("127.0.0.1", proxy_port), application_source, reservations
            except Exception:
                time.sleep(0.1)
        raise RuntimeError("managed audit candidate did not become ready")
    except Exception:
        if 'process' in locals() and process.poll() is None:
            stop_managed_process(process)
        if 'reservations' in locals():
            close_managed_reservations(reservations)
        remove_managed_root(root, project)
        raise


def cleanup_managed_audit(process: subprocess.Popen[bytes], root: Path, project: str, reservations: List[socket.socket]) -> None:
    stop_managed_process(process)
    close_managed_reservations(reservations)
    remove_managed_root(root, project)


def full_audit_preflight(base: str, proxy: Tuple[str, int], expected_project: str, expected_data_dir: str) -> Dict[str, Any]:
    """Verify that full mode is pointed at the intended isolated workstation."""
    expected_root = validate_expected_data_dir(expected_data_dir)
    expected_project = validate_expected_project(expected_project)
    sentinel = expected_root / AUDIT_SENTINEL_NAME
    expected_sentinel = f"interseptor-ui-audit\nproject={expected_project}\n"
    try:
        sentinel_ok = (
            sentinel.is_file()
            and not sentinel.is_symlink()
            and sentinel.stat().st_size == len(expected_sentinel.encode("utf-8"))
            and sentinel.read_text(encoding="utf-8") == expected_sentinel
        )
    except (OSError, UnicodeError):
        sentinel_ok = False
    if not sentinel_ok:
        raise ValueError("audit sentinel is absent or invalid under --expected-data-dir")
    expected_version = expected_fallback_version().lstrip("v")
    _, version, _ = _json_get(base, "/api/version")
    observed_version = str(version.get("version", "")).strip().lstrip("v")
    if observed_version != expected_version:
        raise ValueError(f"server version {observed_version!r} does not match source fallback {expected_version!r}")
    _, project, _ = _json_get(base, "/api/project")
    observed_project = str(project.get("current", "")).strip()
    if observed_project != expected_project:
        raise ValueError("server project does not match --expected-project")
    if project.get("canSwitch") is not False:
        raise ValueError("managed audit candidate did not lock project switching")
    observed_dir = str(project.get("dir", "")).strip()
    try:
        observed_path = Path(observed_dir).expanduser().resolve()
        projects_dir = expected_root / "projects"
        expected_project_path = projects_dir / expected_project
        expected_project_dir = expected_project_path.resolve()
        data_dir_match = (
            bool(observed_dir)
            and projects_dir.is_dir()
            and not projects_dir.is_symlink()
            and expected_project_path.is_dir()
            and not expected_project_path.is_symlink()
            and os.path.commonpath((str(observed_path), str(expected_root))) == str(expected_root)
            and observed_path == expected_project_dir
        )
    except (OSError, RuntimeError, ValueError):
        data_dir_match = False
    if not data_dir_match:
        raise ValueError("server project directory does not match the isolated audit project")
    _, flows, _ = _json_get(base, "/api/flows?limit=1&includeTools=1")
    _, findings, _ = _json_get(base, "/api/findings?view=summary")
    if not isinstance(flows.get("flows"), list) or not isinstance(findings.get("findings"), list):
        raise ValueError("full audit freshness responses must contain list members")
    if flows.get("flows") or findings.get("findings"):
        raise ValueError("full audit requires a fresh empty project")
    _, settings, _ = _json_get(base, "/api/settings")
    expected_proxy = f"{proxy[0]}:{proxy[1]}"
    # The server contract is settings.proxyAddr (the JSON field is proxyAddr).
    observed_proxy = str(settings.get("proxyAddr", "")).strip()
    if observed_proxy != expected_proxy:
        raise ValueError("server proxy does not match --proxy")
    upstream = str(settings.get("upstreamProxy", "")).strip()
    if upstream:
        raise ValueError("full loopback audit requires an empty upstreamProxy")
    return {
        "ok": True,
        "base_netloc": urlsplit(base).netloc,
        "expected_version": expected_version,
        "server_version": observed_version,
        "project_match": True,
        "project_switch_locked": True,
        "disposable_project": True,
        "data_dir_match": True,
        "fresh_flows": True,
        "fresh_findings": True,
        "expected_proxy": expected_proxy,
        "server_proxy": observed_proxy,
        "upstream_configured": bool(upstream),
    }


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


def runtime_source_identity() -> Dict[str, Any]:
    repo_root = Path(__file__).resolve().parents[1]
    source_paths = runtime_source_paths(repo_root)
    return source_identity(repo_root, source_paths, source_base_commit(repo_root))


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


def invalidate_and_close_flow_popup(page: Page) -> None:
    """Invalidate popup epochs until the modal stays hidden for two seconds."""
    deadline = time.monotonic() + 15
    hidden_since: Optional[float] = None
    while time.monotonic() < deadline:
        now = time.monotonic()
        if page.locator("#flowModal").is_visible():
            hidden_since = None
        elif hidden_since is None:
            hidden_since = now
        elif time.monotonic() - hidden_since >= 2:
            return
        page.locator("#fmClose").evaluate("button => button.click()")
        page.wait_for_timeout(100)
    raise TimeoutError("flow popup did not remain hidden during journey teardown")


def attach_observers(page: Page, result: AuditResult, base_netloc: str, expected_console_errors: bool = False) -> None:
    http_sink = result.expected_http_errors if expected_console_errors else result.http_errors

    def record_console(message: Any) -> None:
        if message.type != "error":
            return
        location = message.location or {}
        request_url = str(location.get("url") or "")
        if expected_console_errors:
            result.expected_console_errors.append(message.text)
        elif request_url in result.expected_console_request_urls:
            result.expected_console_request_urls.remove(request_url)
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


def run_audit(args: argparse.Namespace, application_source: Optional[Dict[str, Any]] = None) -> AuditResult:
    result = AuditResult()
    if application_source is None:
        application_source = runtime_source_identity()
    base = args.base_url.rstrip("/")
    base_netloc = urlsplit(base).netloc
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=True)
    fixture: Optional[ThreadingHTTPServer] = None
    baseline_artifacts: Dict[str, Dict[str, Any]] = {}
    preflight: Dict[str, Any] = {"required": bool(args.full), "ok": not args.full}
    if args.full:
        try:
            preflight = full_audit_preflight(base, args.proxy, args.expected_project, args.expected_data_dir)
        except Exception as exc:
            # Fail before Playwright creates a page or navigates to a target.
            message = f"full audit preflight: {type(exc).__name__}: {exc}"
            result.failures.append(message)
            preflight = {"required": True, "ok": False, "error": str(exc)[:512]}
            output = Path(args.output_dir)
            output.mkdir(parents=True, exist_ok=True)
            report = {
                "application_source": application_source,
                "base_url": base,
                "mode": "full",
                "preflight": preflight,
                "cases": result.cases,
                "metrics": result.metrics,
                "console_errors": [], "expected_console_errors": [],
                "page_errors": [], "http_errors": [], "expected_http_errors": [],
                "external_requests": [], "before_screenshots": {}, "after_screenshots": {},
                "failures": result.failures,
            }
            (output / "browser-audit.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
            return result

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

        def assert_module_startup_recovery(module_page: Page, engine_name: str, exercise_keyboard: bool = False) -> None:
            module_page.wait_for_selector(
                "#workspaceHydrationStatus[data-workspace-boot-failed='true']",
                state="visible",
            )
            module_status = module_page.locator("#workspaceHydrationStatus")
            result.require(
                "Workspace scripts could not start" in module_status.inner_text(),
                "module failure left the static saved-workspace loading message",
            )
            result.require(module_status.get_attribute("role") == "alert", "module startup failure is not announced as an alert")
            result.require(module_status.get_attribute("aria-live") == "assertive", "module startup failure is not announced assertively")
            result.require(module_status.locator("[data-workspace-retry]").is_enabled(), "module startup failure has no usable reload action")
            result.require(
                module_page.locator(".tab:disabled").count() == module_page.locator(".tab").count(),
                "module startup failure left nonfunctional navigation enabled",
            )
            result.require(module_page.locator("#main").evaluate("element => element.inert"), "dead workspace controls remain interactive")
            result.require(module_page.evaluate("document.getAnimations().length") == 0, "module startup recovery relies on motion")
            if not exercise_keyboard:
                return
            if engine_name != "webkit":
                module_page.evaluate("document.activeElement?.blur()")
                module_page.keyboard.press("Tab")
                result.require(
                    module_page.evaluate("document.activeElement?.hasAttribute('data-workspace-retry')"),
                    "Reload is not the first usable keyboard action after module startup failure",
                )
            # Playwright WebKit follows Safari's platform preference that can
            # omit buttons from sequential Tab focus. Direct focus plus Enter
            # still verifies the native keyboard activation contract there.
            module_status.locator("[data-workspace-retry]").focus()
            result.require(
                module_page.evaluate("document.activeElement?.hasAttribute('data-workspace-retry')"),
                "Reload cannot receive keyboard focus after module startup failure",
            )
            with module_page.expect_navigation(wait_until="domcontentloaded"):
                module_page.keyboard.press("Enter")
            module_page.wait_for_selector(
                "#workspaceHydrationStatus[data-workspace-boot-failed='true']",
                state="visible",
            )

        def workspace_module_fetch_recovery(target_browser: Browser, engine_name: str) -> None:
            module_context = target_browser.new_context(viewport={"width": 1024, "height": 768}, reduced_motion="reduce")
            module_page = module_context.new_page()
            module_page.set_default_timeout(10_000)
            failed_requests: List[str] = []
            module_page.on("requestfailed", lambda request: failed_requests.append(request.url))
            module_page.route("**/js/tools.js", lambda route: route.abort())
            try:
                module_page.goto(base, wait_until="domcontentloaded")
                assert_module_startup_recovery(module_page, engine_name, exercise_keyboard=True)
                result.require(any(url.endswith("/js/tools.js") for url in failed_requests), "module fetch fault was not exercised")
            finally:
                module_context.close()

        def workspace_module_evaluation_recovery(target_browser: Browser, engine_name: str) -> None:
            module_context = target_browser.new_context(viewport={"width": 1024, "height": 768}, reduced_motion="reduce")
            module_page = module_context.new_page()
            module_page.set_default_timeout(10_000)
            served_faults: List[str] = []

            def fail_tools_evaluation(route: Any) -> None:
                served_faults.append(route.request.url)
                route.fulfill(
                    status=200,
                    content_type="application/javascript",
                    body=(
                        "export const repInit=()=>{},intrInit=()=>{},repSend=()=>{},"
                        "sendToRepeater=()=>{},sendToIntruder=()=>{},scheduleIntr=()=>{},"
                        "releaseWorkstationReady=()=>{},uiStateSyncPending=()=>false,"
                        "retryUIStateSync=()=>{},workspaceStorageWarningMessage=()=>'';"
                        "throw new Error('injected module evaluation failure');"
                    ),
                )

            module_page.route("**/js/tools.js", fail_tools_evaluation)
            try:
                module_page.goto(base, wait_until="domcontentloaded")
                assert_module_startup_recovery(module_page, engine_name)
                result.require(any(url.endswith("/js/tools.js") for url in served_faults), "module evaluation fault was not exercised")
            finally:
                module_context.close()

        def workspace_hydration_recovery(target_browser: Browser, engine_name: str) -> None:
            stalled_context = target_browser.new_context(viewport={"width": 1024, "height": 768})
            stalled_page = stalled_context.new_page()
            stalled_page.set_default_timeout(10_000)
            stalled_page.add_init_script(
                """(() => {
                  const nativeFetch=window.fetch;
                  window.__workspaceStalledFetches=0;
                  window.fetch=function(input,init){
                    const url=typeof input==='string'?input:String(input&&input.url||'');
                    if(url.includes('/api/ui/repeater')){
                      window.__workspaceStalledFetches++;
                      return new Promise(()=>{});
                    }
                    return nativeFetch.call(this,input,init);
                  };
                })()"""
            )
            try:
                started = time.perf_counter()
                stalled_page.goto(base, wait_until="domcontentloaded")
                stalled_page.wait_for_function(
                    "document.querySelector('#workspaceHydrationStatus')?.textContent.includes('Saved workspace unavailable')",
                    timeout=5_000,
                )
                elapsed_ms = (time.perf_counter() - started) * 1000
                stalled_status = stalled_page.locator("#workspaceHydrationStatus")
                result.require(elapsed_ms < 4_500, f"saved workspace request deadline settled too late ({elapsed_ms:.1f}ms)")
                result.require(stalled_status.get_attribute("role") == "status", "local-draft fallback is not a polite status")
                result.require(stalled_status.get_attribute("aria-live") == "polite", "local-draft fallback is not announced politely")
                result.require(stalled_status.locator("[data-workspace-retry]").is_enabled(), "saved workspace failure has no usable retry action")
                result.require(stalled_page.locator("#tabs").get_attribute("aria-busy") == "false", "local-draft recovery left navigation busy")
                result.require(
                    stalled_page.locator(".tab:disabled").count() == 0,
                    "local-draft recovery left navigation disabled",
                )
                result.require(stalled_page.evaluate("window.__workspaceStalledFetches") > 0, "hydration stall fault was not exercised")
                result.metrics[f"workspace_hydration_timeout_ms_{engine_name}"] = round(elapsed_ms, 1)
            finally:
                stalled_context.close()

        def project_identity_sibling_recovery(target_browser: Browser, engine_name: str) -> None:
            _, version_info, _ = _json_get(base, "/api/version")
            project_identity = str(version_info.get("projectDir") or "")
            identity_context = target_browser.new_context(viewport={"width": 1024, "height": 768})
            identity_page = identity_context.new_page()
            identity_page.set_default_timeout(10_000)
            identity_page.add_init_script(
                """(() => {
                  const nativeFetch=window.fetch;
                  window.__projectIdentityStalls=0;
                  window.fetch=function(input,init){
                    const url=typeof input==='string'?input:String(input&&input.url||'');
                    if(url.includes('/api/project')){
                      window.__projectIdentityStalls++;
                      return new Promise(()=>{});
                    }
                    return nativeFetch.call(this,input,init);
                  };
                })()"""
            )
            attach_observers(identity_page, result, base_netloc)
            try:
                started = time.perf_counter()
                identity_page.goto(base, wait_until="domcontentloaded")
                wait_ready(identity_page)
                elapsed_ms = (time.perf_counter() - started) * 1000
                storage_key = identity_page.evaluate(
                    "async () => (await import('/js/core.js')).projectStorageKey('identity.audit')"
                )
                result.require(elapsed_ms < 4_500, f"project identity sibling fallback settled too late ({elapsed_ms:.1f}ms)")
                result.require(identity_page.evaluate("window.__projectIdentityStalls") > 0, "project identity stall fault was not exercised")
                result.require(
                    storage_key == browser_project_storage_key("identity.audit", project_identity),
                    "project identity fallback did not retain the canonical directory",
                )
                result.metrics[f"project_identity_fallback_ms_{engine_name}"] = round(elapsed_ms, 1)
            finally:
                identity_context.close()

        for engine_name in args.startup_engines:
            target_browser = browser if engine_name == "chromium" else getattr(playwright, engine_name).launch(headless=not args.headed)
            try:
                result.run(
                    f"workspace module fetch failure becomes actionable ({engine_name})",
                    lambda current=target_browser, name=engine_name: workspace_module_fetch_recovery(current, name),
                )
                result.run(
                    f"workspace module evaluation failure becomes actionable ({engine_name})",
                    lambda current=target_browser, name=engine_name: workspace_module_evaluation_recovery(current, name),
                )
                result.run(
                    f"workspace hydration timeout falls back locally ({engine_name})",
                    lambda current=target_browser, name=engine_name: workspace_hydration_recovery(current, name),
                )
                result.run(
                    f"project identity accepts a valid sibling ({engine_name})",
                    lambda current=target_browser, name=engine_name: project_identity_sibling_recovery(current, name),
                )
            finally:
                if target_browser is not browser:
                    target_browser.close()

        def delayed_module_eventually_recovers() -> None:
            delayed_context = browser.new_context(viewport={"width": 1024, "height": 768})
            delayed_context.add_init_script(
                """(() => {
                  window.__workspaceWatchdogObserved=false;
                  const observer=setInterval(()=>{
                    if(document.querySelector('#workspaceHydrationStatus[data-workspace-boot-failed="true"]')){
                      window.__workspaceWatchdogObserved=true;
                      clearInterval(observer);
                    }
                  },20);
                })()"""
            )
            delayed_page = delayed_context.new_page()
            delayed_page.set_default_timeout(20_000)

            def delay_tools_module(route: Any) -> None:
                time.sleep(8.75)
                route.continue_()

            delayed_page.route("**/js/tools.js", delay_tools_module)
            attach_observers(delayed_page, result, base_netloc)
            try:
                started = time.perf_counter()
                delayed_page.goto(base, wait_until="domcontentloaded", timeout=20_000)
                wait_ready(delayed_page)
                result.require(delayed_page.evaluate("window.__workspaceWatchdogObserved"), "static watchdog was not exercised")
                result.require(not delayed_page.locator("#main").evaluate("element => element.inert"), "late module completion left the workspace inert")
                result.require(delayed_page.locator("#themeToggle").is_enabled(), "late module completion left the top bar disabled")
                result.require(delayed_page.locator("#cmdkBtn").is_enabled(), "late module completion left the command palette disabled")
                delayed_page.locator('.tab[data-tab="repeater"]').click()
                result.require(
                    delayed_page.locator('.panel[data-panel="repeater"]').get_attribute("class").find("active") >= 0,
                    "late module completion did not restore navigation",
                )
                result.metrics["late_module_recovery_ms"] = round((time.perf_counter() - started) * 1000, 1)
            finally:
                delayed_context.close()

        result.run(
            "late module completion restores the guarded workspace",
            delayed_module_eventually_recovers,
        )

        def pathological_persisted_state_recovery() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            storage_key = browser_project_storage_key("rep.tabs", project_identity)
            tab_count = 5_000
            saved_state = json.dumps(
                {
                    "seq": tab_count + 1,
                    "active": 1,
                    "tabs": [
                        {"tid": index, "method": "GET", "url": f"https://example.com/request/{index}"}
                        for index in range(1, tab_count + 1)
                    ],
                },
                separators=(",", ":"),
            )
            state_context = browser.new_context(viewport={"width": 1024, "height": 768})
            state_context.add_init_script(
                f"localStorage.setItem({json.dumps(storage_key)},{json.dumps(saved_state)});"
            )
            state_page = state_context.new_page()
            state_page.set_default_timeout(10_000)
            isolated_writes: List[str] = []

            def isolated_repeater_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"value":{"seq":"not-a-number","active":1,"tabs":[{"tid":1,"method":"POST","url":"https://example.com/server","headers":"","body":""}]}}',
                    )
                    return
                isolated_writes.append(route.request.method)
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            state_page.route("**/api/ui/repeater", isolated_repeater_state)
            attach_observers(state_page, result, base_netloc)
            try:
                started = time.perf_counter()
                state_page.goto(base, wait_until="domcontentloaded")
                wait_ready(state_page)
                elapsed_ms = (time.perf_counter() - started) * 1000
                result.require(elapsed_ms < 4_500, f"pathological local workspace recovery settled too late ({elapsed_ms:.1f}ms)")
                result.require(state_page.locator("#repTabs .rep-tab").count() == 1, "pathological saved tabs were rendered into the workspace")
                result.require(
                    "too many tabs to load safely" in state_page.locator("#workspaceHydrationStatus").inner_text(),
                    "pathological saved state did not expose persistent recovery feedback",
                )
                retained_count = state_page.evaluate(
                    "key => JSON.parse(localStorage.getItem(key)).tabs.length",
                    storage_key,
                )
                result.require(retained_count == tab_count, "pathological browser-local state was overwritten during recovery")
                dismiss = state_page.locator("[data-workspace-warning-dismiss]")
                result.require(dismiss.is_enabled(), "pathological saved-state warning has no Continue action")
                dismiss.click()
                result.require(state_page.locator("#workspaceHydrationStatus").is_hidden(), "saved-state warning cannot be dismissed after review")
                result.require(
                    state_page.evaluate("document.activeElement?.matches('.tab.active')"),
                    "saved-state Continue action did not restore visible workspace focus",
                )
                state_page.locator('.tab[data-tab="repeater"]').click()
                result.require(state_page.locator("#repUrl").input_value() == "https://example.com/server", "safe server state was not restored in memory")
                state_page.locator("#repTabsAdd").click()
                result.require(
                    state_page.locator("#repTabs .rep-tab").last.get_attribute("data-tid") == "2",
                    "invalid persisted sequence broke the next recovered tab id",
                )
                state_page.locator("#repUrl").fill("https://example.com/recovered")
                state_page.wait_for_timeout(650)
                recovered_url = state_page.evaluate(
                    "key => JSON.parse(localStorage.getItem(key)).tabs.find(tab => tab.url === 'https://example.com/recovered')?.url",
                    storage_key,
                )
                result.require(recovered_url == "https://example.com/recovered", "first edit after guarded recovery was not persisted")
                result.require(bool(isolated_writes) and set(isolated_writes) == {"PUT"}, f"recovered edit produced unexpected sync writes: {isolated_writes}")
                result.metrics["pathological_workspace_recovery_ms"] = round(elapsed_ms, 1)
                result.metrics["pathological_workspace_tabs_retained"] = retained_count
            finally:
                state_context.close()

        result.run(
            "pathological browser-local workspace state remains recoverable",
            pathological_persisted_state_recovery,
        )

        def invalid_pending_state_recovery() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            pending_key = browser_project_storage_key("ui.pending.repeater", project_identity)
            pending_context = browser.new_context(viewport={"width": 1024, "height": 768})
            pending_context.add_init_script(f"localStorage.setItem({json.dumps(pending_key)},'{{}}');")
            pending_page = pending_context.new_page()
            pending_page.set_default_timeout(10_000)
            unexpected_writes: List[str] = []

            def valid_server_repeater_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"value":{"seq":2,"active":1,"tabs":[{"tid":1,"method":"GET","url":"https://example.com/server-preserved","headers":"","body":""}]}}',
                    )
                    return
                unexpected_writes.append(route.request.method)
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            pending_page.route("**/api/ui/repeater", valid_server_repeater_state)
            attach_observers(pending_page, result, base_netloc)
            try:
                pending_page.goto(base, wait_until="domcontentloaded")
                wait_ready(pending_page)
                status = pending_page.locator("#workspaceHydrationStatus")
                result.require("pending state is not recognized" in status.inner_text(), "invalid pending state has no persistent explanation")
                result.require(status.locator("[data-workspace-warning-dismiss]").is_enabled(), "invalid pending-state warning has no Continue action")
                result.require(pending_page.evaluate("key => localStorage.getItem(key)", pending_key) == "{}", "invalid pending state was overwritten")
                pending_page.locator('.tab[data-tab="repeater"]').click()
                result.require(
                    pending_page.locator("#repUrl").input_value() == "https://example.com/server-preserved",
                    "invalid pending state blocked a valid server workspace",
                )
                result.require(not unexpected_writes, f"invalid pending state triggered server writes: {unexpected_writes}")
            finally:
                pending_context.close()

        result.run(
            "invalid pending workspace state is preserved",
            invalid_pending_state_recovery,
        )

        def oversized_pending_state_is_not_retried() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            pending_key = browser_project_storage_key("ui.pending.repeater", project_identity)
            oversized_state = json.dumps(
                {
                    "seq": 2,
                    "active": 1,
                    "tabs": [{"tid": 1, "method": "POST", "url": "https://example.com/oversized", "body": "x" * (4 * 1024 * 1024)}],
                },
                separators=(",", ":"),
            )
            pending_context = browser.new_context(viewport={"width": 1024, "height": 768})
            pending_context.add_init_script(
                f"localStorage.setItem({json.dumps(pending_key)},{json.dumps(oversized_state)});"
            )
            pending_page = pending_context.new_page()
            pending_page.set_default_timeout(10_000)
            unexpected_writes: List[str] = []

            def valid_server_repeater_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"value":{"seq":2,"active":1,"tabs":[{"tid":1,"method":"GET","url":"https://example.com/server-after-oversized","headers":"","body":""}]}}',
                    )
                    return
                unexpected_writes.append(route.request.method)
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            pending_page.route("**/api/ui/repeater", valid_server_repeater_state)
            attach_observers(pending_page, result, base_netloc)
            try:
                pending_page.goto(base, wait_until="domcontentloaded")
                wait_ready(pending_page)
                result.require(
                    "too large to restore safely" in pending_page.locator("#workspaceHydrationStatus").inner_text(),
                    "oversized pending state has no persistent recovery explanation",
                )
                result.require(
                    pending_page.evaluate("key => localStorage.getItem(key)?.length", pending_key) == len(oversized_state),
                    "oversized pending state was not retained exactly",
                )
                pending_page.locator('.tab[data-tab="repeater"]').click()
                result.require(
                    pending_page.locator("#repUrl").input_value() == "https://example.com/server-after-oversized",
                    "oversized pending state blocked a valid server workspace",
                )
                pending_page.wait_for_timeout(500)
                result.require(not unexpected_writes, f"oversized pending state entered a guaranteed-failure retry loop: {unexpected_writes}")
            finally:
                pending_context.close()

        result.run(
            "oversized pending workspace state is retained without retry",
            oversized_pending_state_is_not_retried,
        )

        def valid_pending_replaces_invalid_server_state() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            pending_key = browser_project_storage_key("ui.pending.repeater", project_identity)
            pending_state = json.dumps(
                {
                    "seq": 2,
                    "active": 1,
                    "tabs": [{"tid": 1, "method": "PATCH", "url": "https://example.com/pending", "headers": "", "body": ""}],
                },
                separators=(",", ":"),
            )
            pending_context = browser.new_context(viewport={"width": 1024, "height": 768})
            pending_context.add_init_script(
                f"localStorage.setItem({json.dumps(pending_key)},{json.dumps(pending_state)});"
            )
            pending_page = pending_context.new_page()
            pending_page.set_default_timeout(10_000)
            replacement_writes: List[str] = []

            def invalid_server_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(status=200, content_type="application/json", body='{"value":{}}')
                    return
                replacement_writes.append(route.request.post_data or "")
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            pending_page.route("**/api/ui/repeater", invalid_server_state)
            attach_observers(pending_page, result, base_netloc)
            try:
                pending_page.goto(base, wait_until="domcontentloaded")
                wait_ready(pending_page)
                pending_page.wait_for_function("key => localStorage.getItem(key) === null", arg=pending_key)
                pending_page.locator('.tab[data-tab="repeater"]').click()
                result.require(pending_page.locator("#repUrl").input_value() == "https://example.com/pending", "valid pending state did not win over invalid server state")
                result.require(
                    bool(replacement_writes) and all("https://example.com/pending" in body for body in replacement_writes),
                    "valid pending state was not synchronized as the authoritative replacement",
                )
            finally:
                pending_context.close()

        result.run(
            "valid pending workspace state replaces invalid server state",
            valid_pending_replaces_invalid_server_state,
        )

        def browser_storage_failure_keeps_project_sync() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            preset_key = browser_project_storage_key("intruder.presets", project_identity)
            stale_presets = json.dumps([{"name": "Stale preset", "target": "https://example.com/stale"}], separators=(",", ":"))
            storage_context = browser.new_context(viewport={"width": 1024, "height": 768})
            storage_context.add_init_script(
                """(() => {
                  const nativeSetItem=Storage.prototype.setItem;
                  nativeSetItem.call(localStorage,"""
                + json.dumps(preset_key)
                + ","
                + json.dumps(stale_presets)
                + """ );
                  window.__storageWriteFailures=0;
                  Storage.prototype.setItem=function(){
                    window.__storageWriteFailures++;
                    throw new DOMException('injected unavailable storage','QuotaExceededError');
                  };
                })()"""
            )
            storage_page = storage_context.new_page()
            storage_page.set_default_timeout(10_000)
            project_writes: List[str] = []
            preset_writes: List[str] = []

            def isolated_storage_failure_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"value":{"seq":2,"active":1,"tabs":[{"tid":1,"method":"GET","url":"https://example.com/server-backed","headers":"","body":""}]}}',
                    )
                    return
                project_writes.append(route.request.post_data or "")
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            storage_page.route("**/api/ui/repeater", isolated_storage_failure_state)

            def isolated_preset_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"value":[{"name":"Server preset","target":"https://example.com/server"}]}',
                    )
                    return
                preset_writes.append(route.request.post_data or "")
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            storage_page.route("**/api/ui/intruder-presets", isolated_preset_state)
            attach_observers(storage_page, result, base_netloc)
            try:
                storage_page.goto(base, wait_until="domcontentloaded")
                wait_ready(storage_page)
                result.require(
                    "could not be copied into browser storage" in storage_page.locator("#workspaceHydrationStatus").inner_text(),
                    "browser-storage failure did not expose persistent recovery feedback",
                )
                storage_page.locator('.tab[data-tab="repeater"]').click()
                result.require(storage_page.locator("#repUrl").input_value() == "https://example.com/server-backed", "browser-storage failure replaced valid server state with a blank tab")
                storage_page.locator("#repUrl").fill("https://example.com/project-backed")
                storage_page.wait_for_timeout(700)
                result.require(storage_page.evaluate("window.__storageWriteFailures") > 0, "browser-storage fault was not exercised")
                result.require(
                    any("https://example.com/project-backed" in body for body in project_writes),
                    "browser-storage failure suppressed the project workspace write",
                )
                storage_page.locator('.tab[data-tab="intruder"]').click()
                preset_options = storage_page.locator("#intrPreset option").all_text_contents()
                result.require("Server preset" in preset_options and "Stale preset" not in preset_options, "in-memory server preset lost to stale browser storage")
                storage_page.locator("#intrPresetSave").click()
                storage_page.locator("#promptInput").fill("Session preset")
                storage_page.locator("#promptOk").click()
                storage_page.wait_for_function(
                    "document.querySelector('#intrPreset')?.textContent.includes('Session preset')"
                )
                result.require(
                    "Session preset" in storage_page.locator("#intrPreset").inner_text(),
                    "failed browser storage discarded the newly built preset",
                )
                result.require(
                    any("Session preset" in body for body in preset_writes),
                    "failed browser storage suppressed the new preset project write",
                )
                result.require(
                    "preset kept in this session" in storage_page.locator("#toast").inner_text(),
                    "preset save feedback falsely claimed browser persistence",
                )
            finally:
                storage_context.close()

        result.run(
            "browser storage failure keeps project workspace sync",
            browser_storage_failure_keeps_project_sync,
        )

        def persisted_tab_identity_and_creation_guards() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            storage_key = browser_project_storage_key("rep.tabs", project_identity)
            tab_count = 200
            saved_state = json.dumps(
                {
                    "seq": tab_count + 1,
                    "active": 1,
                    "tabs": [
                        {"tid": index, "method": "GET", "url": f"https://example.com/request/{index}"}
                        for index in range(1, tab_count + 1)
                    ],
                },
                separators=(",", ":"),
            )
            limit_context = browser.new_context(viewport={"width": 1024, "height": 768})
            limit_context.add_init_script(
                f"localStorage.setItem({json.dumps(storage_key)},{json.dumps(saved_state)});"
            )
            limit_page = limit_context.new_page()
            limit_page.set_default_timeout(10_000)

            def empty_limit_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(status=200, content_type="application/json", body='{"value":null}')
                    return
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            limit_page.route("**/api/ui/repeater", empty_limit_state)
            attach_observers(limit_page, result, base_netloc)
            try:
                limit_page.goto(base, wait_until="domcontentloaded")
                wait_ready(limit_page)
                validation = limit_page.evaluate(
                    """async () => {
                      const {isSafePersistedTabState}=await import('/js/core.js');
                      return {
                        unique:isSafePersistedTabState({tabs:[{tid:1},{tid:2}]}),
                        duplicate:isSafePersistedTabState({tabs:[{tid:1},{tid:1}]}),
                        exhausted:isSafePersistedTabState({tabs:[{tid:Number.MAX_SAFE_INTEGER}]})
                      };
                    }"""
                )
                result.require(validation == {"unique": True, "duplicate": False, "exhausted": False}, f"persisted tab ID validation is ambiguous: {validation}")
                limit_page.locator('.tab[data-tab="repeater"]').click()
                result.require(limit_page.locator("#repTabs .rep-tab").count() == tab_count, "safe 200-tab workspace did not load")
                add = limit_page.locator("#repTabsAdd")
                result.require(add.get_attribute("aria-disabled") == "true", "tab creation cap is not exposed semantically")
                add.focus()
                limit_page.keyboard.press("Enter")
                result.require(limit_page.locator("#repTabs .rep-tab").count() == tab_count, "tab creation exceeded the reload-safe cap")
                result.require("supports up to 200 tabs" in limit_page.locator("#toast").inner_text(), "tab creation cap has no direct feedback")
                created = limit_page.evaluate("async () => (await import('/js/tools.js')).repNewTab() !== null")
                result.require(not created, "non-button Repeater creation bypassed the reload-safe cap")
                capped_collection = {
                    "info": {
                        "name": "Capacity audit",
                        "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
                    },
                    "item": [{"name": "Extra", "request": {"method": "GET", "url": "https://example.com/extra"}}],
                }
                limit_page.locator("#repPostmanFile").set_input_files(
                    {
                        "name": "capacity.postman_collection.json",
                        "mimeType": "application/json",
                        "buffer": json.dumps(capped_collection).encode("utf-8"),
                    }
                )
                limit_page.wait_for_function("document.querySelector('#toast')?.textContent.includes('only 0 tab slots available')")
                result.require(limit_page.locator("#repTabs .rep-tab").count() == tab_count, "Postman import bypassed the reload-safe cap")
                limit_page.reload(wait_until="domcontentloaded")
                wait_ready(limit_page)
                limit_page.locator('.tab[data-tab="repeater"]').click()
                result.require(limit_page.locator("#repTabs .rep-tab").count() == tab_count, "capped workspace did not survive reload")
                identity_keys = limit_page.evaluate(
                    """async () => {
                      const core=await import('/js/core.js');
                      core.setStorageProject('team alpha');const spaced=core.projectStorageKey('rep.tabs');
                      core.setStorageProject('team_alpha');const underscored=core.projectStorageKey('rep.tabs');
                      core.setStorageProject('/client-a/shared',[{name:'shared',path:'/client-a/shared'},{name:'shared',path:'/client-b/shared'}],'shared');
                      const externalA=core.projectStorageKey('rep.tabs');
                      core.setStorageProject('/client-b/shared',[{name:'shared',path:'/client-a/shared'},{name:'shared',path:'/client-b/shared'}],'shared');
                      const externalB=core.projectStorageKey('rep.tabs');
                      localStorage.setItem('audit.unambiguous.team_alpha','one');
                      core.setStorageProject('team alpha',['team alpha']);
                      const migratedKey=core.projectStorageKey('audit.unambiguous');
                      const migrated=localStorage.getItem(migratedKey);
                      const removed=localStorage.getItem('audit.unambiguous.team_alpha');
                      localStorage.setItem('audit.ambiguous.team_alpha','two');
                      core.setStorageProject('team alpha',['team alpha','team_alpha']);
                      const ambiguousKey=core.projectStorageKey('audit.ambiguous');
                      const ambiguous={
                        current:localStorage.getItem(ambiguousKey),
                        legacy:localStorage.getItem('audit.ambiguous.team_alpha'),
                        warning:core.consumeStorageMigrationWarning(ambiguousKey)
                      };
                      localStorage.setItem('audit.retry.team_alpha','three');
                      core.setStorageProject('team alpha',['team alpha']);
                      const nativeSetItem=Storage.prototype.setItem;
                      let retryKey,failedWarning;
                      try{
                        Storage.prototype.setItem=function(key,value){
                          if(String(key).includes('audit.retry.v2.'))throw new DOMException('injected migration failure','QuotaExceededError');
                          return nativeSetItem.call(this,key,value);
                        };
                        retryKey=core.projectStorageKey('audit.retry');
                        failedWarning=core.consumeStorageMigrationWarning(retryKey);
                      }finally{Storage.prototype.setItem=nativeSetItem;}
                      const retriedKey=core.projectStorageKey('audit.retry');
                      const retried={
                        sameKey:retryKey===retriedKey,
                        current:localStorage.getItem(retriedKey),
                        legacy:localStorage.getItem('audit.retry.team_alpha'),
                        failedWarning
                      };
                      return {spaced,underscored,externalA,externalB,migrated,removed,ambiguous,retried};
                    }"""
                )
                result.require(identity_keys["spaced"] != identity_keys["underscored"], "distinct project names share browser-local storage")
                result.require(identity_keys["externalA"] != identity_keys["externalB"], "distinct external project directories share browser-local storage")
                result.require(identity_keys["migrated"] == "one" and identity_keys["removed"] is None, "unambiguous legacy project state was not migrated")
                result.require(
                    identity_keys["ambiguous"] == {"current": None, "legacy": "two", "warning": "ambiguous legacy project state"},
                    f"ambiguous legacy project state was not preserved: {identity_keys['ambiguous']}",
                )
                result.require(
                    identity_keys["retried"] == {"sameKey": True, "current": "three", "legacy": None, "failedWarning": "legacy project state could not be migrated"},
                    f"failed legacy migration was not reported and retried: {identity_keys['retried']}",
                )
            finally:
                limit_context.close()

        result.run(
            "persisted tab identity and creation limits remain safe",
            persisted_tab_identity_and_creation_guards,
        )

        def postman_import_preserves_unique_repeater_tabs() -> None:
            import_context = browser.new_context(viewport={"width": 1024, "height": 768})
            import_page = import_context.new_page()
            import_page.set_default_timeout(10_000)

            def empty_import_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(status=200, content_type="application/json", body='{"value":null}')
                    return
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            import_page.route("**/api/ui/repeater", empty_import_state)
            attach_observers(import_page, result, base_netloc)
            collection = {
                "info": {
                    "name": "Browser audit",
                    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
                },
                "item": [
                    {"name": "Read", "request": {"method": "GET", "url": "https://example.com/read"}},
                    {"name": "Update", "request": {"method": "PATCH", "url": "https://example.com/update"}},
                ],
            }
            try:
                import_page.goto(base, wait_until="domcontentloaded")
                wait_ready(import_page)
                import_page.locator('.tab[data-tab="repeater"]').click()
                initial_count = import_page.locator("#repTabs .rep-tab").count()
                import_page.locator("#repPostmanFile").set_input_files(
                    {
                        "name": "browser-audit.postman_collection.json",
                        "mimeType": "application/json",
                        "buffer": json.dumps(collection).encode("utf-8"),
                    }
                )
                import_page.wait_for_function(
                    "count => document.querySelectorAll('#repTabs .rep-tab').length === count + 2",
                    arg=initial_count,
                )
                tab_state = import_page.evaluate(
                    """async () => {
                      const {repTabs}=await import('/js/tools.js');
                      return {ids:repTabs.tabs.map(tab=>tab.tid),urls:repTabs.tabs.map(tab=>tab.url)};
                    }"""
                )
                result.require(len(tab_state["ids"]) == initial_count + 2, "Postman import created an unexpected number of tabs")
                result.require(len(tab_state["ids"]) == len(set(tab_state["ids"])), "Postman import created duplicate tab IDs")
                result.require(
                    "https://example.com/read" in tab_state["urls"] and "https://example.com/update" in tab_state["urls"],
                    "Postman import did not preserve both request targets",
                )
            finally:
                import_context.close()

        result.run(
            "Postman import creates unique reload-safe Repeater tabs",
            postman_import_preserves_unique_repeater_tabs,
        )

        def malformed_tab_fields_and_history_keys_recover() -> None:
            _, project_info, _ = _json_get(base, "/api/project")
            project_identity = str(project_info.get("dir") or "")
            rep_key = browser_project_storage_key("rep.tabs", project_identity)
            intr_key = browser_project_storage_key("intr.tabs", project_identity)
            preset_key = browser_project_storage_key("intruder.presets", project_identity)
            rep_state = json.dumps(
                {
                    "seq": 3,
                    "active": 1,
                    "tabs": [
                        {"tid": 1, "method": {}, "url": {}, "headers": [], "body": 7, "historyKey": "shared-history"},
                        {"tid": 2, "method": "GET", "url": "https://example.com/valid", "historyKey": "shared-history"},
                    ],
                },
                separators=(",", ":"),
            )
            intr_state = json.dumps(
                {"seq": 2, "active": 1, "tabs": [{"tid": 1, "target": {}, "template": [], "type": {}, "threads": "many"}]},
                separators=(",", ":"),
            )
            preset_state = json.dumps(
                [None, {"name": {}, "target": {}, "template": [], "type": {}, "threads": "many", "pos": "invalid"}, "invalid"],
                separators=(",", ":"),
            )
            malformed_context = browser.new_context(viewport={"width": 1024, "height": 768})
            malformed_context.add_init_script(
                f"localStorage.setItem({json.dumps(rep_key)},{json.dumps(rep_state)});"
                f"localStorage.setItem({json.dumps(intr_key)},{json.dumps(intr_state)});"
                f"localStorage.setItem({json.dumps(preset_key)},{json.dumps(preset_state)});"
            )
            malformed_page = malformed_context.new_page()
            malformed_page.set_default_timeout(10_000)

            def empty_malformed_state(route: Any) -> None:
                if route.request.method == "GET":
                    route.fulfill(status=200, content_type="application/json", body='{"value":null}')
                    return
                route.fulfill(status=200, content_type="application/json", body='{"ok":true}')

            malformed_page.route("**/api/ui/repeater", empty_malformed_state)
            malformed_page.route("**/api/ui/intruder", empty_malformed_state)
            malformed_page.route("**/api/ui/intruder-presets", empty_malformed_state)
            attach_observers(malformed_page, result, base_netloc)
            try:
                malformed_page.goto(base, wait_until="domcontentloaded")
                wait_ready(malformed_page)
                malformed_page.locator('.tab[data-tab="repeater"]').click()
                result.require(malformed_page.locator("#repUrl").input_value() == "", "malformed Repeater URL was not safely coerced")
                result.require(malformed_page.locator("#repMethod").input_value() == "GET", "malformed Repeater method was not safely coerced")
                rep_tabs = malformed_page.evaluate(
                    """async () => {
                      const {repTabs}=await import('/js/tools.js');
                      return repTabs.tabs.map(tab=>({tid:tab.tid,urlType:typeof tab.url,historyKey:tab.historyKey}));
                    }"""
                )
                history_keys = [tab["historyKey"] for tab in rep_tabs]
                result.require(len(rep_tabs) == 2 and all(tab["urlType"] == "string" for tab in rep_tabs), "malformed Repeater tabs did not normalize")
                result.require(len(history_keys) == len(set(history_keys)), "duplicate Repeater history identities survived normalization")
                malformed_page.locator('.tab[data-tab="intruder"]').click()
                result.require(malformed_page.locator("#intrTarget").input_value() == "", "malformed Intruder target was not safely coerced")
                result.require(malformed_page.locator("#intrThreads").input_value() == "1", "malformed Intruder thread count was not bounded")
                preset_options = malformed_page.locator("#intrPreset option").all_text_contents()
                result.require(preset_options == ["presets…", "preset 0"], f"malformed Intruder presets were not filtered and normalized: {preset_options}")
                malformed_page.evaluate(
                    """() => {
                      const preset=document.querySelector('#intrPreset');
                      preset.value='0';
                      preset.dispatchEvent(new Event('change',{bubbles:true}));
                    }"""
                )
                result.require(malformed_page.locator("#intrTarget").input_value() == "", "malformed Intruder preset target reached the editor")
                result.require(malformed_page.locator("#intrThreads").input_value() == "1", "malformed Intruder preset limits reached the editor")
            finally:
                malformed_context.close()

        result.run(
            "malformed tab and preset fields recover with isolated Repeater history",
            malformed_tab_fields_and_history_keys_recover,
        )

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
            nonlocal preflight
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

            page.locator('#setNav button[data-sec="proxy"]').click()
            toggle = page.locator("#suppressTelemetryToggle")
            initial_pressed = toggle.get_attribute("aria-pressed")
            toggle.focus()
            result.require(
                page.evaluate("document.activeElement?.id") == "suppressTelemetryToggle",
                "browser-background toggle is not keyboard-focusable",
            )
            if args.full:
                # The earlier navigation checks are read-only. Revalidate the
                # disposable instance immediately before the first mutation.
                full_audit_preflight(base, args.proxy, args.expected_project, args.expected_data_dir)
                preflight["revalidated_before_settings"] = True
                toggle.press("Enter")
                page.wait_for_function(
                    "initial => document.querySelector('#suppressTelemetryToggle')?.getAttribute('aria-pressed') !== initial",
                    arg=initial_pressed,
                )
                result.require(
                    page.evaluate("document.activeElement?.id") == "suppressTelemetryToggle",
                    "browser-background toggle lost keyboard focus after acknowledgement",
                )
                toggle.press("Enter")
                page.wait_for_function(
                    "initial => document.querySelector('#suppressTelemetryToggle')?.getAttribute('aria-pressed') === initial",
                    arg=initial_pressed,
                )

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
            proxy = args.proxy
            # Re-check immediately before entering the mutation-heavy block.
            full_audit_preflight(base, proxy, args.expected_project, args.expected_data_dir)
            preflight["revalidated_before_full_audit"] = True
            fixture, _fixture_thread = start_fixture()
            fixture_base = f"http://127.0.0.1:{fixture.server_port}"

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

            def codec_latest_load_ownership() -> None:
                codec_context = browser.new_context(viewport={"width": 1024, "height": 768})
                codec_page = codec_context.new_page()
                codec_page.set_default_timeout(10_000)
                attach_observers(codec_page, result, base_netloc)
                list_routes: List[Any] = []
                docs_routes: List[Any] = []

                def hold_codec_list(route: Any) -> None:
                    list_routes.append(route)

                def hold_codec_docs(route: Any) -> None:
                    docs_routes.append(route)

                codec_page.route("**/api/codecs", hold_codec_list)
                codec_page.route("**/api/codecs/reference", hold_codec_docs)
                try:
                    codec_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(codec_page)
                    codec_page.locator('.tab[data-tab="scanner"]').click()
                    codec_page.locator("#codecsBtn").click()
                    codec_page.wait_for_selector("#codecsModal", state="visible", timeout=10_000)
                    deadline = time.monotonic() + 2.0
                    while len(list_routes) < 1 and time.monotonic() < deadline:
                        codec_page.wait_for_timeout(10)
                    result.require(len(list_routes) == 1, "opening Codecs did not issue its initial list request")

                    codec_page.locator("#codecModeDocs").click()
                    deadline = time.monotonic() + 2.0
                    while len(docs_routes) < 1 and time.monotonic() < deadline:
                        codec_page.wait_for_timeout(10)
                    result.require(len(docs_routes) == 1, "opening codec Docs did not issue its initial request")
                    codec_page.locator("#codecModeCode").click()
                    codec_page.locator("#codecModeDocs").click()
                    deadline = time.monotonic() + 2.0
                    while len(docs_routes) < 2 and time.monotonic() < deadline:
                        codec_page.wait_for_timeout(10)
                    result.require(len(docs_routes) == 2, "reopening codec Docs did not issue a latest-owner request")
                    docs_routes[1].fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"markdown":"# Latest codec reference"}',
                    )
                    codec_page.wait_for_function(
                        "document.querySelector('#codecDocs')?.textContent.includes('Latest codec reference')"
                    )
                    docs_routes[0].fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"markdown":"# Stale codec reference"}',
                    )
                    codec_page.wait_for_timeout(120)
                    result.require(
                        "Latest codec reference" in codec_page.locator("#codecDocs").inner_text()
                        and "Stale codec reference" not in codec_page.locator("#codecDocs").inner_text(),
                        "an older codec Docs response replaced the latest reference",
                    )

                    codec_page.locator("#codecNew").click()
                    deadline = time.monotonic() + 2.0
                    while len(list_routes) < 2 and time.monotonic() < deadline:
                        codec_page.wait_for_timeout(10)
                    result.require(len(list_routes) == 2, "New codec did not issue a latest-owner list request")
                    list_routes[1].fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"codecs":[{"id":"latest-codec","meta":{"title":"Latest codec"}}],"dir":"/tmp/ui-audit/codecs"}',
                    )
                    codec_page.wait_for_selector('#codecsList .codecs-row[data-id="latest-codec"]')
                    list_routes[0].fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"codecs":[{"id":"stale-codec","meta":{"title":"Stale codec"}}],"dir":"/tmp/ui-audit/codecs"}',
                    )
                    codec_page.wait_for_timeout(120)
                    result.require(
                        codec_page.locator('#codecsList .codecs-row[data-id="latest-codec"]').count() == 1
                        and codec_page.locator('#codecsList .codecs-row[data-id="stale-codec"]').count() == 0,
                        "an older codec list response replaced the latest list",
                    )
                finally:
                    for route in list_routes + docs_routes:
                        try:
                            route.fulfill(status=200, content_type="application/json", body='{"codecs":[],"markdown":""}')
                        except Exception:
                            pass
                    try:
                        codec_page.unroute("**/api/codecs", hold_codec_list)
                        codec_page.unroute("**/api/codecs/reference", hold_codec_docs)
                    except Exception:
                        pass
                    codec_context.close()

            result.run("Codecs Docs and list loads retain their latest owners", codec_latest_load_ownership)

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
                status, body = proxy_request(proxy, fixture_base + "/audit/seed?host=example")
                result.require(status == 200, f"seed request failed: {bounded_response_diagnostic(status, body)}")
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
                _, project_info, _ = _json_get(base, "/api/project")
                legacy_project = re.sub(r"[^A-Za-z0-9._-]+", "_", str(project_info.get("current") or "default")) or "default"
                legacy_rows = page.evaluate(
                    """async legacyPrefix => {
                      const tools=await import('/js/tools.js'),core=await import('/js/core.js');
                      const tab=tools.repTabs.cur();
                      const currentTabKey=core.projectStorageKey('rep.history')+'|'+tab.historyKey;
                      const legacyTabKey=legacyPrefix+'|'+tab.historyKey;
                      return await new Promise((resolve,reject)=>{
                        const open=indexedDB.open('interseptor-repeater-history',1);
                        open.onerror=()=>reject(open.error);
                        open.onsuccess=()=>{
                          const tx=open.result.transaction('entries','readwrite');
                          const store=tx.objectStore('entries'),index=store.index('tabKey');
                          const rows=index.getAll(currentTabKey);
                          rows.onsuccess=()=>{
                            for(const row of rows.result||[]){
                              store.put({key:legacyTabKey+'|'+row.entry.id,tabKey:legacyTabKey,entry:row.entry});
                              store.delete(row.key);
                            }
                          };
                          tx.oncomplete=()=>resolve((rows.result||[]).length);
                          tx.onerror=()=>reject(tx.error);
                          tx.onabort=()=>reject(tx.error);
                        };
                      });
                    }""",
                    f"rep.history.{legacy_project}",
                )
                result.require(legacy_rows == history_count, "legacy Repeater history migration fixture did not move the current rows")

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
                migrated_counts = page.evaluate(
                    """async legacyPrefix => {
                      const tools=await import('/js/tools.js'),core=await import('/js/core.js');
                      const tab=tools.repTabs.cur();
                      const keys=[core.projectStorageKey('rep.history')+'|'+tab.historyKey,legacyPrefix+'|'+tab.historyKey];
                      return await new Promise((resolve,reject)=>{
                        const open=indexedDB.open('interseptor-repeater-history',1);
                        open.onerror=()=>reject(open.error);
                        open.onsuccess=()=>{
                          const tx=open.result.transaction('entries','readonly'),index=tx.objectStore('entries').index('tabKey');
                          const counts=keys.map(key=>index.count(key));
                          tx.oncomplete=()=>resolve(counts.map(request=>request.result));
                          tx.onerror=()=>reject(tx.error);
                        };
                      });
                    }""",
                    f"rep.history.{legacy_project}",
                )
                result.require(migrated_counts == [history_count, 0], f"legacy Repeater history rows were not migrated atomically: {migrated_counts}")

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

                # Exercise the evidence-first contract through the same controls
                # an operator (or an AI driving the UI) uses.  Keep the fixture
                # generic and deterministic: no real target data or external
                # requests are involved.
                page.wait_for_selector("#findSummary")
                page.locator("#findSummary").fill(
                    "An unauthorised user can read a second generic loopback record."
                )
                page.locator("#findImpact").fill("A record outside the user's scope is disclosed.")
                page.locator("#findWhy").fill("The object-level access check is missing.")
                page.locator("#findTarget").fill(f"{fixture_base}/audit/seed")
                page.locator("#findFix").fill("Enforce authorization before returning the requested record.")
                page.locator("#findRetest").fill("The same request returns 403 and no record data.")
                page.locator("#findConfidence").select_option("firm")
                page.locator("#findBody").click(position={"x": 4, "y": 4})
                page.wait_for_function(
                    """async () => {
                      const list=await (await fetch('/api/findings')).json();
                      const f=(list.findings||[]).find(x=>x.title==='Generic UI audit finding');
                      return f?.summary?.includes('unauthorised') && f?.impact?.includes('disclosed')
                        && f?.why?.includes('missing') && f?.target?.includes('/audit/seed')
                        && f?.fix?.includes('Enforce') && f?.retest?.includes('403')
                        && f?.confidence==='firm';
                    }""",
                    timeout=10_000,
                )

                page.locator("#findNarrativePreset").select_option("Differential proof")
                page.locator("#findApplyPreset").click()
                steps = page.locator("#findBody .block-text")
                result.require(steps.count() == 3, f"differential outline created {steps.count()} steps")
                step_text = (
                    "The baseline identity can read only its own record.",
                    "Request the neighbouring record identifier without changing identity.",
                    "The server returns the neighbouring record, proving the missing check.",
                )
                for index, text in enumerate(step_text):
                    steps.nth(index).fill(text)

                # Paste a generic 1x1 PNG while the final step still owns focus.
                # This proves evidence attachment captures in-progress text
                # before the authoritative response replaces the editor DOM.
                result.require(
                    page.evaluate("document.activeElement?.matches('#findBody .block-text')") is True,
                    "final reproduction step did not retain focus before screenshot paste",
                )
                png_hex = (
                    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
                    "0000000d49444154789c6360f8cf00000003000101c9fe2a0000000049454e44ae426082"
                )
                page.evaluate(
                    """hex => {
                      const bytes = new Uint8Array(hex.match(/../g).map(v => parseInt(v, 16)));
                      const file = new File([bytes], 'ui-audit-evidence.png', {type:'image/png'});
                      const transfer = new DataTransfer();
                      transfer.items.add(file);
                      document.activeElement.dispatchEvent(new ClipboardEvent('paste', {
                        bubbles:true, cancelable:true, clipboardData:transfer,
                      }));
                    }""",
                    png_hex,
                )
                page.wait_for_selector("#findBody .find-block-image, #findBody .find-doc-image", timeout=10_000)
                page.wait_for_function(
                    """async () => {
                      const list=await (await fetch('/api/findings')).json();
                      const f=(list.findings||[]).find(x=>x.title==='Generic UI audit finding');
                      const text=(f?.blocks||[]).filter(b=>b.type==='text');
                      return text.length===3
                        && text.every((b,i)=>b.role===['baseline','action','result'][i] && b.md)
                        && (f?.blocks||[]).some(b=>b.type==='image' && b.source==='operator_upload');
                    }""",
                    timeout=10_000,
                )

                # Attach the captured loopback flow via the picker, proving the
                # flow reference remains tied to its sourceFlowId/provenance.
                page.locator("#findAddFlow").click()
                page.wait_for_selector("#findFlowPickModal", state="visible", timeout=10_000)
                page.wait_for_selector("#findFlowPickList .find-flow-pick", timeout=10_000)
                first_flow_pick = page.locator("#findFlowPickList .find-flow-pick").first
                first_flow_pick.locator(".p").click()
                result.require(first_flow_pick.locator("input").is_checked(), "flow-picker row text did not select exactly once")
                result.require(page.locator("#ffpAttach").is_enabled(), "flow-picker attach action did not enable")
                page.locator("#ffpAttach").click()
                page.wait_for_selector("#findFlowPickModal", state="hidden", timeout=10_000)
                page.wait_for_function(
                    """async () => {
                      const list=await (await fetch('/api/findings')).json();
                      const f=(list.findings||[]).find(x=>x.title==='Generic UI audit finding');
                      return (f?.blocks||[]).some(b=>b.type==='flow' && b.source==='captured_flow'
                        && Number(b.sourceFlowId)>0 && Number(b.flowId)>0);
                    }""",
                    timeout=10_000,
                )
                page.wait_for_selector("#findBody .find-doc-flow", timeout=10_000)
                # Evidence is not report-ready until each visual/traffic
                # artifact states the exact claim it supports. Fill both
                # annotations through the editor, then leave Edit so the
                # normal Done/flush path is covered too.
                page.locator("#findBody .find-doc-image .find-block-proof").fill(
                    "Confirms the observed record disclosure in the UI."
                )
                page.locator("#findBody .find-doc-flow .find-block-proof").fill(
                    "Confirms the request and response returned the other record."
                )
                result.require(
                    page.locator("#findBody .find-block-proof").count() >= 2,
                    "evidence blocks did not expose proof annotations",
                )
                page.locator("#findToggleEdit").click()
                page.wait_for_selector("#findSummary", state="hidden", timeout=10_000)
                evidence = page.evaluate(
                    """async () => {
                      const list=await (await fetch('/api/findings')).json();
                      const f=(list.findings||[]).find(x=>x.title==='Generic UI audit finding');
                      return {f, text:document.querySelector('#findDetail')?.innerText||''};
                    }"""
                )
                finding = evidence["f"] or {}
                blocks = finding.get("blocks") or []
                result.require(
                    any(b.get("type") == "image" and b.get("source") == "operator_upload" for b in blocks),
                    "uploaded screenshot did not retain operator_upload provenance",
                )
                result.require(
                    any(
                        b.get("type") == "flow"
                        and b.get("source") == "captured_flow"
                        and b.get("sourceFlowId") == b.get("flowId")
                        for b in blocks
                    ),
                    "attached flow did not retain captured_flow/sourceFlowId provenance",
                )
                result.require(
                    finding.get("readiness", {}).get("stage") == "report_ready",
                    "complete evidence-first finding did not become report ready",
                )
                result.require("operator_upload" in evidence["text"] and "captured_flow" in evidence["text"], "finding view hid evidence provenance")

                # Verify all supported report handoffs produce a browser download.
                # Headless Chromium exposes the native File System Access API,
                # but Playwright cannot accept its operating-system save sheet.
                # Force the product's documented anchor-download fallback so
                # the real UI fetch/blob/filename path remains observable.
                page.evaluate("Object.defineProperty(window,'showSaveFilePicker',{value:undefined,configurable:true})")
                for fmt in ("md", "html", "json"):
                    page.wait_for_function("!document.querySelector('#findExport')?.disabled")
                    page.locator("#findExportFmt").select_option(fmt)
                    with page.expect_download(timeout=10_000) as download_info:
                        page.locator("#findExport").click()
                    download = download_info.value
                    result.require(download.suggested_filename.endswith("." + fmt), f"{fmt} export filename was not reported")
                    page.wait_for_function("!document.querySelector('#findExport')?.disabled")

                # At 1024px the report toolbar must remain distinct from the
                # findings content, and the mobile listbox must retain its
                # announced vertical orientation.
                page.set_viewport_size({"width": 1024, "height": 768})
                try:
                    page.locator('.tab[data-tab="findings"]').click()
                    result.require(
                        page.evaluate(
                            """() => {
                              const toolbar=document.querySelector('.findings-toolbar')?.getBoundingClientRect();
                              const content=document.querySelector('#scanFindingsView')?.getBoundingClientRect();
                              return toolbar && content && toolbar.bottom <= content.top + 1;
                            }"""
                        ),
                        "1024px Findings toolbar overlaps report content",
                    )
                    result.require(
                        page.evaluate(
                            """() => {
                              const exportBox=document.querySelector('#findExport')?.getBoundingClientRect();
                              const groupBox=document.querySelector('#findExportGroupByTag')?.closest('label')?.getBoundingClientRect();
                              if(!exportBox||!groupBox)return false;
                              return exportBox.right<=groupBox.left || groupBox.right<=exportBox.left
                                || exportBox.bottom<=groupBox.top || groupBox.bottom<=exportBox.top;
                            }"""
                        ),
                        "1024px Findings Export and Group-by controls overlap",
                    )
                    result.require(
                        page.evaluate(
                            """() => {
                              const title=document.querySelector('#findTitleText');
                              if(!title)return false;
                              const style=getComputedStyle(title);
                              const text=title.textContent.trim();
                              const line=parseFloat(style.lineHeight)||20;
                              return title.getBoundingClientRect().width>=Math.min(180, text.length*5)
                                && title.getBoundingClientRect().height<=line*2.5
                                && style.wordBreak!=='break-all' && style.overflowWrap!=='anywhere';
                            }"""
                        ),
                        "1024px compact Finding title was reduced to an unusable narrow row",
                    )
                    page.set_viewport_size({"width": 390, "height": 844})
                    page.wait_for_function(
                        "document.querySelector('#tabs')?.getAttribute('aria-orientation')==='horizontal'"
                    )
                    result.require(
                        page.locator("#findList").get_attribute("aria-orientation") == "vertical",
                        "mobile Findings listbox lost its vertical orientation",
                    )
                finally:
                    # A failed responsive assertion must not cascade into later
                    # desktop-only journeys by leaving the shared page narrow.
                    page.set_viewport_size({"width": 1440, "height": 900})
                guide = page.locator("#findGuide")
                guide.click()
                page.keyboard.press("Escape")
                result.require(page.evaluate("document.activeElement?.id") == "findGuide", "Finding guide did not restore focus")

            result.run("Notes save/preview and Findings evidence workflow", notes_and_findings)

            def mutation_modals_lock_dismissal() -> None:
                def wait_for_route(routes: List[Any], label: str, count: int = 1) -> None:
                    deadline = time.monotonic() + 2.0
                    while len(routes) < count and time.monotonic() < deadline:
                        page.wait_for_timeout(10)
                    result.require(len(routes) >= count, f"{label} request was not issued")

                case_http_start = len(result.http_errors)
                expected_rejections: List[str] = []

                def reject_expected(route: Any, label: str) -> None:
                    item = f"409 {route.request.method} {route.request.url}"
                    result.expected_console_request_urls.append(route.request.url)
                    route.fulfill(
                        status=409,
                        content_type="application/json",
                        body=json.dumps({"error": f"{label} audit rejected"}),
                    )
                    expected_rejections.append(item)

                def absorb_expected_rejections() -> None:
                    for item in list(expected_rejections):
                        for index in range(len(result.http_errors) - 1, case_http_start - 1, -1):
                            if result.http_errors[index] != item:
                                continue
                            result.expected_http_errors.append(item)
                            del result.http_errors[index]
                            expected_rejections.remove(item)
                            break
                    result.require(not expected_rejections, "an injected modal rejection was not classified as expected")

                finding_title = "UI audit held finding"
                held_findings: List[Any] = []

                def hold_finding_create(route: Any) -> None:
                    if route.request.method == "POST":
                        held_findings.append(route)
                    else:
                        route.continue_()

                page.route("**/api/findings", hold_finding_create)
                try:
                    page.locator('.tab[data-tab="findings"]').click()
                    page.locator("#findNew").click()
                    page.locator("#fcTitle").fill(finding_title)
                    page.locator("#fcSave").click()
                    wait_for_route(held_findings, "held finding create")
                    result.require(
                        page.locator("#fcTitle").is_disabled()
                        and page.locator("#fcSeverity").is_disabled()
                        and page.locator("#fcSave").is_disabled()
                        and page.locator("#fcClose").is_disabled(),
                        "finding controls did not lock while creation was pending",
                    )
                    page.keyboard.press("Escape")
                    page.locator("#findCreateModal").click(position={"x": 5, "y": 5})
                    page.wait_for_timeout(80)
                    result.require(page.locator("#findCreateModal").is_visible(), "finding creation was dismissible before acknowledgement")
                    result.require(page.evaluate("document.activeElement?.id") == "fcStatus", "finding pending state lost modal focus")
                    reject_expected(held_findings[0], "finding create")
                    page.wait_for_function(
                        "document.querySelector('#fcStatus')?.getAttribute('role')==='alert'"
                    )
                    absorb_expected_rejections()
                    result.require(page.evaluate("document.activeElement?.id") == "fcSave", "finding failure did not restore Create focus")
                    page.locator("#fcSave").click()
                    wait_for_route(held_findings, "retried finding create", 2)
                    result.require(page.evaluate("document.activeElement?.id") == "fcStatus", "finding retry did not focus its pending status")
                    held_findings[1].continue_()
                    page.wait_for_selector("#findCreateModal", state="hidden", timeout=10_000)
                    page.wait_for_function(
                        "title => document.querySelector('#findDetail')?.textContent.includes(title)",
                        arg=finding_title,
                    )
                    result.require(page.evaluate("document.activeElement?.id") == "findNew", "finding acknowledgement did not restore invoker focus")
                finally:
                    for route in held_findings:
                        try:
                            route.continue_()
                        except Exception:
                            pass
                    try:
                        page.unroute("**/api/findings", hold_finding_create)
                    except Exception:
                        pass
                    try:
                        cleanup_status = page.evaluate(
                            """async title => {
                              const response=await fetch('/api/findings');
                              const data=await response.json();
                              const finding=(data.findings||[]).find(item=>item.title===title);
                              if(!finding)return 404;
                              return (await fetch('/api/findings/'+finding.id,{method:'DELETE'})).status;
                            }""",
                            finding_title,
                        )
                        if cleanup_status not in (200, 204, 404):
                            raise RuntimeError(f"DELETE returned {cleanup_status}")
                    except Exception as exc:
                        result.failures.append(f"held-finding audit cleanup failed: {type(exc).__name__}: {exc}")

                check_id = "ui-audit-held-check"
                held_checks: List[Any] = []

                def hold_check_save(route: Any) -> None:
                    if route.request.method == "PUT":
                        held_checks.append(route)
                    else:
                        route.continue_()

                page.route(f"**/api/checks/{check_id}", hold_check_save)
                try:
                    page.locator('.tab[data-tab="scanner"]').click()
                    page.locator("#checksBtn").click()
                    page.wait_for_selector("#checksModal", state="visible", timeout=10_000)
                    page.locator("#checkNew").click()
                    page.locator("#checkId").fill(check_id)
                    page.locator("#checkSrc").fill("def check(flow):\n    return []\n")
                    page.locator("#checkSave").click()
                    wait_for_route(held_checks, "held Scanner check save")
                    result.require(
                        page.locator("#checksClose").is_disabled()
                        and page.locator("#checkId").is_disabled()
                        and page.locator("#checkSrc").is_disabled(),
                        "Scanner check controls did not lock while save was pending",
                    )
                    page.keyboard.press("Escape")
                    page.locator("#checksModal").click(position={"x": 5, "y": 5})
                    page.wait_for_timeout(80)
                    result.require(page.locator("#checksModal").is_visible(), "Scanner check save was dismissible before acknowledgement")
                    result.require(page.evaluate("document.activeElement?.id") == "checkOut", "Scanner pending state lost modal focus")
                    reject_expected(held_checks[0], "Scanner check save")
                    page.wait_for_function(
                        "document.querySelector('#checkOut')?.getAttribute('role')==='alert'"
                    )
                    absorb_expected_rejections()
                    result.require(page.evaluate("document.activeElement?.id") == "checkSave", "Scanner failure did not restore Save focus")
                    page.locator("#checkSave").click()
                    wait_for_route(held_checks, "retried Scanner check save", 2)
                    result.require(page.evaluate("document.activeElement?.id") == "checkOut", "Scanner retry did not focus its pending status")
                    held_checks[1].continue_()
                    page.wait_for_function(
                        "document.querySelector('#checkOut')?.textContent.includes('Saved')",
                        timeout=10_000,
                    )
                    result.require(page.locator("#checksClose").is_enabled(), "Scanner close control did not restore after acknowledgement")
                    result.require(page.evaluate("document.activeElement?.id") == "checkSave", "Scanner acknowledgement did not restore Save focus")
                finally:
                    for route in held_checks:
                        try:
                            route.continue_()
                        except Exception:
                            pass
                    try:
                        page.unroute(f"**/api/checks/{check_id}", hold_check_save)
                    except Exception:
                        pass
                    try:
                        cleanup_status = page.evaluate(
                            "async id => (await fetch('/api/checks/'+encodeURIComponent(id),{method:'DELETE'})).status",
                            check_id,
                        )
                        if cleanup_status not in (200, 204, 404):
                            raise RuntimeError(f"DELETE returned {cleanup_status}")
                    except Exception as exc:
                        result.failures.append(f"held-check audit cleanup failed: {type(exc).__name__}: {exc}")
                    if page.locator("#checksModal").is_visible() and page.locator("#checksClose").is_enabled():
                        page.locator("#checksClose").click()

                page.locator('.tab[data-tab="proxy"]').click()
                page.wait_for_function("document.querySelectorAll('#rows .trow').length>0", timeout=10_000)
                original_identities = page.evaluate("async () => (await (await fetch('/api/authz')).json()).identities||[]")
                held_authz: List[Any] = []
                held_authz_runs: List[Any] = []

                def hold_authz_save(route: Any) -> None:
                    if route.request.method == "POST":
                        held_authz.append(route)
                    else:
                        route.continue_()

                def hold_authz_run(route: Any) -> None:
                    if route.request.method == "POST":
                        held_authz_runs.append(route)
                    else:
                        route.continue_()

                first_flow = page.locator("#rows .trow").first.get_attribute("data-id")
                authz_row = page.locator(f'#rows .trow[data-id="{first_flow}"]')
                authz_row.scroll_into_view_if_needed()
                authz_row.click(button="right")
                page.wait_for_selector("#ctxmenu.show", state="visible", timeout=10_000)
                page.locator("#ctxmenu .ctx-item", has_text="Authz test").click()
                page.wait_for_selector("#authzModal", state="visible", timeout=10_000)
                page.wait_for_selector("#authzIds .authz-name", timeout=10_000)
                page.route("**/api/authz", hold_authz_save)
                try:
                    page.locator("#authzIds .authz-name").first.fill("ui-audit-held-identity")
                    page.locator("#authzSave").click()
                    wait_for_route(held_authz, "held Authz identity save")
                    result.require(
                        page.locator("#authzClose").is_disabled()
                        and page.locator("#authzIds .authz-name").first.is_disabled(),
                        "Authz controls did not lock while identities were saving",
                    )
                    page.keyboard.press("Escape")
                    page.locator("#authzModal").click(position={"x": 5, "y": 5})
                    page.wait_for_timeout(80)
                    result.require(page.locator("#authzModal").is_visible(), "Authz identity save was dismissible before acknowledgement")
                    result.require(page.evaluate("document.activeElement?.id") == "authzStatus", "Authz pending state lost modal focus")
                    reject_expected(held_authz[0], "Authz identity save")
                    page.wait_for_function(
                        "document.querySelector('#authzStatus')?.getAttribute('role')==='alert'"
                        "&&document.querySelector('#authzStatus')?.textContent.includes('failed')"
                    )
                    absorb_expected_rejections()
                    result.require(
                        page.locator("#authzStatus").get_attribute("aria-live") == "assertive",
                        "Authz failure was not announced assertively",
                    )
                    result.require(page.evaluate("document.activeElement?.id") == "authzSave", "Authz failure did not restore Save focus")
                    page.locator("#authzSave").click()
                    wait_for_route(held_authz, "retried Authz identity save", 2)
                    result.require(page.evaluate("document.activeElement?.id") == "authzStatus", "Authz retry did not focus its pending status")
                    held_authz[1].continue_()
                    page.wait_for_function(
                        "document.querySelector('#authzStatus')?.textContent.includes('Identities saved')",
                        timeout=10_000,
                    )
                    result.require(page.locator("#authzClose").is_enabled(), "Authz close control did not restore after acknowledgement")
                    result.require(page.evaluate("document.activeElement?.id") == "authzSave", "Authz acknowledgement did not restore Save focus")

                    page.unroute("**/api/authz", hold_authz_save)
                    page.route("**/api/authz/run", hold_authz_run)
                    page.locator('#authzMode button[data-m="scope"]').click()
                    page.wait_for_selector("#authzScopeEdit", state="visible", timeout=10_000)
                    page.locator("#authzRun").click()
                    wait_for_route(held_authz_runs, "held in-scope Authz run")
                    result.require(page.locator("#authzScopeEdit").is_disabled(), "Authz scope navigation stayed enabled during a run")
                    result.require(page.evaluate("document.activeElement?.id") == "authzStatus", "Authz run did not retain pending focus")
                    page.evaluate("""() => {
                      const edit=document.querySelector('#authzScopeEdit');
                      edit.disabled=false;
                      edit.click();
                    }""")
                    page.wait_for_timeout(80)
                    result.require(page.locator("#authzModal").is_visible(), "busy Authz scope navigation closed the modal")
                    result.require(
                        page.locator('.tab[data-tab="settings"][aria-selected="true"]').count() == 0,
                        "busy Authz scope navigation changed the underlying panel",
                    )
                    held_authz_runs[0].fulfill(
                        status=200,
                        content_type="application/json",
                        body='{"runs":[],"summary":{"endpoints":0,"flagged":0}}',
                    )
                    page.wait_for_function(
                        "document.querySelector('#authzStatus')?.textContent.includes('Authorization replay complete')",
                        timeout=10_000,
                    )
                    result.require(page.evaluate("document.activeElement?.id") == "authzRun", "Authz run acknowledgement did not restore Run focus")
                finally:
                    for route in held_authz:
                        try:
                            route.continue_()
                        except Exception:
                            pass
                    for route in held_authz_runs:
                        try:
                            route.fulfill(
                                status=200,
                                content_type="application/json",
                                body='{"runs":[],"summary":{"endpoints":0,"flagged":0}}',
                            )
                        except Exception:
                            pass
                    try:
                        page.unroute("**/api/authz", hold_authz_save)
                        page.unroute("**/api/authz/run", hold_authz_run)
                    except Exception:
                        pass
                    try:
                        restore_status = page.evaluate(
                            """async identities => (await fetch('/api/authz',{
                              method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({identities})
                            })).status""",
                            original_identities,
                        )
                        if restore_status not in (200, 204):
                            raise RuntimeError(f"POST returned {restore_status}")
                    except Exception as exc:
                        result.failures.append(f"held-Authz audit cleanup failed: {type(exc).__name__}: {exc}")
                    if page.locator("#authzModal").is_visible() and page.locator("#authzClose").is_enabled():
                        page.locator("#authzClose").click()

            result.run("modal mutations retain focus, errors, dismissal, and scope ownership", mutation_modals_lock_dismissal)

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

                # Simulate a second UI/API client. The active Allowlist pane
                # must consume allowlist.update and reconcile itself; hidden
                # panes remain lazy and do no background work.
                external_allow = page.evaluate(
                    """async () => {
                      const response = await fetch('/api/allowlist', {
                        method: 'POST',
                        headers: {'content-type': 'application/json'},
                        body: JSON.stringify({cidr: '127.0.0.253/32', label: 'external-audit'})
                      });
                      if (!response.ok) throw new Error(`allowlist POST ${response.status}`);
                      return response.json();
                    }"""
                )
                external_allow_id = int(external_allow.get("id", 0))
                result.require(external_allow_id > 0, "external allowlist mutation returned no id")
                page.wait_for_function(
                    """() => [...document.querySelectorAll('#allowList tr')]
                      .some(row=>row.textContent.includes('127.0.0.253/32'))""",
                    timeout=10_000,
                )
                page.evaluate(
                    """async id => {
                      const response = await fetch(`/api/allowlist/${id}`, {method: 'DELETE'});
                      if (!response.ok) throw new Error(`allowlist DELETE ${response.status}`);
                    }""",
                    external_allow_id,
                )
                page.wait_for_function(
                    "!document.querySelector('#allowList tr')?.textContent.includes('127.0.0.253/32')",
                    timeout=10_000,
                )
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
                project_context = browser.new_context(viewport={"width": 1024, "height": 768})
                project_page = project_context.new_page()
                project_page.set_default_timeout(10_000)
                attach_observers(project_page, result, base_netloc)
                switch_requests: List[Any] = []

                def expose_switchable_fixture(route: Any) -> None:
                    route.fulfill(
                        status=200,
                        content_type="application/json",
                        body=json.dumps({
                            "current": "ui-audit-fixture",
                            "dir": "/audit/projects/ui-audit-fixture",
                            "projects": [{"name": "default", "path": ""}],
                            "canSwitch": True,
                        }),
                    )

                def block_project_switch(route: Any) -> None:
                    switch_requests.append(route)
                    route.fulfill(status=400, content_type="application/json", body='{"error":"switch must not be attempted"}')

                project_page.route("**/api/project", expose_switchable_fixture)
                project_page.route("**/api/project/switch", block_project_switch)
                try:
                    project_page.goto(base, wait_until="domcontentloaded")
                    wait_ready(project_page)
                    project_page.locator('.tab[data-tab="settings"]').click()
                    project_page.locator("#projBadge").click()
                    project_page.wait_for_selector("#projModal", state="visible", timeout=10_000)
                    project_page.locator("#pmNew").fill("ui-audit-relative")
                    project_page.locator("#pmNewPath").fill("relative/audit")
                    project_page.locator("#pmNewBtn").click()
                    project_page.wait_for_function(
                        "needle => [...document.querySelectorAll('#toast .toast-item')].some(el=>el.textContent.includes(needle))",
                        arg="absolute folder path",
                        timeout=10_000,
                    )
                    project_page.wait_for_function(
                        "document.querySelector('#pmSwitchNote')?.textContent.includes('absolute folder path')",
                        timeout=10_000,
                    )
                    result.require(project_page.locator("#pmNewPath").get_attribute("aria-invalid") == "true", "relative project path was not associated with its field")
                    result.require(project_page.locator("#pmSwitchNote").get_attribute("role") == "alert", "relative project path error was not persistently announced")
                    result.require(not switch_requests, "relative project path attempted a project switch request")
                finally:
                    project_context.close()

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
                authz_row = page.locator(f'#rows .trow[data-id="{explicit_id}"]')
                authz_row.scroll_into_view_if_needed()
                for attempt in range(2):
                    try:
                        authz_row.click(button="right")
                        page.wait_for_selector("#ctxmenu.show", state="visible", timeout=10_000)
                        page.locator("#ctxmenu .ctx-item", has_text="Authz test").click(timeout=2_000)
                        break
                    except Exception:
                        if attempt == 1:
                            raise
                        page.keyboard.press("Escape")
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

            def flow_note_read_waits_for_save() -> None:
                invalidate_and_close_flow_popup(page)
                page.locator('.tab[data-tab="proxy"]').click()
                page.wait_for_function("document.querySelectorAll('#rows .trow').length>=2", timeout=10_000)
                rows = page.locator("#rows .trow")
                flow_a = rows.nth(0).get_attribute("data-id")
                flow_b = rows.nth(1).get_attribute("data-id")
                page.locator(f'#rows .trow[data-id="{flow_a}"]').click(force=True)
                page.wait_for_selector("#noteBar", state="visible", timeout=10_000)
                original_note = page.locator("#noteInput").input_value()
                marker = "UI audit acknowledged note"
                held_note_saves: List[Any] = []
                detail_reads: List[str] = []

                def hold_note_save(route: Any) -> None:
                    held_note_saves.append(route)

                def observe_detail_read(route: Any) -> None:
                    detail_reads.append(route.request.url)
                    route.continue_()

                page.route(f"**/api/flows/{flow_a}/note", hold_note_save)
                page.route(f"**/api/flows/{flow_a}", observe_detail_read)
                try:
                    page.locator("#noteInput").fill(marker)
                    page.locator(f'#rows .trow[data-id="{flow_b}"]').click(force=True)
                    deadline = time.monotonic() + 2.0
                    while not held_note_saves and time.monotonic() < deadline:
                        page.wait_for_timeout(10)
                    result.require(bool(held_note_saves), "flow-note blur did not issue its PUT")
                    page.locator(f'#rows .trow[data-id="{flow_a}"]').click(force=True)
                    page.wait_for_timeout(120)
                    result.require(
                        not detail_reads,
                        "Inspector fetched a flow snapshot before its queued note save acknowledged",
                    )
                    held_note_saves[0].continue_()
                    page.wait_for_function(
                        "value => document.querySelector('#noteInput')?.value===value",
                        arg=marker,
                        timeout=10_000,
                    )
                    result.require(bool(detail_reads), "Inspector did not refetch the flow after its note save acknowledged")
                finally:
                    for route in held_note_saves:
                        try:
                            route.continue_()
                        except Exception:
                            pass
                    try:
                        page.unroute(f"**/api/flows/{flow_a}/note", hold_note_save)
                        page.unroute(f"**/api/flows/{flow_a}", observe_detail_read)
                    except Exception:
                        pass
                    try:
                        cleanup_status = page.evaluate(
                            """async owner => (await fetch('/api/flows/'+owner.id+'/note',{
                              method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({note:owner.note})
                            })).status""",
                            {"id": flow_a, "note": original_note},
                        )
                        if cleanup_status not in (200, 204):
                            raise RuntimeError(f"PUT returned {cleanup_status}")
                    except Exception as exc:
                        result.failures.append(f"flow-note audit cleanup failed: {type(exc).__name__}: {exc}")
                    invalidate_and_close_flow_popup(page)

            result.run("Inspector reads wait for their flow's acknowledged note save", flow_note_read_waits_for_save)

            def burst_and_map_performance() -> None:
                invalidate_and_close_flow_popup(page)
                page.set_viewport_size({"width": 1440, "height": 900})
                try:
                    page.locator('.tab[data-tab="proxy"]').click(timeout=2_000)
                except Exception:
                    if not page.locator("#flowModal").is_visible():
                        raise
                    # Commit the intended tab change underneath the known
                    # transient popup, then finish closing that popup before
                    # the performance measurements begin.
                    page.locator('.tab[data-tab="proxy"]').click(force=True)
                    invalidate_and_close_flow_popup(page)
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
                        responses = list(pool.map(lambda url: proxy_request(proxy, url), urls))
                    statuses = [status for status, _body in responses]
                    network_ms = round((time.perf_counter() - started) * 1000, 1)
                    network_samples.append(network_ms)
                    bad = next(((status, body) for status, body in responses if status != 200), None)
                    result.require(bad is None, f"burst {run + 1} contained a direct-proxy failure: {bounded_response_diagnostic(*bad)}" if bad else "")
                    page.wait_for_timeout(1500)
                    run_long_tasks = page.evaluate("window.__uiAuditLongTasks||[]")
                    all_long_tasks.extend(run_long_tasks)
                    result.require(max(run_long_tasks or [0]) < 200, f"burst {run + 1} produced a blocking long task: {run_long_tasks}")
                invalidate_and_close_flow_popup(page)
                rows = page.locator("#rows .trow").count()
                # Measure the virtualized History surface itself. A global DOM
                # count couples this performance guard to unrelated hidden
                # panels (for example a richer Findings editor) and produces
                # false regressions even when History remains bounded.
                nodes = page.evaluate("document.querySelector('#rows')?.getElementsByTagName('*').length||0")
                result.require(0 < rows <= 160, f"History DOM was not bounded after burst: {rows} rows")
                result.require(nodes <= rows * 12 + 100, f"History row DOM grew unexpectedly: {nodes} nodes for {rows} rows")

                rows_box = page.locator("#rows")
                rows_box.evaluate("el=>{el.scrollTop=Math.min(500,el.scrollHeight-el.clientHeight)}")
                saved_scroll = rows_box.evaluate("el=>el.scrollTop")
                invalidate_and_close_flow_popup(page)
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
                invalidate_and_close_flow_popup(page)
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
                invalidate_and_close_flow_popup(page)
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
                invalidate_and_close_flow_popup(page)
                screenshot_meta: Dict[str, Dict[str, Any]] = {}

                def capture(name: str, path: Path, dimensions: Tuple[int, int]) -> None:
                    page.screenshot(path=str(path))
                    width, height = png_dimensions(path)
                    result.require((width, height) == dimensions, f"{path.name} is {width}x{height}, expected {dimensions[0]}x{dimensions[1]}")
                    screenshot_meta[name] = {"path": str(path), "width": width, "height": height}

                # Retain the evidence-first Findings detail at every required
                # viewport, not only the surrounding product surfaces.
                page.set_viewport_size({"width": 1440, "height": 900})
                page.locator('.tab[data-tab="findings"]').click()
                finding_row = page.locator("#findList .find-row").filter(has_text="Generic UI audit finding").first
                finding_row.wait_for(state="visible", timeout=10_000)
                finding_row.click()
                page.wait_for_function(
                    "document.querySelector('#findDetail')?.textContent.includes('Generic UI audit finding')"
                )
                if page.locator("#findSummary").is_visible():
                    page.locator("#findToggleEdit").click()
                    page.wait_for_selector("#findSummary", state="hidden", timeout=10_000)
                result.require(not page.locator("#findSummary").is_visible(), "Findings screenshot remained in edit mode")
                page.evaluate("() => { window.scrollTo(0,0); const detail=document.querySelector('#findDetail'); if(detail) detail.scrollTop=0; }")
                page.mouse.move(170, 20)
                capture("findings-1440x900", output / "findings-after-1440x900.png", (1440, 900))

                page.set_viewport_size({"width": 1024, "height": 768})
                page.evaluate("() => { window.scrollTo(0,0); const detail=document.querySelector('#findDetail'); if(detail) detail.scrollTop=0; }")
                capture("findings-1024x768", output / "findings-after-1024x768.png", (1024, 768))

                page.set_viewport_size({"width": 390, "height": 844})
                page.wait_for_function("document.querySelector('#findDetail')?.classList.contains('find-mobile-detail-visible')")
                page.evaluate("() => { window.scrollTo(0,0); const detail=document.querySelector('#findDetail'); if(detail) detail.scrollTop=0; }")
                capture("findings-390x844", output / "findings-after-390x844.png", (390, 844))

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
        "application_source": application_source,
        "base_url": base,
        "mode": "full" if args.full else "smoke",
        "startup_engines": args.startup_engines,
        "preflight": preflight,
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
    parser.add_argument("--base-url", default=None)
    parser.add_argument("--proxy", type=parse_host_port, default=None, metavar="HOST:PORT")
    parser.add_argument("--output-dir", default="/tmp/interseptor-ui-audit")
    parser.add_argument("--full", action="store_true", help="run the mutating matrix against a managed disposable candidate")
    parser.add_argument("--managed", action="store_true", help="required ownership mode for --full; starts a disposable candidate")
    parser.add_argument("--burst", type=int, default=240, help="requests in the full high-volume pass")
    parser.add_argument("--perf-runs", type=int, default=3, help="repeat the high-volume profile this many times")
    parser.add_argument(
        "--startup-engines",
        default="chromium",
        help="comma-separated browser engines for saved-workspace startup fault checks",
    )
    parser.add_argument("--headed", action="store_true")
    args = parser.parse_args()
    requested_engines = [value.strip().lower() for value in args.startup_engines.split(",") if value.strip()]
    unknown_engines = sorted(set(requested_engines) - {"chromium", "firefox", "webkit"})
    if unknown_engines:
        parser.error("--startup-engines accepts only chromium, firefox, webkit")
    args.startup_engines = list(dict.fromkeys(["chromium", *requested_engines]))
    if args.burst < 120:
        parser.error("--burst must be at least 120 to exercise History virtualization")
    if args.perf_runs < 1 or args.perf_runs > 10:
        parser.error("--perf-runs must be between 1 and 10")
    if args.full:
        if not args.managed:
            parser.error("--full requires --managed; arbitrary existing servers are not accepted")
        if args.base_url or args.proxy:
            parser.error("--full --managed chooses its own disposable server and data root")
    elif args.managed:
        parser.error("--managed requires --full")
    if args.full:
        # Managed mode fills these only after it owns the candidate process.
        args.base_url = None
        args.proxy = None
        args.expected_project = None
        args.expected_data_dir = None
    else:
        args.base_url = args.base_url or "http://127.0.0.1:9966"
        args.proxy = args.proxy or ("127.0.0.1", 8080)
    managed: Optional[Tuple[subprocess.Popen[bytes], Path, str, List[socket.socket]]] = None
    managed_source: Optional[Dict[str, Any]] = None
    result: Optional[AuditResult] = None
    audit_error: Optional[str] = None
    cleanup_error = False
    try:
        if args.full:
            process, root, project, base, proxy, managed_source, reservations = prepare_managed_audit()
            managed = (process, root, project, reservations)
            args.base_url = base
            args.proxy = proxy
            args.expected_project = project
            args.expected_data_dir = str(root)
        result = run_audit(args, managed_source)
    except Exception as exc:
        audit_error = type(exc).__name__
    finally:
        if managed is not None:
            try:
                cleanup_managed_audit(*managed)
            except Exception:
                cleanup_error = True
    if audit_error:
        print(f"audit failed ({audit_error})", file=sys.stderr)
        return 1
    if cleanup_error:
        print("audit cleanup failed; an owned temporary candidate or root may remain", file=sys.stderr)
        return 1
    if result is None:
        print("audit failed before producing a result", file=sys.stderr)
        return 1
    print(json.dumps({"cases": result.cases, "metrics": result.metrics, "failures": result.failures}, indent=2, sort_keys=True))
    return 1 if result.failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
