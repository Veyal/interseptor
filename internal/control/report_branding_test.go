package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func putJSON(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(out)
}

const brandingBody = `{
  "reportTitle": "Example Corp — External Assessment",
  "companyName": "Example Security",
  "clientName": "Example Corp",
  "companyLogo": "data:image/png;base64,iVBORw0KGgoCOMPANY",
  "engagementName": "Q1 external assessment",
  "assessmentStart": "2026-03-03",
  "assessmentEnd": "2026-03-14",
  "leadAssessor": "A. Assessor",
  "contact": "security@example.com",
  "classification": "Confidential"
}`

func TestReportBrandingRoundTrips(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	get, raw := func() (*http.Response, string) {
		r, err := http.Get(ts.URL + "/api/report-branding")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return r, string(b)
	}()
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d", get.StatusCode)
	}
	var empty store.ReportBranding
	if err := json.Unmarshal([]byte(raw), &empty); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if !empty.IsEmpty() || empty.Version != 0 {
		t.Fatalf("a fresh project should have no branding: %+v", empty)
	}

	put, putBody := putJSON(t, ts.URL+"/api/report-branding", brandingBody)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", put.StatusCode, putBody)
	}
	var saved store.ReportBranding
	if err := json.Unmarshal([]byte(putBody), &saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	if saved.Version != 1 || saved.LeadAssessor != "A. Assessor" {
		t.Fatalf("saved: %+v", saved)
	}
}

func TestReportBrandingRejectsBadInputWithAReason(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := putJSON(t, ts.URL+"/api/report-branding", `{"companyLogo":"https://example.com/logo.png"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "data: image URI") {
		t.Fatalf("unhelpful error: %s", body)
	}
	resp, body = putJSON(t, ts.URL+"/api/report-branding", `{"assessmentStart":"last March"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "ISO date") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestReportBrandingFlowsIntoEveryExport(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	var printed string
	stubPDF(t, func(_ context.Context, html []byte) ([]byte, error) {
		printed = string(html)
		return []byte("%PDF-1.4\n%%EOF\n"), nil
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	if resp, body := putJSON(t, ts.URL+"/api/report-branding", brandingBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, body)
	}

	_, md := getReport(t, ts.URL, "?statuses=all")
	for _, want := range []string{
		"# Example Corp — External Assessment",
		"Prepared by **Example Security** for **Example Corp**.",
		"- **Assessment period:** 2026-03-03 to 2026-03-14",
		"- **Lead assessor:** A. Assessor",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown export missing %q", want)
		}
	}
	if strings.Contains(md, "base64") {
		t.Error("the Markdown export should not carry logo bytes")
	}

	_, html := getReport(t, ts.URL, "?statuses=all&format=html")
	for _, want := range []string{
		"<title>Example Corp — External Assessment</title>",
		`src="data:image/png;base64,iVBORw0KGgoCOMPANY"`,
		`alt="Example Security logo"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML export missing %q", want)
		}
	}

	if resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf"); resp.StatusCode != http.StatusOK {
		t.Fatalf("pdf status=%d body=%s", resp.StatusCode, body)
	}
	if printed != html {
		t.Error("the PDF printed a different document than the branded HTML export")
	}

	_, jsonBody := getReport(t, ts.URL, "?statuses=all&format=json")
	if !strings.Contains(jsonBody, `"reportBrandingVersion":1`) {
		t.Errorf("JSON export does not cite the branding version: %s", jsonBody)
	}
}

func TestReportBrandingUnsetLeavesExportsAlone(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	_, md := getReport(t, ts.URL, "?statuses=all")
	if !strings.HasPrefix(md, "# Interseptor — Engagement Report") {
		t.Errorf("default title changed: %s", md[:60])
	}
	_, html := getReport(t, ts.URL, "?statuses=all&format=html")
	if !strings.Contains(html, "<title>Interseptor — Engagement Report</title>") {
		t.Error("default HTML title changed")
	}
	if strings.Contains(html, "report-brand") {
		t.Error("an unbranded export carries brand markup")
	}
}

func TestReportBrandingCustomCoverPage(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body := `{"reportTitle":"Example Corp — External Assessment","coverPage":"## Scope\n\nTwo applications and one API."}`
	if resp, out := putJSON(t, ts.URL+"/api/report-branding", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, out)
	}
	_, md := getReport(t, ts.URL, "?statuses=all")
	if !strings.Contains(md, "## Scope") || !strings.Contains(md, "Two applications and one API.") {
		t.Fatalf("custom cover missing:\n%s", md[:400])
	}
	_, html := getReport(t, ts.URL, "?statuses=all&format=html")
	if !strings.Contains(html, "<h2>Scope</h2>") {
		t.Errorf("custom cover not rendered in HTML:\n%s", html[:600])
	}
}
