package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
)

// The MCP export_report tool reaches SARIF and credential masking, the same
// options the REST report and the UI offer.
func TestMCPExportReportSupportsSARIFAndMasking(t *testing.T) {
	h, s, _ := newHub(t)
	_, secrets := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	srv := mcp.New(ts.URL)

	sarif, err := srv.Call("export_report", map[string]any{"format": "sarif"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sarif, `"version": "2.1.0"`) {
		t.Fatalf("export_report format=sarif did not return a SARIF log: %.300s", sarif)
	}
	masked, err := srv.Call("export_report", map[string]any{"maskHeaders": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(masked, secret) {
			t.Errorf("maskHeaders=true leaked %q", secret)
		}
	}
	if _, err := srv.Call("export_report", map[string]any{"format": "docx"}); err == nil || !strings.Contains(err.Error(), "sarif") {
		t.Fatalf("unknown format error should list the formats: %v", err)
	}
}
