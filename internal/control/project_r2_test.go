package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func importBundle(t *testing.T, h *Hub, bundle string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/project/import", strings.NewReader(bundle)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// C6: a portable import is documented as an additive merge, but a bundle that
// carried authz identities (every export does) overwrote the whole local list.
func TestImportProjectKeepsLocalAuthzIdentities(t *testing.T) {
	h, st, _ := newHub(t)
	local := `[{"name":"admin","headers":"Cookie: sid=local-admin","role":"tenant admin"},{"name":"local-only","headers":"Authorization: Bearer local"}]`
	if err := st.SetSetting(authzIdentitiesSetting, local); err != nil {
		t.Fatal(err)
	}
	incoming := `[{"name":"admin","headers":"Cookie: sid=from-bundle"},{"name":"auditor","headers":"Cookie: sid=auditor"}]`
	settings, _ := json.Marshal(map[string]string{"authz.identities": incoming})
	bundle := `{"version":"1","settings":` + string(settings) + `}`

	code, out := importBundle(t, h, bundle)
	if code != http.StatusOK {
		t.Fatalf("import status %d: %v", code, out)
	}
	ids := (&authzAPI{h}).authzIdentities()
	byName := map[string]identity{}
	for _, id := range ids {
		byName[id.Name] = id
	}
	if len(ids) != 3 {
		t.Fatalf("identities after import: %d (%v), want local 2 + 1 new", len(ids), ids)
	}
	if got := byName["local-only"].Headers; got != "Authorization: Bearer local" {
		t.Fatalf("local-only identity lost or changed: %q", got)
	}
	if got := byName["admin"]; got.Headers != "Cookie: sid=local-admin" || got.Role != "tenant admin" {
		t.Fatalf("local admin identity replaced by the bundle's: %+v", got)
	}
	if got := byName["auditor"].Headers; got != "Cookie: sid=auditor" {
		t.Fatalf("new identity from the bundle not added: %q", got)
	}
	if out["importedIdentities"] != float64(1) || out["keptIdentities"] != float64(1) {
		t.Fatalf("import result: importedIdentities=%v keptIdentities=%v, want 1/1", out["importedIdentities"], out["keptIdentities"])
	}

	// Idempotent: a second import adds nothing.
	if _, out = importBundle(t, h, bundle); out["importedIdentities"] != float64(0) || out["keptIdentities"] != float64(2) {
		t.Fatalf("re-import: importedIdentities=%v keptIdentities=%v, want 0/2", out["importedIdentities"], out["keptIdentities"])
	}
	if n := len((&authzAPI{h}).authzIdentities()); n != 3 {
		t.Fatalf("identities after re-import: %d, want 3", n)
	}
}

// A bundle whose identities do not decode is rejected before anything is applied
// (it used to be stored verbatim and break every later identity read).
func TestImportProjectRejectsUndecodableAuthzIdentities(t *testing.T) {
	h, st, _ := newHub(t)
	settings, _ := json.Marshal(map[string]string{"authz.identities": `{"not":"a list"}`})
	code, _ := importBundle(t, h, `{"version":"1","rules":[{"type":"req-header","match":"a","replace":"b","enabled":true}],"settings":`+string(settings)+`}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
	if rules, _ := st.ListRules(); len(rules) != 0 {
		t.Fatalf("%d rules imported from a rejected bundle", len(rules))
	}
}
