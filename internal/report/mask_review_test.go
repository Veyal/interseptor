package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A masked export masks secret query values on the raw request line. The
// citation line printed directly above that request ("> `GET host/path`")
// carries the same path, so it must be masked too — otherwise the token the
// reader was promised is hidden sits two lines above its placeholder.
func TestMaskSensitiveHeadersMasksQuerySecretsInFlowCitation(t *testing.T) {
	const canary = "CANARYquerytoken0123456789"
	path := "/cb?access_token=" + canary + "&page=2"
	raw := "GET " + path + " HTTP/1.1\r\nHost: example.com\r\n\r\n"
	findings := []store.Finding{
		{ID: 1, Severity: "High", Status: "open", Title: "Blocks", Blocks: []store.FindingBlock{{
			Type: "flow", FlowID: 7, Method: "GET", Host: "example.com", Path: path, Status: 200, ReqRaw: raw,
		}}},
		{ID: 2, Severity: "Low", Status: "open", Title: "Legacy", Flows: []store.FindingFlow{{
			FlowID: 8, Method: "GET", Host: "example.com", Path: path, Status: 200, ReqRaw: raw,
		}}},
	}
	masked := MaskSensitiveHeaders(findings)
	for name, out := range map[string]string{"markdown": Project(masked), "html": ProjectHTML(masked)} {
		if strings.Contains(out, canary) {
			t.Errorf("%s: masked export still shows the query secret:\n%s", name, out)
		}
		if !strings.Contains(out, "page=2") {
			t.Errorf("%s: masking damaged the rest of the path", name)
		}
	}
	if got := masked[0].Blocks[0].Path; strings.Contains(got, canary) || !strings.HasPrefix(got, "/cb?access_token=[redacted") {
		t.Errorf("block path = %q", got)
	}
	// The input is never mutated, and an unmasked export is unchanged.
	if findings[0].Blocks[0].Path != path || findings[1].Flows[0].Path != path {
		t.Error("MaskSensitiveHeaders mutated its input")
	}
	if !strings.Contains(Project(findings), canary) {
		t.Error("default export must keep evidence as captured")
	}
}
