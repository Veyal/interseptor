package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A path pattern wrapped in slashes ("/admin/") is read as an unanchored regex
// and also matches "/x/admin/y". Nothing told the operator; the API now says how
// each rule's path is matched and warns when a plain-looking directory is a regex.
func TestScopeRulesReportHowTheirPathIsMatched(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	post := func(body string) map[string]any {
		t.Helper()
		resp, err := http.Post(ts.URL+"/api/scope", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s: status %d", body, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	dir := post(`{"action":"include","enabled":true,"host":"example.com","path":"/admin/"}`)
	if dir["pathKind"] != "regex" {
		t.Fatalf("pathKind for /admin/ = %v, want regex", dir["pathKind"])
	}
	warn, _ := dir["pathWarning"].(string)
	if !strings.Contains(warn, "^/admin/") {
		t.Fatalf("warning must name the anchored form, got %q", warn)
	}
	if dir["path"] != "/admin/" || dir["host"] != "example.com" || dir["id"] == nil {
		t.Fatalf("the rule's own fields must be unchanged: %v", dir)
	}
	prefix := post(`{"action":"include","enabled":true,"host":"example.com","path":"/api"}`)
	if prefix["pathKind"] != "prefix" || prefix["pathWarning"] != nil {
		t.Fatalf("/api: %v", prefix)
	}
	noPath := post(`{"action":"exclude","enabled":true,"host":"static.example.com"}`)
	if noPath["pathKind"] != "any" || noPath["pathWarning"] != nil {
		t.Fatalf("no path: %v", noPath)
	}

	resp, err := http.Get(ts.URL + "/api/scope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range list.Rules {
		kinds[r["path"].(string)], _ = r["pathKind"].(string)
	}
	if kinds["/admin/"] != "regex" || kinds["/api"] != "prefix" || kinds[""] != "any" {
		t.Fatalf("list kinds = %v", kinds)
	}
}
