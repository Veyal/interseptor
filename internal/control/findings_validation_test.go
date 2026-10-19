package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func findingReq(t *testing.T, base, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestFindingMissingIDIs404ForPatchAndDelete(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	if code, body := findingReq(t, ts.URL, http.MethodPatch, "/api/findings/999", `{"title":"x"}`); code != http.StatusNotFound {
		t.Fatalf("PATCH missing = %d %s, want 404", code, body)
	}
	if code, body := findingReq(t, ts.URL, http.MethodDelete, "/api/findings/999", ``); code != http.StatusNotFound {
		t.Fatalf("DELETE missing = %d %s, want 404", code, body)
	}
}

func TestFindingTitleAndEnumValidation(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	long := strings.Repeat("a", maxFindingTitleRunes+1)
	for _, title := range []string{``, `   `, long} {
		code, body := findingReq(t, ts.URL, http.MethodPost, "/api/findings", `{"title":"`+title+`"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "title") {
			t.Fatalf("POST title %q = %d %s, want 400 title", title, code, body)
		}
	}
	for _, bad := range []string{`{"title":"t","severity":"nope"}`, `{"title":"t","status":"zzz"}`} {
		code, body := findingReq(t, ts.URL, http.MethodPost, "/api/findings", bad)
		if code != http.StatusBadRequest || !strings.Contains(body, "must be one of") {
			t.Fatalf("POST %s = %d %s, want 400 allowed values", bad, code, body)
		}
	}
	code, body := findingReq(t, ts.URL, http.MethodPost, "/api/findings", `{"title":"  ok title  ","severity":"high","status":"fp"}`)
	if code != http.StatusOK || !strings.Contains(body, `"title":"ok title"`) {
		t.Fatalf("valid create = %d %s", code, body)
	}
	for _, bad := range []string{`{"title":""}`, `{"title":"   "}`, `{"title":"` + long + `"}`, `{"severity":"Nope","status":"zzz"}`, `{"severity":""}`, `{"status":"zzz"}`} {
		code, body := findingReq(t, ts.URL, http.MethodPatch, "/api/findings/1", bad)
		if code != http.StatusBadRequest {
			t.Fatalf("PATCH %.40s = %d %s, want 400", bad, code, body)
		}
	}
	code, body = findingReq(t, ts.URL, http.MethodPatch, "/api/findings/1", `{"title":"Renamed","severity":"Low","status":"verified"}`)
	if code != http.StatusOK || !strings.Contains(body, `"title":"Renamed"`) {
		t.Fatalf("valid patch = %d %s", code, body)
	}
}
