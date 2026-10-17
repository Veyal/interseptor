package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindingCreateRejectsLegacyCVSS31(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"legacy","cvss":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "CVSS:4.0/") || !strings.Contains(string(body), "cvss") || !strings.Contains(string(body), "cvssV3") {
		t.Fatalf("status=%d body=%s, want 400 naming cvss, CVSS:4.0/ and the cvssV3 field", resp.StatusCode, body)
	}
}

func TestFindingCVSSV31IsStoredBesideV40OverREST(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, body := doJSON(t, http.MethodPost, ts.URL+"/api/findings",
		`{"title":"IDOR","severity":"High","cvss":"CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N","cvssV3":"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N"}`)
	if code != http.StatusOK || !strings.Contains(body, `"cvssV3":"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N"`) || !strings.Contains(body, `"cvssV3Rating":"MEDIUM"`) {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	code, body = doJSON(t, http.MethodPatch, ts.URL+"/api/findings/1", `{"cvssV3":"CVSS:4.0/AV:N"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "cvssV3") {
		t.Fatalf("bad cvssV3 patch status=%d body=%s", code, body)
	}
	code, body = doJSON(t, http.MethodPatch, ts.URL+"/api/findings/1", `{"cvssV3":""}`)
	if code != http.StatusOK || strings.Contains(body, `"cvssV3"`) {
		t.Fatalf("clear status=%d body=%s", code, body)
	}
}
