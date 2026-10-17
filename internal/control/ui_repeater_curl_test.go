package control

import (
	"os/exec"
	"testing"
)

// TestUIRepeaterCopyCurlKeepsAHostOverride runs the real repBuildRaw and
// repCurlCommand: a Host header that differs from the URL (vhost or Host-header
// testing) must survive into the copied cURL command, while a Host that only
// repeats the URL is left for curl to derive. HEAD uses curl -I.
func TestUIRepeaterCopyCurlKeepsAHostOverride(t *testing.T) {
	source := readUIAsset(t, "js/tools.js")
	script := repeaterRenderJS(t, source, "function compactBody(s)") +
		repeaterRenderJS(t, source, "function shellQuote(v)") + // one-liner: extraction runs through repExportBody
		repeaterRenderJS(t, source, "function repBuildRaw(t)") +
		repeaterRenderJS(t, source, "function repCurlCommand(built)") + `
const vhost=repCurlCommand(repBuildRaw({method:'GET',url:'https://example.com/admin',headers:'Host: internal.example.com\nX-Test: 1',body:'',reqView:'raw'}));
if(!vhost.includes("-H 'Host: internal.example.com'"))throw Error('Host override dropped from cURL:\n'+vhost);
const same=repCurlCommand(repBuildRaw({method:'GET',url:'https://example.com/a',headers:'Host: example.com',body:'',reqView:'raw'}));
if(/Host:/i.test(same))throw Error('a Host equal to the URL host should be left to curl:\n'+same);
const head=repCurlCommand(repBuildRaw({method:'HEAD',url:'https://example.com/a',headers:'',body:'',reqView:'raw'}));
if(!head.startsWith('curl -I ')||head.includes('-X HEAD'))throw Error('HEAD must use curl -I:\n'+head);
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater cURL export: %v\n%s", err, out)
	}
}
