package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func evidenceFinding() store.Finding {
	return store.Finding{ID: 1, Severity: "High", Status: "verified", Title: "IDOR on records",
		Blocks: []store.FindingBlock{
			{Type: "flow", FlowID: 1, Role: "baseline", Proof: "Alice reads her own record 7.", Method: "GET", Host: "example.com", Path: "/records/7", Status: 200,
				ReqRaw: "GET /records/7 HTTP/1.1\r\nHost: example.com\r\n\r\n", ResRaw: "HTTP/1.1 200 OK\r\n\r\n{\"id\":7}"},
			{Type: "flow", FlowID: 2, Role: "action", Proof: "Alice changes the id to 8.", Method: "GET", Host: "example.com", Path: "/records/8", Status: 200,
				ReqRaw: "GET /records/8 HTTP/1.1\r\nHost: example.com\r\n\r\n", ResRaw: "HTTP/1.1 200 OK\r\n\r\n{\"id\":8}"},
			{Type: "flow", FlowID: 2, Role: "result", Proof: "The response returns record 8 owned by bob", Method: "GET", Host: "example.com", Path: "/records/8", Status: 200,
				ReqRaw: "GET /records/8 HTTP/1.1\r\nHost: example.com\r\n\r\n", ResRaw: "HTTP/1.1 200 OK\r\n\r\n{\"id\":8}"},
			{Type: "flow", FlowID: 3, Role: "control", Proof: "Bob reads record 8.", Method: "GET", Host: "example.com", Path: "/records/8", Status: 200},
			{Type: "image", Hash: strings.Repeat("a", 64), Caption: "Record 8 in alice's browser", URL: "data:image/png;base64,AAAA"},
			{Type: "image", Hash: strings.Repeat("b", 64), URL: "data:image/png;base64,BBBB"},
		}}
}

func TestEvidenceProofLinesHaveOneFinalPeriod(t *testing.T) {
	out := Project([]store.Finding{evidenceFinding()}, nil)
	if strings.Contains(out, ".._") {
		t.Fatalf("proof sentence ends in a double period:\n%s", out)
	}
	if !strings.Contains(out, "proof=The response returns record 8 owned by bob._") {
		t.Fatalf("proof without its own period lost the closing one:\n%s", out)
	}
}

func TestControlRoleIsNotLabelledAsBaseline(t *testing.T) {
	out := Project([]store.Finding{evidenceFinding()}, nil)
	if strings.Contains(out, "role=control (normal behavior)") {
		t.Fatalf("control must not reuse the baseline label:\n%s", out)
	}
	if !strings.Contains(out, "role=control (comparison)") {
		t.Fatalf("control label missing:\n%s", out)
	}
}

func TestRepeatedFlowKeepsProofButPrintsTheExchangeOnce(t *testing.T) {
	out := Project([]store.Finding{evidenceFinding()}, nil)
	if !strings.Contains(out, "proof=The response returns record 8 owned by bob") {
		t.Fatalf("second citation of the flow lost its proof:\n%s", out)
	}
	if n := strings.Count(out, "GET /records/8 HTTP/1.1"); n != 1 {
		t.Fatalf("flow #2 raw request printed %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "Same exchange as flow #2 above") {
		t.Fatalf("repeated citation must point back at the printed exchange:\n%s", out)
	}
}

func TestScreenshotsGetVisibleNumberedCaptions(t *testing.T) {
	out := Project([]store.Finding{evidenceFinding()}, nil)
	if !strings.Contains(out, "_Figure 1.1: Record 8 in alice's browser_") || !strings.Contains(out, "_Figure 1.2: Screenshot_") {
		t.Fatalf("numbered captions missing:\n%s", out)
	}
	html := HTMLDocument(out, nil, store.ReportBranding{})
	if !strings.Contains(html, "<figure") || !strings.Contains(html, "<figcaption>Figure 1.1: Record 8 in alice's browser</figcaption>") {
		t.Fatalf("HTML caption must be visible, not alt-only:\n%s", html)
	}
}

func TestQuoteContinuationLineIsNotPrinted(t *testing.T) {
	html := markdownToHTML("> `GET example.com/` → **200**\n>\n")
	if strings.Contains(html, "&gt;") || strings.Contains(html, "<br>") {
		t.Fatalf("empty quote line leaked into HTML: %s", html)
	}
}

func TestReportHTMLRefusesRemoteImagesAndPinsCSP(t *testing.T) {
	md := "# Report\n\n![pixel](http://attacker.example.com/track.png)\n\n![local](file:///etc/hosts)\n\n![ok](data:image/png;base64,AAAA)\n"
	html := HTMLDocument(md, nil, store.ReportBranding{})
	if strings.Contains(html, `src="http://attacker.example.com`) || strings.Contains(html, `src="file:`) {
		t.Fatalf("remote or local-file image would be fetched by the PDF browser:\n%s", html)
	}
	if !strings.Contains(html, `src="data:image/png;base64,AAAA"`) {
		t.Fatalf("embedded image dropped:\n%s", html)
	}
	if !strings.Contains(html, `http-equiv="Content-Security-Policy"`) || !strings.Contains(html, "img-src data:") {
		t.Fatalf("report document must pin a no-network CSP:\n%s", html)
	}
}

func TestPrintCSSUsesA4WithPageNumbers(t *testing.T) {
	html := HTMLDocument("# Report\n", nil, store.ReportBranding{})
	for _, want := range []string{"size:A4", "counter(page)", "p:has(+pre)"} {
		if !strings.Contains(html, want) {
			t.Fatalf("print CSS missing %q", want)
		}
	}
}
