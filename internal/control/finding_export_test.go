package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getExport(t *testing.T, base string, id int64, query string) (*http.Response, string) {
	t.Helper()
	url := fmt.Sprintf("%s/api/finding-export/%d%s", base, id, query)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestFindingExportShapesEachTarget(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getExport(t, ts.URL, id, "?format=hackerone")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hackerone status=%d body=%s", resp.StatusCode, body)
	}
	var h1 struct {
		Title                    string `json:"title"`
		VulnerabilityInformation string `json:"vulnerability_information"`
		Impact                   string `json:"impact"`
		SeverityRating           string `json:"severity_rating"`
	}
	if err := json.Unmarshal([]byte(body), &h1); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if h1.Title != "IDOR on /invoices/{id}" || h1.SeverityRating != "high" {
		t.Fatalf("hackerone payload: %+v", h1)
	}
	if !strings.Contains(h1.VulnerabilityInformation, "GET /invoices/8 HTTP/1.1") {
		t.Error("raw evidence missing from the narrative")
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "hackerone.json") {
		t.Errorf("disposition %q", resp.Header.Get("Content-Disposition"))
	}

	resp, body = getExport(t, ts.URL, id, "?format=bugcrowd")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bugcrowd status=%d body=%s", resp.StatusCode, body)
	}
	var bc struct {
		BugURL   string `json:"bug_url"`
		Severity int    `json:"severity"`
	}
	json.Unmarshal([]byte(body), &bc)
	if bc.BugURL != "https://example.com/invoices/8" || bc.Severity != 2 {
		t.Fatalf("bugcrowd payload: %+v", bc)
	}

	resp, body = getExport(t, ts.URL, id, "?format=jira")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("jira status=%d body=%s", resp.StatusCode, body)
	}
	var jira struct {
		Fields struct {
			Summary   string `json:"summary"`
			IssueType struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Priority *struct {
				Name string `json:"name"`
			} `json:"priority"`
			Labels []string `json:"labels"`
		} `json:"fields"`
	}
	json.Unmarshal([]byte(body), &jira)
	if jira.Fields.IssueType.Name != "Bug" || jira.Fields.Priority == nil || jira.Fields.Priority.Name != "High" {
		t.Fatalf("jira payload: %+v", jira.Fields)
	}
	if strings.Contains(body, `"project"`) {
		t.Error("the caller's Jira project key must not be invented")
	}

	resp, body = getExport(t, ts.URL, id, "?format=sarif")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sarif status=%d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/sarif+json") {
		t.Errorf("sarif content-type %q", ct)
	}
	var log map[string]any
	if err := json.Unmarshal([]byte(body), &log); err != nil {
		t.Fatalf("sarif is not JSON: %v", err)
	}
	if log["version"] != "2.1.0" {
		t.Errorf("sarif version = %v", log["version"])
	}
}

func TestFindingExportMasksOnRequest(t *testing.T) {
	h, s, _ := newHub(t)
	id, secrets := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, format := range []string{"hackerone", "bugcrowd", "jira"} {
		resp, body := getExport(t, ts.URL, id, "?format="+format+"&maskHeaders=1")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", format, resp.StatusCode)
		}
		if resp.Header.Get("X-Interseptor-Report-Masked") != "1" {
			t.Errorf("%s: masked export not labelled", format)
		}
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s: leaked %q", format, secret)
			}
		}
	}
	// A ticket on a third-party platform masks credentials by default; the
	// verbatim evidence needs an explicit maskHeaders=0.
	resp, masked := getExport(t, ts.URL, id, "?format=hackerone")
	if resp.Header.Get("X-Interseptor-Report-Masked") != "1" {
		t.Error("default tracker export is not masked")
	}
	for _, secret := range secrets {
		if strings.Contains(masked, secret) {
			t.Errorf("default tracker export leaked %q", secret)
		}
	}
	_, plain := getExport(t, ts.URL, id, "?format=hackerone&maskHeaders=0")
	if !strings.Contains(plain, secrets[0]) {
		t.Error("maskHeaders=0 should carry evidence verbatim")
	}
}

func TestFindingExportRequiresAFormat(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, query := range []string{"", "?format=", "?format=csv"} {
		resp, body := getExport(t, ts.URL, id, query)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status=%d, want 400", query, resp.StatusCode)
		}
		if !strings.Contains(body, "format must be") {
			t.Errorf("%q: unhelpful error %s", query, body)
		}
	}
}

func TestFindingExportRefusesDismissedFindings(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	patch, _ := http.NewRequest(http.MethodPatch,
		fmt.Sprintf("%s/api/findings/%d", ts.URL, id),
		strings.NewReader(`{"status":"false_positive"}`))
	patch.Header.Set("Content-Type", "application/json")
	presp, err := http.DefaultClient.Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()

	resp, body := getExport(t, ts.URL, id, "?format=hackerone")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "false_positive") {
		t.Errorf("error does not explain itself: %s", body)
	}
}

func TestFindingExportFinalModeKeepsTheEvidenceGate(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, body := getExport(t, ts.URL, id, "?format=hackerone&mode=final")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("an incomplete finding must not pass a final export: status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "quality") {
		t.Errorf("the refusal should list the failing checks: %s", body)
	}
	// Draft still exports.
	if resp, _ := getExport(t, ts.URL, id, "?format=hackerone&mode=draft"); resp.StatusCode != http.StatusOK {
		t.Fatalf("draft status=%d", resp.StatusCode)
	}
	if resp, body := getExport(t, ts.URL, id, "?format=hackerone&mode=typo"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad mode: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestFindingExportMissingFinding(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	if resp, _ := getExport(t, ts.URL, 9999, "?format=jira"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestFindingExportCanOmitBodies(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := reportFixture(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	_, body := getExport(t, ts.URL, id, "?format=jira&includeBodies=0")
	if strings.Contains(body, "GET /invoices/8 HTTP/1.1") {
		t.Error("includeBodies=0 still carried raw evidence")
	}
	if !strings.Contains(body, "IDOR on /invoices/{id}") {
		t.Error("the finding itself went missing")
	}
}
