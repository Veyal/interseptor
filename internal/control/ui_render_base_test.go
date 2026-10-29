package control

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIRenderHTMLResponseBase runs the real renderHTMLResponse with the real
// withBaseHref: the sandbox stays empty and the base is injected only for an
// absolute http(s) flow URL.
func TestUIRenderHTMLResponseBase(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	base := strings.TrimPrefix(readUIAsset(t, "js/render-base.js"), "")
	base = strings.ReplaceAll(base, "export function", "function")
	script := base + repeaterRenderJS(t, core, "export function renderHTMLResponse(raw,baseURL)") + `
const escAttr=s=>String(s).replaceAll('&','&amp;').replaceAll('"','&quot;').replaceAll('<','&lt;').replaceAll('>','&gt;');
const raw='HTTP/1.1 200 OK\r\n\r\n<html><head></head><body><img src="/logo.png"></body></html>';
const out=renderHTMLResponse(raw,'https://example.com/app/');
if(!out.includes('sandbox=""')||out.includes('allow-')||!out.includes('referrerpolicy="no-referrer"'))throw Error('sandbox weakened: '+out);
if(!out.includes('&lt;head&gt;&lt;base href=&quot;https://example.com/app/&quot;&gt;'))throw Error('base missing: '+out);
for(const bad of [undefined,'','javascript:alert(1)','/x'])if(renderHTMLResponse(raw,bad).includes('base href'))throw Error('base injected for '+bad);
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("renderHTMLResponse base: %v\n%s", err, out)
	}
}

// TestUIRenderCallersPassFlowURL pins every Render caller to a base URL.
func TestUIRenderCallersPassFlowURL(t *testing.T) {
	for file, want := range map[string]string{
		"js/proxy.js": "renderHTMLResponse(raw,flowUrl(detail))",
		"js/tools.js": "renderHTMLResponse(raw,String(t.url||'').trim())",
	} {
		if !strings.Contains(readUIAsset(t, file), want) {
			t.Errorf("%s must call %s", file, want)
		}
	}
	drawer := readUIAsset(t, "js/flowdrawer.js")
	for _, want := range []string{"renderHTML: (raw, url) => renderHTMLResponse(raw, url)", "data-fd-res-view"} {
		if !strings.Contains(drawer, want) {
			t.Errorf("flowdrawer.js missing %s", want)
		}
	}
}
