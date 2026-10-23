package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A PATCH carrying only a new CVSS 4.0 vector takes the calculated severity
// instead of failing as a severity conflict.
func TestPatchFindingCVSSOnlyDerivesSeverity(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, body := findingReq(t, ts.URL, http.MethodPost, "/api/findings", `{"title":"t","severity":"Medium"}`)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, body)
	}
	const critical = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"
	code, body = findingReq(t, ts.URL, http.MethodPatch, "/api/findings/1", `{"cvss":"`+critical+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"severity":"Critical"`) {
		t.Fatalf("cvss-only patch = %d %s, want 200 with Critical", code, body)
	}
}
