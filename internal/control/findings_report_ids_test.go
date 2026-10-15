package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A report can be exported for a chosen set of findings, or a single one, in any
// format, so an operator can send one finding without the rest of the engagement.
func TestFindingsReportIDsSelectsASubset(t *testing.T) {
	h, s, _ := newHub(t)
	var ids []int64
	for _, f := range []store.Finding{
		{Title: "Alpha finding", Severity: "High", Status: "open"},
		{Title: "Bravo finding", Severity: "High", Status: "open"},
		{Title: "Charlie finding", Severity: "Medium", Status: "open"},
	} {
		f := f
		id, err := s.CreateFinding(&f)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := func(query string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/findings/report" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// One finding only.
	code, only := body("?format=json&ids=" + strconv.FormatInt(ids[1], 10))
	if code != http.StatusOK {
		t.Fatalf("single-id export status = %d", code)
	}
	var doc struct {
		Findings []struct {
			Title string `json:"title"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(only), &doc); err != nil {
		t.Fatalf("decode report: %v\n%s", err, only)
	}
	if len(doc.Findings) != 1 || doc.Findings[0].Title != "Bravo finding" {
		t.Fatalf("ids=<bravo> should export exactly Bravo, got %+v", doc.Findings)
	}

	// A chosen subset, in report order (Alpha before Charlie), in Markdown too.
	code, md := body("?ids=" + strconv.FormatInt(ids[2], 10) + "," + strconv.FormatInt(ids[0], 10))
	if code != http.StatusOK {
		t.Fatalf("subset export status = %d", code)
	}
	if !strings.Contains(md, "Alpha finding") || !strings.Contains(md, "Charlie finding") {
		t.Errorf("subset report missing a selected finding:\n%s", md)
	}
	if strings.Contains(md, "Bravo finding") {
		t.Errorf("subset report leaked an unselected finding:\n%s", md)
	}
	if ai, ci := strings.Index(md, "Alpha finding"), strings.Index(md, "Charlie finding"); ai > ci {
		t.Errorf("ids export did not keep report order (Alpha should precede Charlie)")
	}

	// ids that match nothing is a 404, so a typo does not silently export all.
	if code, _ := body("?ids=999999"); code != http.StatusNotFound {
		t.Errorf("ids matching no finding should be 404, got %d", code)
	}
	// A malformed ids value is a 400.
	if code, _ := body("?ids=abc"); code != http.StatusBadRequest {
		t.Errorf("non-numeric ids should be 400, got %d", code)
	}
}
