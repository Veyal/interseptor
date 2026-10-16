package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/pdfexport"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/version"
)

// reportFixture inserts a flow whose captured headers carry credentials and
// attaches it to a finding as evidence. Values are invented; example.com only.
func reportFixture(t *testing.T, s *store.Store) (findingID int64, secrets []string) {
	t.Helper()
	flowID, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/invoices/8", Status: 200,
		ReqHeaders: map[string][]string{
			"Authorization":       {"Bearer tok-aaaabbbbccccdddd"},
			"Cookie":              {"sid=cookie-1111222233334444"},
			"Proxy-Authorization": {"Basic cHJveHk6c2VjcmV0dmFsdWU="},
			"Accept":              {"application/json"},
		},
		ResHeaders: map[string][]string{
			"Set-Cookie":   {"sid=rotated-5555666677778888; HttpOnly"},
			"Content-Type": {"application/json"},
		},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	f := &store.Finding{
		Severity: "High", Status: "verified", Title: "IDOR on /invoices/{id}",
		Summary: "A reader reaches another tenant's invoice.",
		Impact:  "Cross-tenant read of invoice records.",
		Why:     "The object reference is not checked against the session tenant.",
		Fix:     "Check tenant ownership before returning an invoice.",
		Retest:  "Repeat the request as the other tenant; expect 403.",
		Cwe:     "CWE-639",
		Target:  "https://example.com/invoices/8",
	}
	id, err := s.CreateFinding(f, store.FindingChange{Actor: "test", Source: "test"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlowWithMetadata(id, flowID, "evidence", -1, "action",
		"Reads another tenant's invoice.", "captured_flow", 0,
		store.FindingChange{Actor: "test", Source: "test"}); err != nil {
		t.Fatalf("AttachFlowWithMetadata: %v", err)
	}
	return id, []string{
		"tok-aaaabbbbccccdddd", "cookie-1111222233334444",
		"cHJveHk6c2VjcmV0dmFsdWU=", "rotated-5555666677778888",
	}
}

func getReport(t *testing.T, base, query string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(base + "/api/findings/report" + query)
	if err != nil {
		t.Fatalf("GET %s: %v", query, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestReportExportKeepsCredentialHeadersByDefault(t *testing.T) {
	h, s, _ := newHub(t)
	_, secrets := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, format := range []string{"", "&format=html", "&format=json"} {
		resp, body := getReport(t, ts.URL, "?statuses=all"+format)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", format, resp.StatusCode)
		}
		if resp.Header.Get("X-Interseptor-Report-Masked") != "" {
			t.Errorf("%s: masked header set on an unmasked export", format)
		}
		for _, secret := range secrets {
			if !strings.Contains(body, secret) {
				t.Errorf("%s: default export dropped %q", format, secret)
			}
		}
	}
}

func TestReportExportMasksCredentialHeadersOnRequest(t *testing.T) {
	h, s, _ := newHub(t)
	_, secrets := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, format := range []string{"", "&format=html", "&format=json"} {
		resp, body := getReport(t, ts.URL, "?statuses=all&maskHeaders=1"+format)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", format, resp.StatusCode, body)
		}
		if resp.Header.Get("X-Interseptor-Report-Masked") != "1" {
			t.Errorf("%s: masked export is not labelled", format)
		}
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s: masked export leaked %q", format, secret)
			}
		}
		if !strings.Contains(body, "[redacted") {
			t.Errorf("%s: no placeholder in masked export:\n%s", format, body)
		}
		// Non-credential evidence and provenance survive the pass.
		for _, keep := range []string{"GET /invoices/8", "IDOR on /invoices/{id}"} {
			if !strings.Contains(body, keep) {
				t.Errorf("%s: masked export dropped %q", format, keep)
			}
		}
	}
}

func TestReportExportMaskingDoesNotPersist(t *testing.T) {
	h, s, _ := newHub(t)
	id, secrets := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	if _, body := getReport(t, ts.URL, "?statuses=all&maskHeaders=1"); !strings.Contains(body, "[redacted") {
		t.Fatal("masked export did not mask")
	}
	// The stored finding is untouched: masking is an export-time pass only.
	resp, err := http.Get(ts.URL + "/api/findings/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(stored), "[redacted") {
		t.Fatal("masking leaked into the stored finding")
	}
	_, after := getReport(t, ts.URL, "?statuses=all")
	for _, secret := range secrets {
		if !strings.Contains(after, secret) {
			t.Fatalf("a later unmasked export lost %q — masking mutated stored state", secret)
		}
	}
}

func TestReportExportRejectsUnknownFormat(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, body := getReport(t, ts.URL, "?format=docx")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "format must be") {
		t.Fatalf("unhelpful error: %s", body)
	}
}

// stubPDF replaces the browser-backed renderer for the duration of a test.
func stubPDF(t *testing.T, fn func(ctx context.Context, html []byte) ([]byte, error)) {
	t.Helper()
	prev := newReportPDFRenderer
	newReportPDFRenderer = func() reportPDFRenderer { return stubRenderer(fn) }
	t.Cleanup(func() { newReportPDFRenderer = prev })
}

type stubRenderer func(ctx context.Context, html []byte) ([]byte, error)

func (s stubRenderer) Render(ctx context.Context, html []byte) ([]byte, error) { return s(ctx, html) }

func TestReportExportPDFPrintsTheHTMLReport(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	var printed string
	stubPDF(t, func(_ context.Context, html []byte) ([]byte, error) {
		printed = string(html)
		return []byte("%PDF-1.4\n%%EOF\n"), nil
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content-type %q", ct)
	}
	if !strings.HasPrefix(body, "%PDF-") {
		t.Errorf("body is not a PDF: %q", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), ".pdf") {
		t.Errorf("disposition %q", resp.Header.Get("Content-Disposition"))
	}

	// The PDF is the HTML report printed, so the two must be the same bytes.
	_, htmlBody := getReport(t, ts.URL, "?statuses=all&format=html")
	if printed != htmlBody {
		t.Error("PDF printed a different document than the HTML export")
	}
}

func TestReportExportPDFHonoursMasking(t *testing.T) {
	h, s, _ := newHub(t)
	_, secrets := reportFixture(t, s)
	var printed string
	stubPDF(t, func(_ context.Context, html []byte) ([]byte, error) {
		printed = string(html)
		return []byte("%PDF-1.4\n%%EOF\n"), nil
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	if resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf&maskHeaders=1"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	for _, secret := range secrets {
		if strings.Contains(printed, secret) {
			t.Errorf("masked PDF source leaked %q", secret)
		}
	}
}

func TestReportExportPDFWithoutBrowserSaysSo(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	stubPDF(t, func(context.Context, []byte) ([]byte, error) {
		return nil, fmt.Errorf("%w: set %s to a Chrome or Chromium binary", pdfexport.ErrNoBrowser, pdfexport.EnvBrowser)
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503; body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{"no headless Chrome found", pdfexport.EnvBrowser} {
		if !strings.Contains(body, want) {
			t.Errorf("error does not name %q: %s", want, body)
		}
	}
	if strings.Contains(body, "%PDF") || strings.HasPrefix(body, "<!DOCTYPE") {
		t.Error("a failed PDF export must not fall back to another format")
	}
}

func TestReportExportPDFBrowserFailureIsReported(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	stubPDF(t, func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("pdf export: chrome failed: exit status 1: FATAL: no usable sandbox")
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "no usable sandbox") {
		t.Errorf("the browser's own words are missing: %s", body)
	}
}

func TestReportExportPDFObeysTheFinalGate(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	stubPDF(t, func(context.Context, []byte) ([]byte, error) { return []byte("%PDF-1.4\n"), nil })
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// The fixture is deliberately incomplete, so a final PDF must be refused by
	// the same readiness gate that guards every other format.
	resp, body := getReport(t, ts.URL, "?statuses=all&format=pdf&mode=final")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "%PDF") {
		t.Error("an unready project produced a PDF")
	}
}

func TestReportExportSARIF(t *testing.T) {
	h, s, _ := newHub(t)
	reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getReport(t, ts.URL, "?statuses=all&format=sarif")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/sarif+json") {
		t.Errorf("content-type %q", ct)
	}
	var log map[string]any
	if err := json.Unmarshal([]byte(body), &log); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	if log["version"] != "2.1.0" {
		t.Errorf("version = %v", log["version"])
	}
	runs, _ := log["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %v", log["runs"])
	}
	run, _ := runs[0].(map[string]any)
	results, _ := run["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("want one result, got %d", len(results))
	}
	driver, _ := run["tool"].(map[string]any)["driver"].(map[string]any)
	if driver["version"] != version.String() {
		t.Errorf("driver.version = %v, want %q", driver["version"], version.String())
	}
}
