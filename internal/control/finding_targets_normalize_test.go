package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestNormalizeFindingTargetsRouteDryRunThenApply(t *testing.T) {
	h, st, _ := newHub(t)
	handler := h.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost"
		handler.ServeHTTP(r, req)
		return r
	}
	r := do("POST", "/api/findings", `{"title":"Example","targets":[{"url":"https://example.com/users/edit/1","method":"GET"},{"url":"https://example.com/users/edit/2","method":"GET"},{"url":"https://example.com/users/edit/3","method":"POST"}]}`)
	var f store.Finding
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &f) != nil {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	path := "/api/findings/" + strconv.FormatInt(f.ID, 10) + "/normalize-targets"

	// Default is a dry run: nothing persists.
	r = do("POST", path, `{"approve":[0,1,2]}`)
	var out struct {
		DryRun  bool                       `json:"dryRun"`
		Applied bool                       `json:"applied"`
		Preview store.FindingTargetPreview `json:"preview"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.DryRun || out.Applied || len(out.Preview.Targets) != 2 {
		t.Fatalf("dry run: %d %s", r.Code, r.Body.String())
	}
	got, _ := st.GetFinding(f.ID)
	if len(got.Targets) != 3 || got.Targets[0].URL != "https://example.com/users/edit/1" {
		t.Fatalf("dry run persisted: %+v", got.Targets)
	}

	if r = do("POST", path, `{"approve":[9],"dryRun":false}`); r.Code != http.StatusBadRequest {
		t.Fatalf("bad index: %d %s", r.Code, r.Body.String())
	}
	if r = do("POST", "/api/findings/99999/normalize-targets", `{}`); r.Code != http.StatusNotFound {
		t.Fatalf("missing finding: %d", r.Code)
	}

	r = do("POST", path, `{"approve":[0,1,2],"dryRun":false}`)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.Applied {
		t.Fatalf("apply: %d %s", r.Code, r.Body.String())
	}
	got, _ = st.GetFinding(f.ID)
	if len(got.Targets) != 2 || got.Targets[0].URL != "https://example.com/users/edit/{id}" {
		t.Fatalf("apply result: %+v", got.Targets)
	}
}
