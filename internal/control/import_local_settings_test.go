package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// QA round 3: restoring a full archive left proxy auth enabled with an empty
// password (every client gets 407) and Settings looked healthy. Restore fails
// closed (auth stays on) but says so: the restore result names the passwords
// that were not in the backup, Settings flags them, and readiness is not green.
func TestFullArchiveRestoreReportsProxyPasswordsNotIncluded(t *testing.T) {
	h, st, _ := newHub(t)
	for k, v := range map[string]string{
		"upstream.proxy":     "http://bob:s3cretPW@proxy.example.com:3128",
		proxyAuthEnabledKey:  "1",
		proxyAuthUserKey:     "alice",
		proxyAuthPasswordKey: "listenerPW",
	} {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	er, err := http.Get(ts.URL + "/api/export/full")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(er.Body)
	er.Body.Close()

	h2, _, _ := newHub(t)
	h2.GlobalDir = t.TempDir()
	ts2 := httptest.NewServer(h2.Handler())
	defer ts2.Close()
	ir, err := http.Post(ts2.URL+"/api/import/full?name=restored", "application/zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer ir.Body.Close()
	if ir.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(ir.Body)
		t.Fatalf("import status %d: %s", ir.StatusCode, b)
	}
	var res struct {
		CredentialsNotRestored []string `json:"credentialsNotRestored"`
		Notice                 string   `json:"notice"`
	}
	if err := json.NewDecoder(ir.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.CredentialsNotRestored, ",") != "proxyAuthPassword,upstreamProxyPassword" {
		t.Fatalf("credentialsNotRestored = %v", res.CredentialsNotRestored)
	}
	if !strings.Contains(res.Notice, "not included") || !strings.Contains(res.Notice, "re-enter") {
		t.Fatalf("notice = %q", res.Notice)
	}

	restored, err := store.Open(filepath.Join(h2.GlobalDir, "projects", "restored"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	h3 := New(restored, nil, nil, nil, nil)
	defer h3.Close()
	rec := httptest.NewRecorder()
	(&settingsAPI{h3}).getSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var s settingsJSON
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if !s.ProxyAuthEnabled || !s.ProxyAuthPasswordMissing || !s.UpstreamProxyPasswordMissing {
		t.Fatalf("restored settings: enabled=%v authMissing=%v upstreamMissing=%v", s.ProxyAuthEnabled, s.ProxyAuthPasswordMissing, s.UpstreamProxyPasswordMissing)
	}
	rep := (&authzAPI{h3}).buildReadiness()
	var authCheck *readinessCheck
	for i := range rep.Checks {
		if rep.Checks[i].ID == "proxy_auth" {
			authCheck = &rep.Checks[i]
		}
	}
	if authCheck == nil || authCheck.OK || !strings.Contains(authCheck.Detail, "no password") {
		t.Fatalf("readiness proxy_auth = %+v", authCheck)
	}
}

// Saving the upstream proxy again (with or without a password) clears the
// "password not included in the backup" marker.
func TestUpstreamPasswordMissingClearsOnSave(t *testing.T) {
	h, st, _ := newHub(t)
	for k, v := range map[string]string{"upstream.proxy": "http://bob@proxy.example.com:3128", upstreamPasswordRemovedKey: "1"} {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	get := func() settingsJSON {
		rec := httptest.NewRecorder()
		(&settingsAPI{h}).getSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var s settingsJSON
		json.NewDecoder(rec.Body).Decode(&s)
		return s
	}
	if !get().UpstreamProxyPasswordMissing {
		t.Fatal("marker not reported")
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(`{"upstreamProxy":"http://bob:pw@proxy.example.com:3128"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status %d", resp.StatusCode)
	}
	if get().UpstreamProxyPasswordMissing {
		t.Fatal("marker survived a save")
	}
}

// QA round 3: Settings → Import project replaced the local upstream proxy with
// the bundle's (Direct → 502). A portable import keeps this machine's network
// settings and says which ones it kept.
func TestPortableImportKeepsLocalNetworkSettings(t *testing.T) {
	h, st, _ := newHub(t)
	localCA := strings.TrimSpace(testCertificatePEM(t))
	if err := st.SetSetting("upstream.proxyCA", localCA); err != nil {
		t.Fatal(err)
	}
	upstreamCalled, caCalled := false, false
	h.Upstream = func(string) error { upstreamCalled = true; return nil }
	h.SetUpstreamProxyCA = func([]byte) error { caCalled = true; return nil }
	payload, _ := json.Marshal(projectBundle{Version: "2", Settings: map[string]string{
		"upstream.proxy": "http://bob@127.0.0.1:18999", "upstream.proxyCA": "-----BEGIN CERTIFICATE-----\nb3RoZXI=\n-----END CERTIFICATE-----",
	}})
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if upstreamCalled || caCalled {
		t.Fatalf("import applied network settings: upstream=%v ca=%v", upstreamCalled, caCalled)
	}
	if v, ok, _ := st.GetSetting("upstream.proxy"); ok && v != "" {
		t.Fatalf("local upstream replaced by %q", v)
	}
	if v, _, _ := st.GetSetting("upstream.proxyCA"); v != localCA {
		t.Fatal("local upstream CA replaced")
	}
	var res struct {
		KeptLocalSettings []string `json:"keptLocalSettings"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if strings.Join(res.KeptLocalSettings, ",") != "upstream.proxy,upstream.proxyCA" {
		t.Fatalf("keptLocalSettings = %v", res.KeptLocalSettings)
	}
}

// QA round 3: importing the same bundle twice duplicated every flow, rule and
// scope rule. Import is idempotent.
func TestPortableImportTwiceIsIdempotent(t *testing.T) {
	h, st, _ := newHub(t)
	api := &projectAPI{h}
	bodyHash, _ := api.storeBody([]byte(`{"a":1}`))
	if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1700000000123), Method: "POST", Scheme: "https", Host: "app.example.com",
		Port: 443, Path: "/login", Status: 200, ResBodyHash: bodyHash, ResLen: 7}); err != nil {
		t.Fatal(err)
	}
	st.CreateRule(&store.Rule{Enabled: true, Type: "req-header", Match: "A: .*", Replace: "A: b"})
	st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.example.com"})
	export := httptest.NewRecorder()
	api.exportProject(export, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	bundle := export.Body.Bytes()

	h2, st2, _ := newHub(t)
	type result struct {
		ImportedFlows, ImportedRules, ImportedScope int
		SkippedFlows, SkippedRules, SkippedScope    int
	}
	imp := func() result {
		rec := httptest.NewRecorder()
		(&projectAPI{h2}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(bundle)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var r result
		json.Unmarshal(rec.Body.Bytes(), &r)
		return r
	}
	if r := imp(); r.ImportedFlows != 1 || r.ImportedRules != 1 || r.ImportedScope != 1 {
		t.Fatalf("first import %+v", r)
	}
	if r := imp(); r.ImportedFlows != 0 || r.ImportedRules != 0 || r.ImportedScope != 0 || r.SkippedFlows != 1 || r.SkippedRules != 1 || r.SkippedScope != 1 {
		t.Fatalf("second import %+v", r)
	}
	if n, _ := st2.FlowCount(); n != 1 {
		t.Fatalf("flows = %d, want 1", n)
	}
	if rules, _ := st2.ListRules(); len(rules) != 1 {
		t.Fatalf("rules = %d", len(rules))
	}
	if sc, _ := st2.ListScopeRules(); len(sc) != 1 {
		t.Fatalf("scope = %d", len(sc))
	}
}

// QA round 3: PUT /api/notes without a notes key wiped the notebook.
func TestPutNotesRequiresNotesKey(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.PersistNotes("keep me"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"md":"x"}`, `{"notes":null}`} {
		rec := httptest.NewRecorder()
		(&projectAPI{h}).putNotes(rec, httptest.NewRequest(http.MethodPut, "/api/notes", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, rec.Code)
		}
	}
	if got, _ := st.LoadNotes(); got != "keep me" {
		t.Fatalf("notes = %q", got)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).putNotes(rec, httptest.NewRequest(http.MethodPut, "/api/notes", strings.NewReader(`{"notes":""}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("explicit empty notes: status %d", rec.Code)
	}
}

// QA round 3: scope POST accepted port 70000, scheme "gopher" and exact
// duplicates; DELETE of a missing rule returned 204.
func TestScopeValidationDuplicatesAndMissingDelete(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	post := func(body string) (int, map[string]any) {
		resp, err := http.Post(ts.URL+"/api/scope", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	for _, body := range []string{
		`{"action":"include","host":"example.com","port":70000,"enabled":true}`,
		`{"action":"include","host":"example.com","port":-1,"enabled":true}`,
		`{"action":"include","host":"example.com","scheme":"gopher","enabled":true}`,
	} {
		if code, _ := post(body); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, code)
		}
	}
	if code, _ := post(`{"action":"include","host":"example.com","scheme":"HTTPS","port":8443,"enabled":true}`); code != http.StatusCreated {
		t.Fatalf("valid rule: status %d", code)
	}
	code, out := post(`{"action":"include","host":"example.com","scheme":"https","port":8443,"enabled":true}`)
	if code != http.StatusConflict {
		t.Fatalf("duplicate: status %d, want 409", code)
	}
	if rule, ok := out["rule"].(map[string]any); !ok || rule["id"] == nil {
		t.Fatalf("duplicate response lacks the existing rule: %v", out)
	}
	if sc, _ := st.ListScopeRules(); len(sc) != 1 {
		t.Fatalf("scope rules = %d, want 1", len(sc))
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/scope/99999", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing: status %d, want 404", resp.StatusCode)
	}
}
