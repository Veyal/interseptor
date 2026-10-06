package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const critical40 = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"

func postJSON(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFindingCVSSEndpointReturnsSeverityAndExplanation(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, body := postJSON(t, ts.URL+"/api/finding-cvss", `{"vector":"`+critical40+`"}`)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var got struct {
		Severity    string `json:"severity"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Severity != "Critical" || got.Explanation == "" {
		t.Fatalf("body=%s err=%v", body, err)
	}
}

func TestFindingCreateBlocksSeverityMismatchUnlessOverridden(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, body := postJSON(t, ts.URL+"/api/findings", `{"title":"x","severity":"low","cvss":"`+critical40+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "severityOverride") {
		t.Fatalf("mismatch status=%d body=%s, want 400 naming severityOverride", code, body)
	}
	code, body = postJSON(t, ts.URL+"/api/findings", `{"title":"x","severity":"low","cvss":"`+critical40+`","proofReview":{"severityOverride":"Lab-only exposure"}}`)
	if code != http.StatusOK {
		t.Fatalf("override status=%d body=%s", code, body)
	}
}
