package control

import (
	"os/exec"
	"testing"
)

func TestUIHexDumpAndInspectorCopy(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	proxy := readUIAsset(t, "js/proxy.js")

	script := repeaterRenderJS(t, core, "export function formatHexDump(strOrBytes, maxBytes=32768)") +
		repeaterRenderJS(t, proxy, "export function toggleSelectCurrentFlow()") +
		repeaterRenderJS(t, proxy, "export async function copyFlowRaw(f,side='req')") +
		repeaterRenderJS(t, proxy, "export async function copyFlowBody(f,side='req')") + `
const fmtSize = n => n + " B";
// Test formatHexDump
const dump = formatHexDump("Hello\x00World! 1234");
if(!dump.startsWith("00000000  48 65 6c 6c 6f 00 57 6f  72 6c 64 21 20 31 32 33  |Hello.World! 123|")) {
  throw Error("unexpected hex dump output: " + dump);
}
const longDump = formatHexDump("A".repeat(100), 32);
if(!longDump.includes("... [truncated, 100 B total]")) {
  throw Error("expected truncation marker in hex dump: " + longDump);
}

// Test toggleSelectCurrentFlow
const row = {
  classes: new Set(),
  attrs: {},
  classList: {
    toggle(c, force){ if(force) row.classes.add(c); else row.classes.delete(c); },
    add(c){ row.classes.add(c); },
    remove(c){ row.classes.delete(c); },
  },
  setAttribute(k, v){ row.attrs[k] = v; },
};
let selBarUpdated = false;
globalThis.updateSelBar = () => { selBarUpdated = true; };
globalThis.document = {
  querySelector(sel){
    if(sel === '.trow[data-id="42"]') return row;
    return null;
  }
};
globalThis.state = {
  selId: 42,
  selected: new Set(),
};

toggleSelectCurrentFlow();
if(!state.selected.has(42) || !row.classes.has('msel') || row.attrs['aria-pressed'] !== 'true' || !selBarUpdated) {
  throw Error("toggleSelectCurrentFlow failed to select");
}

toggleSelectCurrentFlow();
if(state.selected.has(42) || row.classes.has('msel') || row.attrs['aria-pressed'] !== 'false') {
  throw Error("toggleSelectCurrentFlow failed to deselect");
}

// Test copyFlowRaw and copyFlowBody
let copiedText = '';
globalThis.copyText = (text, msg) => { copiedText = text; };
globalThis.toast = (msg) => {};
globalThis.api = async (url) => {
  if(url === '/api/flows/10/raw?side=req') {
    return 'POST /test HTTP/1.1\r\nHost: example.com\r\n\r\n{"action":"test"}';
  }
  if(url === '/api/flows/10/raw?side=res') {
    return 'HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\n{"status":"ok"}';
  }
  throw Error('unexpected url: ' + url);
};

await copyFlowRaw({id: 10}, 'req');
if(!copiedText.startsWith('POST /test HTTP/1.1')) throw Error('copyFlowRaw req failed');

await copyFlowBody({id: 10}, 'req');
if(copiedText !== '{"action":"test"}') throw Error('copyFlowBody req failed: ' + copiedText);

await copyFlowBody({id: 10}, 'res');
if(copiedText !== '{"status":"ok"}') throw Error('copyFlowBody res failed: ' + copiedText);
`

	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("UI hex and copy tests: %v\n%s", err, out)
	}
}
