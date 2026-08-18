package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestExtractAuthHeaders(t *testing.T) {
	h := map[string][]string{
		"Cookie":        {"session=abc"},
		"Authorization": {"Bearer tok"},
		"X-Api-Key":     {"secret"},
		"Accept":        {"*/*"},
	}
	got := extractAuthHeaders(h)
	if !strings.Contains(got, "Cookie: session=abc") {
		t.Fatalf("cookie missing: %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer tok") {
		t.Fatalf("auth missing: %q", got)
	}
	if !strings.Contains(got, "X-Api-Key: secret") {
		t.Fatalf("api key missing: %q", got)
	}
}

func TestGetAuthzSurfacesStoreFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/authz", nil)
	rec := httptest.NewRecorder()
	(&authzAPI{h}).getAuthz(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q, want 500", rec.Code, rec.Body.String())
	}
}

func TestAuthzPromoteFromFlowRejectsTrailingJSONBeforePersistence(t *testing.T) {
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "example.com", Path: "/account",
		ReqHeaders: map[string][]string{"Cookie": {"session=example"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/authz/from-flow/"+strconv.FormatInt(flowID, 10), "application/json",
		strings.NewReader(`{"name":"Admin","merge":true}{}`))
	if err != nil {
		t.Fatalf("POST promote auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if value, ok, err := st.GetSetting("authz.identities"); err != nil || ok {
		t.Fatalf("authz identities changed after rejected command: value=%q ok=%v err=%v", value, ok, err)
	}
}

func TestApplyIdentityHeadersAnonymousStripsAuth(t *testing.T) {
	base := map[string][]string{
		"Cookie":        {"admin=1"},
		"Authorization": {"Bearer x"},
		"Accept":        {"*/*"},
	}
	out := applyIdentityHeaders(base, identity{Name: "anon", Headers: ""})
	if _, ok := out["Cookie"]; ok {
		t.Fatal("Cookie should be stripped for anonymous identity")
	}
	if _, ok := out["Authorization"]; ok {
		t.Fatal("Authorization should be stripped for anonymous identity")
	}
	if out["Accept"][0] != "*/*" {
		t.Fatal("non-auth headers should remain")
	}
}

func TestApplyIdentityHeadersOverridesCookie(t *testing.T) {
	base := map[string][]string{"Cookie": {"admin=1"}}
	out := applyIdentityHeaders(base, identity{Name: "user", Headers: "Cookie: user=2"})
	if out["Cookie"][0] != "user=2" {
		t.Fatalf("got %q", out["Cookie"][0])
	}
}

func TestSessionInvalid401(t *testing.T) {
	// A 401/403 accompanied by an actual auth-challenge signal (WWW-Authenticate,
	// or a redirect to a login page) is real evidence of session/credential
	// expiry.
	challenge := map[string][]string{"Www-Authenticate": {`Bearer realm="api"`}}
	if !sessionLooksInvalid(401, true, challenge) {
		t.Fatal("401 with auth + WWW-Authenticate should be invalid")
	}
	if sessionLooksInvalid(401, false, challenge) {
		t.Fatal("401 without auth is not a session error")
	}
	if sessionLooksInvalid(200, true, challenge) {
		t.Fatal("200 should be valid")
	}
	redirect := map[string][]string{"Location": {"/login?returnTo=/api/me"}}
	if !sessionLooksInvalid(302, true, redirect) {
		t.Fatal("redirect to a login page should be invalid")
	}
}

// A bare 403/401 with NO auth-challenge evidence is exactly the ambiguous case
// this fix addresses: right after a developer fixes an IDOR, the tool's own
// success (access now correctly denied) must not be reported the same way as
// "your session died" — sessionLooksInvalid must not fire without evidence.
func TestSessionInvalidRequiresEvidenceNotBareStatus(t *testing.T) {
	plainForbidden := map[string][]string{"Content-Type": {"application/json"}}
	if sessionLooksInvalid(403, true, plainForbidden) {
		t.Fatal("bare 403 with no auth-challenge signal should NOT be reported as sessionInvalid — it's ambiguous with correctly-working access control")
	}
	if sessionLooksInvalid(401, true, plainForbidden) {
		t.Fatal("bare 401 with no auth-challenge signal should NOT be reported as sessionInvalid")
	}
	// Access is still flagged as denied — the information isn't dropped, just
	// correctly labeled as ambiguous rather than "session expired".
	if !accessDenied(403, true) {
		t.Fatal("403 with auth should still be surfaced as access-denied")
	}
}

func TestAuthzSkipStatic(t *testing.T) {
	if !authzSkipStatic(&store.Flow{Path: "/app/main.js"}) {
		t.Fatal("expected .js to skip")
	}
	if authzSkipStatic(&store.Flow{Path: "/api/users"}) {
		t.Fatal("api path should not skip")
	}
}

func TestAuthzSameAccessBodyHash(t *testing.T) {
	base := authzResult{Status: 200, Length: 100, BodyHash: "abc", Mime: "application/json"}
	if !authzSameAccess(200, 100, "abc", "application/json", base) {
		t.Fatal("same hash should match")
	}
	diff := authzResult{Status: 200, Length: 100, BodyHash: "xyz", Mime: "application/json"}
	if authzSameAccess(200, 100, "abc", "application/json", diff) {
		t.Fatal("different hash should not match")
	}
	failed := base
	failed.Error = "response body unavailable"
	if authzSameAccess(200, 100, "abc", "application/json", failed) {
		t.Fatal("errored replay should not match baseline")
	}
}

func TestAuthzReplayReportsIncompleteResponseCapture(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("short"))
	}))
	defer target.Close()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	h, _, _ := newHub(t)
	rr := (&authzAPI{h}).authzReplay(&store.Flow{
		Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
	}, identity{Name: "user"})
	if rr.Error != "response capture incomplete" {
		t.Fatalf("error = %q, want response capture incomplete", rr.Error)
	}
}

func TestAuthzReplaySurfacesSendFailure(t *testing.T) {
	h, _, _ := newHub(t)
	rr := (&authzAPI{h}).authzReplay(&store.Flow{
		Method: "GET\nInvalid", Scheme: "https", Host: "example.com", Port: 443, Path: "/probe",
	}, identity{Name: "user"})
	if rr.Error != "send failed" {
		t.Fatalf("error = %q, want send failed", rr.Error)
	}
}

func TestAuthzReplayRefusesOwnListener(t *testing.T) {
	h, _, _ := newHub(t)
	h.SetSelfAddr("127.0.0.1:9966")
	rr := (&authzAPI{h}).authzReplay(&store.Flow{
		Method: "GET", Scheme: "http", Host: "localhost", Port: 9966, Path: "/api/authz",
	}, identity{Name: "user", Headers: "Authorization: Bearer example"})
	if rr.Error != "refusing to send to Interseptor's own listener" {
		t.Fatalf("error = %q, want own-listener refusal", rr.Error)
	}
}

func TestAuthzRunDoesNotUseErroredReplayAsBaseline(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "broken") {
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("short"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	h, _, _ := newHub(t)
	ro := (&authzAPI{h}).authzRunOne(&store.Flow{
		Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
	}, []identity{
		{Name: "broken", Headers: "Authorization: Bearer broken"},
		{Name: "valid", Headers: "Authorization: Bearer valid"},
	})
	if len(ro.Results) != 2 || ro.Results[0].Error == "" {
		t.Fatalf("results = %+v, want errored first replay", ro.Results)
	}
	if ro.Results[1].Same {
		t.Fatalf("valid replay matched errored baseline: %+v", ro.Results)
	}
}

func TestAuthzRunRequiresScopeForBulk(t *testing.T) {
	h, s, _ := newHub(t)
	_ = s.SetSetting("authz.identities", `[{"name":"admin","headers":"Cookie: a=1"}]`)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body := `{"inScope":true}`
	resp, err := http.Post(ts.URL+"/api/authz/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bulk without include rules: status %d, want 400", resp.StatusCode)
	}
}

func TestSetAuthzRejectsUnsupportedHeaderShapeWithoutPersisting(t *testing.T) {
	h, st, _ := newHub(t)
	previous := `[{"name":"admin","headers":"Authorization: Bearer preserved"}]`
	if err := st.SetSetting("authz.identities", previous); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/authz", "application/json",
		strings.NewReader(`{"identities":[{"name":"admin","headers":123}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	stored, ok, err := st.GetSetting("authz.identities")
	if err != nil || !ok || stored != previous {
		t.Fatalf("stored identities = %q, ok=%v, err=%v; want prior value", stored, ok, err)
	}
}

func TestAuthzCrossHostReplayRejectsUnknownModeBeforeSending(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer captured"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/authz/cross-host-replay", "application/json",
		strings.NewReader(`{"flowId":`+itoa(flowID)+`,"jwt":"header.payload.signature","mode":"barer"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unknown mode sent %d outbound request(s)", got)
	}
}

func TestAuthzReplayCommandsRejectTrailingJSONBeforeSending(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	h, st, _ := newHub(t)
	if err := st.SetSetting("authz.identities", `[{"name":"user","headers":"Authorization: Bearer user"}]`); err != nil {
		t.Fatal(err)
	}
	flowID, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/authz/check-sessions", "/api/authz/run"} {
		requests.Store(0)
		resp, err := http.Post(ts.URL+path, "application/json",
			strings.NewReader(`{"flowId":`+itoa(flowID)+`}{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, resp.StatusCode)
		}
		if got := requests.Load(); got != 0 {
			t.Errorf("%s sent %d outbound request(s)", path, got)
		}
	}
}

func TestAuthzReplayDoesNotSendMissingRequestBody(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, st, _ := newHub(t)
	if err := st.SetSetting("authz.identities", `[{"name":"user","headers":"Authorization: Bearer user"}]`); err != nil {
		t.Fatal(err)
	}
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/authz/check-sessions", strings.NewReader(`{"flowId":`+itoa(flowID)+`}`))
	rec := httptest.NewRecorder()
	(&authzAPI{h}).authzCheckSessions(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "request body unavailable") {
		t.Fatalf("status=%d body=%q, want per-result body error", rec.Code, rec.Body.String())
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("sent %d outbound request(s) despite missing body", got)
	}
}

func TestAuthzCrossHostReplayRejectsMissingRequestBody(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`,"jwt":"a.b.c"}`))
	rec := httptest.NewRecorder()
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("sent %d outbound request(s) despite missing body", got)
	}
}

func TestAuthzCrossHostReplayClassifiesMissingJWTSourceBody(t *testing.T) {
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/probe",
		ResBodyHash: strings.Repeat("a", 64), ResLen: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`}`))
	rec := httptest.NewRecorder()
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestAuthzCrossHostReplayRejectsIncompleteAcceptanceEvidence(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("short"))
	}))
	defer target.Close()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/probe",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`,"jwt":"a.b.c"}`))
	rec := httptest.NewRecorder()
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []struct {
			Accepted bool   `json:"accepted"`
			Error    string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Accepted || out.Results[0].Error != "response capture incomplete" {
		t.Fatalf("results=%+v, want rejected incomplete response", out.Results)
	}
}

func TestAuthzCrossHostReplayRefusesOwnListenerReference(t *testing.T) {
	h, st, _ := newHub(t)
	h.SetSelfAddr("127.0.0.1:9966")
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "http", Host: "127.0.0.1", Port: 9966, Path: "/api/authz",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`,"jwt":"a.b.c"}`))
	rec := httptest.NewRecorder()
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q, want 403", rec.Code, rec.Body.String())
	}
}

func TestAuthzTargetsInScope(t *testing.T) {
	h, s, _ := newHub(t)
	s.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "in.test", Enabled: true})
	h.sc.SetRules(mustListScope(s))

	in, _ := s.InsertFlow(&store.Flow{Method: "GET", Host: "in.test", Path: "/a", Scheme: "https", Port: 443, Status: 200})
	out, _ := s.InsertFlow(&store.Flow{Method: "GET", Host: "out.test", Path: "/b", Scheme: "https", Port: 443, Status: 200})
	targets := (&authzAPI{h}).authzTargets([]*store.Flow{
		mustGetFlow(s, in), mustGetFlow(s, out),
	}, true)
	if len(targets) != 1 || targets[0].ID != in {
		t.Fatalf("expected one in-scope target, got %+v", targets)
	}
}

func TestAuthzFlowAuthEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := s.InsertFlow(&store.Flow{
		Method: "GET", Host: "api.test", Path: "/me", Scheme: "https", Port: 443,
		ReqHeaders: map[string][]string{"Cookie": {"s=1"}, "Authorization": {"Bearer t"}},
		ResHeaders: map[string][]string{"Set-Cookie": {"s=1; Path=/; Expires=Wed, 09 Jun 2027 10:18:14 GMT"}},
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/authz/flow-auth/" + itoa(id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["suggestedHeaders"].(string), "Cookie: s=1") {
		t.Fatalf("suggestedHeaders: %v", body["suggestedHeaders"])
	}
}

func mustListScope(s *store.Store) []store.ScopeRule {
	r, _ := s.ListScopeRules()
	return r
}

func mustGetFlow(s *store.Store, id int64) *store.Flow {
	f, err := s.GetFlow(id)
	if err != nil {
		panic(err)
	}
	return f
}
