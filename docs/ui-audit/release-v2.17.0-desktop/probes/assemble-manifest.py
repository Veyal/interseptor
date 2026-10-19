#!/usr/bin/env python3
"""Assemble the retained visual-audit checkpoint from one finished managed audit run.

Nothing here executes the UI. The browser evidence comes from
`python3 scripts/ui_browser_audit.py --full --managed --output-dir <run>`; this
script only verifies that run against the current source tree, copies its real
artifacts into a checkpoint directory, and records their hashes in manifest.json.

Required environment:
  QA_REPO      repository root whose runtime source was audited
  QA_RUN       --output-dir of the finished audit run (browser-audit.json, firefox/, webkit/)
  QA_OUT       checkpoint directory to create (must not exist yet)
  QA_BINARY    reference binary built from the same source (CGO_ENABLED=0 go build -trimpath)
  QA_COMMAND   exact audit command line that produced QA_RUN
  QA_STARTED / QA_FINISHED   UTC timestamps of the audit run
  QA_TOOLS     free-text description of the Python/Playwright/browser versions used

The script refuses to write anything unless the run passed in every engine and its
recorded runtime digest and harness hash equal the current working tree.
"""
from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

ENGINES = ("chromium", "firefox", "webkit")
TRANSFORMATION = (
    "Only local run-directory and screenshot paths are made relative to this checkpoint. "
    "All other values are unchanged."
)


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(f"refusing to assemble: {message}")


def runtime_digest(repo: Path) -> tuple[str, int]:
    """Same selection and digest as TestUIVisualAuditEvidenceMatchesCurrentRuntime."""
    listed = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "go.mod", "go.sum", "cmd", "internal"],
        cwd=repo, check=True, capture_output=True,
    ).stdout
    paths = set()
    for raw in listed.split(b"\0"):
        if not raw:
            continue
        relative = raw.decode()
        if relative.endswith("_test.go"):
            continue
        if (repo / relative).is_file():
            paths.add(relative)
    digest = hashlib.sha256()
    for relative in sorted(paths):
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(hashlib.sha256((repo / relative).read_bytes()).digest())
    return digest.hexdigest(), len(paths)


def load(path: Path) -> dict:
    return json.loads(path.read_text())


def relativize(value, run: Path, engine: str):
    """Rewrite absolute run-directory paths to checkpoint-relative links."""
    if isinstance(value, dict):
        return {key: relativize(item, run, engine) for key, item in value.items()}
    if isinstance(value, list):
        return [relativize(item, run, engine) for item in value]
    if isinstance(value, str) and value.startswith(str(run) + os.sep):
        parts = Path(value[len(str(run)) + 1:]).parts
        owner = parts[0] if parts and parts[0] in ENGINES else engine
        return "screenshots/" + owner + "-" + parts[-1]
    return value


def main() -> None:
    repo = Path(os.environ["QA_REPO"]).resolve()
    run = Path(os.environ["QA_RUN"]).resolve()
    out = Path(os.environ["QA_OUT"]).resolve()
    binary = Path(os.environ["QA_BINARY"]).resolve()
    require(not out.exists(), f"{out} already exists")

    aggregate = load(run / "browser-audit.json")
    reports = {"chromium": None, "firefox": load(run / "firefox" / "browser-audit.json"), "webkit": load(run / "webkit" / "browser-audit.json")}
    reports["chromium"] = aggregate["engine_reports"]["chromium"] if isinstance(aggregate.get("engine_reports", {}).get("chromium"), dict) and aggregate["engine_reports"]["chromium"].get("cases") else None

    require(aggregate.get("mode") == "full", "run is not a --full run")
    require(sorted(aggregate.get("browser_engines", [])) == sorted(ENGINES), "run did not cover all three engines")
    for key in ("failures", "console_errors", "page_errors", "http_errors", "external_requests"):
        require(aggregate.get(key) == [], f"aggregate {key} is not empty: {aggregate.get(key)!r}"[:200])
    require(aggregate.get("cases") and all(v == "pass" for v in aggregate["cases"].values()), "aggregate contains a non-pass case")

    digest, files = runtime_digest(repo)
    source = aggregate["application_source"]
    harness = sha256_file(repo / "scripts" / "ui_browser_audit.py")
    require(source["runtime_sha256"] == digest and source["runtime_files"] == files, "run was executed against different runtime source")
    require(source["audit_harness_sha256"] == harness, "run was executed with a different harness")

    per_engine_cases = {}
    for engine in ENGINES:
        report = reports[engine]
        if report is None:
            per_engine_cases[engine] = sum(1 for name in aggregate["cases"] if name.endswith(f"({engine})")) or None
            continue
        require(report.get("failures") == [], f"{engine} reported failures")
        require(report.get("application_source") == source, f"{engine} audited a different source identity")
        per_engine_cases[engine] = len(report["cases"])

    out.mkdir(parents=True)
    (out / "visual" / "screenshots").mkdir(parents=True)
    (out / "probes").mkdir()

    # Real screenshots only: every PNG the harness retained for each engine.
    copied = []
    for engine in ENGINES:
        source_dir = run if engine == "chromium" else run / engine
        for png in sorted(source_dir.glob("*.png")):
            target = out / "visual" / "screenshots" / f"{engine}-{png.name}"
            shutil.copy2(png, target)
            copied.append(target)
    require(len(copied) >= 8, "fewer than eight screenshots were retained by the run")

    publication = []
    published = {"visual/report.json": (aggregate, "chromium"), "visual/firefox.json": (reports["firefox"], "firefox"), "visual/webkit.json": (reports["webkit"], "webkit")}
    original_paths = {"visual/report.json": run / "browser-audit.json", "visual/firefox.json": run / "firefox" / "browser-audit.json", "visual/webkit.json": run / "webkit" / "browser-audit.json"}
    for name, (document, engine) in published.items():
        rewritten = relativize(document, run, engine)
        text = json.dumps(rewritten, indent=2, sort_keys=True) + "\n"
        require("/Users/" not in text, f"{name} still contains a home-directory path")
        (out / name).write_text(text)
        publication.append({"file": name, "original_sha256": sha256_file(original_paths[name]), "transformation": TRANSFORMATION})

    shutil.copy2(Path(__file__).resolve(), out / "probes" / "assemble-manifest.py")
    shutil.copy2(repo / "scripts" / "ui_browser_audit.py", out / "probes" / "ui_browser_audit.py")

    evidence = []
    for path in sorted(p for p in out.rglob("*") if p.is_file()):
        relative = path.relative_to(out).as_posix()
        if relative == "manifest.json":
            continue
        if relative.startswith("visual/screenshots/"):
            kind = "screenshot"
        elif relative.startswith("visual/"):
            kind = "report"
        elif relative == "probes/ui_browser_audit.py":
            kind = "dependency"
        else:
            kind = "probe"
        evidence.append({"file": relative, "kind": kind, "sha256": sha256_file(path)})

    version = subprocess.run([str(binary), "version"], capture_output=True, text=True, check=True).stdout.strip()
    manifest = {
        "accepted_runs": {
            "visual": {
                "cases_per_engine": per_engine_cases,
                "engines": list(ENGINES),
                "motion": "default, with the reduced-motion context verified inside the run",
                "report": "visual/report.json",
            }
        },
        "audit_run": {
            "command": os.environ["QA_COMMAND"],
            "finished_utc": os.environ["QA_FINISHED"],
            "started_utc": os.environ["QA_STARTED"],
            "tools": os.environ["QA_TOOLS"],
        },
        "binary": {
            "note": (
                "Reference build of the audited source (CGO_ENABLED=0 go build -trimpath ./cmd/interseptor). "
                "The harness builds and deletes its own disposable binary inside each engine run and does not "
                "record that binary's hash; runtime.runtime_sha256 is the binding identity of what was exercised."
            ),
            "sha256": sha256_file(binary),
            "version": version,
        },
        "evidence": evidence,
        "publication_reports": publication,
        "runtime": {
            "audit_harness_sha256": harness,
            "runtime_files": files,
            "runtime_sha256": digest,
        },
    }
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    print(json.dumps({"checkpoint": str(out), "evidence_files": len(evidence), "screenshots": len(copied), "runtime_sha256": digest}, indent=2))


if __name__ == "__main__":
    sys.exit(main())
