#!/usr/bin/env python3
"""Seed only generic local popup-audit fixtures into a supplied disposable candidate."""
import hashlib, importlib.util, json, os, sys, time
from pathlib import Path
from playwright.sync_api import sync_playwright

REPO=Path(os.environ.get('INTERSEPTOR_REPO',Path.cwd())).resolve()
sys.path.insert(0,str(REPO/'scripts'))
import ui_browser_audit as audit
probe=REPO/'docs/ui-audit/findings-workspace/probes/interseptor_final_matrix.py'
spec=importlib.util.spec_from_file_location('popup_seed_matrix',probe); matrix=importlib.util.module_from_spec(spec); spec.loader.exec_module(matrix)
base=os.environ['BASE_URL']; proxy=os.environ['PROXY']; out=Path(os.environ['OUT_PATH'])
out.parent.mkdir(parents=True,exist_ok=True)
with sync_playwright() as pw:
    b=pw.chromium.launch(); p=b.new_page(viewport={'width':1440,'height':900}); p.goto(base,wait_until='domcontentloaded'); matrix.ready(p)
    fixture=matrix.seed(p,base,proxy)
    # The popup audit's conditional Tags path uses its fixed generic row #4.
    r=p.evaluate("""async()=>{const r=await fetch('/api/findings/4',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({tags:['generic']})});if(!r.ok)throw new Error(await r.text());return await r.json()}""")
    b.close()
payload={'base':base,'proxy':proxy,'fixture':fixture,'tagged_id':r.get('id'),'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'matrix_probe_sha256':hashlib.sha256(probe.read_bytes()).hexdigest(),'started_unix':time.time()}
out.write_text(json.dumps(payload,indent=2)+'\n');print(json.dumps(payload))
