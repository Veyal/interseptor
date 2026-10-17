package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// A rejected match regex names the field, not a placeholder rule id: the
// engine's compile error ("rule 0 (req-header): ...") was passed through, so
// both a new rule and an edit of rule 7 reported "rule 0".
func TestRuleRegexErrorNamesTheField(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	check := func(name string, resp *http.Response, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, resp.StatusCode)
		}
		msg := string(b)
		if !strings.Contains(msg, "invalid match regex") || strings.Contains(msg, "rule 0") {
			t.Fatalf("%s: error %q, want it to name the match regex and no rule 0", name, msg)
		}
	}

	resp, err := http.Post(ts.URL+"/api/rules", "application/json",
		strings.NewReader(`{"type":"req-header","match":"([","replace":"","enabled":true}`))
	check("create", resp, err)

	resp, err = http.Post(ts.URL+"/api/rules", "application/json",
		strings.NewReader(`{"type":"req-header","match":"a","replace":"b","enabled":true}`))
	if err != nil {
		t.Fatalf("create valid: %v", err)
	}
	resp.Body.Close()
	rules, err := st.ListRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("ListRules = %v, %v", rules, err)
	}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/rules/"+strconv.FormatInt(rules[0].ID, 10), strings.NewReader(`{"type":"req-header","match":"(","replace":"","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	check("update", resp, err)
}
