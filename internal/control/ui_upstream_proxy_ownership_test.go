package control

import (
	"strings"
	"testing"
)

func TestUIUpstreamProxySaveReconcilesOnlyUnchangedFields(t *testing.T) {
	source := executableJS(readUIAsset(t, "js/settings.js"))
	for _, contract := range []string{
		"function upstreamProxyFieldSnapshot()",
		"const submitted=upstreamProxyFieldSnapshot()",
		"const acknowledged=await saveSettingsPatch({upstreamProxy,upstreamProxyCA})",
		"upstreamProxyValues(typeof acknowledged?.upstreamProxy==='string'?acknowledged.upstreamProxy:upstreamProxy)",
		"settingsEditOwned(el,snapshot.generation,snapshot.value)",
		"if(settingsEditOwned(el,snapshot.generation,snapshot.value))",
		"renderUpstreamProxyFields($('#setUpstreamScheme')?.value||'direct')",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("upstream proxy ownership contract missing %q", contract)
		}
	}
}

func TestUIUpstreamProxySaveKeepsDirtyFieldsOnFailure(t *testing.T) {
	settings := readUIAsset(t, "js/settings.js")
	start := strings.Index(settings, "$('#saveUpstreamBtn')")
	end := -1
	if start >= 0 {
		if offset := strings.Index(settings[start:], "\n\nlet sessionLoaded"); offset >= 0 {
			end = start + offset
		}
	}
	if start < 0 || end <= start {
		t.Fatal("upstream proxy save handler not found")
	}
	handler := settings[start:end]
	for _, contract := range []string{
		"const submitted=upstreamProxyFieldSnapshot()",
		"const acknowledged=await saveSettingsPatch({upstreamProxy,upstreamProxyCA})",
		"catch(e){toast(e.message,'error');}",
	} {
		if !strings.Contains(handler, contract) {
			t.Errorf("upstream proxy failure ownership contract missing %q", contract)
		}
	}
	if strings.Contains(handler, "parseUpstreamProxyURL(upstreamProxy);toast") {
		t.Error("upstream proxy save must not blindly reparse the submitted URL over newer edits")
	}
}
