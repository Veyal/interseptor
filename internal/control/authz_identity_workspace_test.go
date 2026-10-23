package control

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// ---- helpers ---------------------------------------------------------------

// authzReq issues one control-plane request and decodes the JSON body.
func authzReq(t *testing.T, method, target, body string, hdrs map[string]string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// authzEventSink subscribes to the hub's SSE fan-out without an HTTP stream.
func authzEventSink(t *testing.T, h *Hub) (<-chan string, func()) {
	t.Helper()
	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	stop := func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}
	t.Cleanup(stop)
	return ch, stop
}

// waitForEvent drains the sink until a message containing want arrives.
func waitForEvent(t *testing.T, ch <-chan string, want string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-ch:
			if strings.Contains(msg, want) {
				return msg
			}
		case <-deadline:
			t.Fatalf("no SSE event containing %q", want)
			return ""
		}
	}
}

// identityOf reads one identity from a response body.
func identityOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	id, ok := out["identity"].(map[string]any)
	if !ok {
		t.Fatalf("response has no identity object: %+v", out)
	}
	return id
}

// jwtFor builds an unsigned JWT with the given payload claims.
func jwtFor(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]any{"alg": "HS256", "typ": "JWT"}) + "." + enc(claims) + ".sig"
}

// ---- identity storage contract --------------------------------------------

// TestIdentityBackCompatLoadsLegacyBlob stores the identity list exactly as an
// older build wrote it (no role/createdAt/source/lastCheck) and checks that
// both the list and the new detail endpoint still read it.
func TestIdentityBackCompatLoadsLegacyBlob(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	legacy := `[{"name":"admin","headers":"Authorization: Bearer legacy-token-value","broken":true,` +
		`"brokenNote":"locked","updatedAt":"2024-01-02T03:04:05Z","owner":"agent-a"}]`
	if err := st.SetSetting(authzIdentitiesSetting, legacy); err != nil {
		t.Fatal(err)
	}

	ids, err := (&authzAPI{h}).authzIdentitiesResult()
	if err != nil {
		t.Fatalf("legacy blob failed to load: %v", err)
	}
	if len(ids) != 1 || ids[0].Owner != "agent-a" || !ids[0].Broken || ids[0].Source != nil || ids[0].LastCheck != nil {
		t.Fatalf("legacy identity = %+v", ids)
	}

	code, out := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/admin", "", nil)
	if code != http.StatusOK {
		t.Fatalf("detail status = %d", code)
	}
	id := identityOf(t, out)
	if id["updatedAt"] != "2024-01-02T03:04:05Z" || id["brokenNote"] != "locked" {
		t.Fatalf("detail dropped legacy fields: %+v", id)
	}
	if _, ok := out["auth"]; !ok {
		t.Fatalf("detail missing auth block: %+v", out)
	}
}

func TestIdentityCreatedStampWrittenOnceAndOwnerLabels(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Human/UI caller.
	code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity",
		`{"name":"admin","headers":"Authorization: Bearer admin-token-one"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}
	_, out := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/admin", "", nil)
	first := identityOf(t, out)
	if first["createdBy"] != "you" || first["owner"] != "you" {
		t.Fatalf("human create owner/createdBy = %+v", first)
	}
	createdAt, _ := first["createdAt"].(string)
	if createdAt == "" {
		t.Fatal("createdAt not stamped")
	}
	if src, _ := first["source"].(map[string]any); src == nil || src["kind"] != identitySourceManual {
		t.Fatalf("human create source = %+v", first["source"])
	}

	// An AI/MCP caller updating it must not rewrite the first-create stamp.
	time.Sleep(1100 * time.Millisecond) // RFC3339 is second-resolution
	code, _ = authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity",
		`{"name":"admin","headers":"Authorization: Bearer admin-token-two"}`,
		map[string]string{"X-Interseptor-Source": "ai"})
	if code != http.StatusOK {
		t.Fatalf("update status = %d", code)
	}
	_, out = authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/admin", "", nil)
	second := identityOf(t, out)
	if second["createdAt"] != createdAt || second["createdBy"] != "you" {
		t.Fatalf("createdAt/createdBy overwritten: %+v", second)
	}
	if second["owner"] != identitySourceAgent {
		t.Fatalf("AI write owner = %v, want agent", second["owner"])
	}
	if second["updatedAt"] == createdAt {
		t.Fatal("updatedAt not advanced")
	}

	// An AI create with no owner label is attributed to the agent channel.
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity",
		`{"name":"bot","headers":"Authorization: Bearer bot-token-value"}`,
		map[string]string{"X-Interseptor-Source": "ai"})
	_, out = authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/bot", "", nil)
	bot := identityOf(t, out)
	if bot["createdBy"] != identitySourceAgent {
		t.Fatalf("agent create createdBy = %v", bot["createdBy"])
	}
	if src, _ := bot["source"].(map[string]any); src == nil || src["kind"] != identitySourceAgent {
		t.Fatalf("agent create source = %+v", bot["source"])
	}

	// An explicit owner label (what the MCP tools pass) wins.
	authzReq(t, http.MethodPost, ts.URL+"/api/authz",
		`{"identities":[{"name":"labelled","headers":"Cookie: sid=cookie-value-1"}],"owner":"agent-b"}`,
		map[string]string{"X-Interseptor-Source": "ai"})
	_, out = authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/labelled", "", nil)
	if got := identityOf(t, out)["createdBy"]; got != "agent-b" {
		t.Fatalf("explicit owner createdBy = %v", got)
	}
}

func TestIdentitySourceFromPromoteAndRefresh(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	first := &store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/api/me",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer promoted-token-1"}}}
	firstID, err := st.InsertFlow(first)
	if err != nil {
		t.Fatal(err)
	}
	second := &store.Flow{Method: "POST", Scheme: "https", Host: "example.com", Path: "/api/session",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer refreshed-token-2"}}}
	secondID, err := st.InsertFlow(second)
	if err != nil {
		t.Fatal(err)
	}

	code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/from-flow/"+strconv.FormatInt(firstID, 10), `{"name":"surveyor"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("promote status = %d", code)
	}
	_, out := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/surveyor", "", nil)
	src, _ := identityOf(t, out)["source"].(map[string]any)
	if src == nil || src["kind"] != identitySourceFlow || int64(src["flowId"].(float64)) != firstID || src["path"] != "/api/me" {
		t.Fatalf("promote source = %+v", src)
	}

	code, refreshed := authzReq(t, http.MethodPost,
		ts.URL+"/api/authz/identities/surveyor/refresh-from-flow/"+strconv.FormatInt(secondID, 10), "", nil)
	if code != http.StatusOK {
		t.Fatalf("refresh status = %d: %+v", code, refreshed)
	}
	id := identityOf(t, refreshed)
	if !strings.Contains(id["headers"].(string), "refreshed-token-2") {
		t.Fatalf("refresh did not replace headers: %+v", id)
	}
	src, _ = id["source"].(map[string]any)
	if src == nil || int64(src["flowId"].(float64)) != secondID || src["method"] != "POST" {
		t.Fatalf("refresh source = %+v", src)
	}

	_, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/surveyor", "", nil)
	tl, _ := detail["timeline"].(map[string]any)
	events, _ := tl["events"].([]any)
	kinds := map[string]bool{}
	for _, raw := range events {
		if ev, ok := raw.(map[string]any); ok {
			kinds[ev["kind"].(string)] = true
		}
	}
	if !kinds[identityEventCreated] || !kinds[identityEventRefreshed] {
		t.Fatalf("timeline events = %+v", events)
	}
	if srcRef, _ := tl["source"].(map[string]any); srcRef == nil || int64(srcRef["flowId"].(float64)) != secondID {
		t.Fatalf("timeline source = %+v", tl["source"])
	}

	// Unknown identity and unknown flow are 404s, not silent creates.
	if code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identities/nope/refresh-from-flow/"+strconv.FormatInt(secondID, 10), "", nil); code != http.StatusNotFound {
		t.Fatalf("refresh unknown identity = %d, want 404", code)
	}
	if code, _ := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/nope", "", nil); code != http.StatusNotFound {
		t.Fatalf("detail unknown identity = %d, want 404", code)
	}
}

func TestPatchIdentityRenameKeepsHistoryAndRejectsCollision(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzReq(t, http.MethodPost, ts.URL+"/api/authz",
		`{"identities":[{"name":"admin","headers":"Authorization: Bearer admin-token-one"},`+
			`{"name":"user","headers":"Authorization: Bearer user-token-one"}]}`, nil)

	code, out := authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/admin",
		`{"role":"tenant admin","notes":"shared test account","brokenNote":"","broken":false}`, nil)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d: %+v", code, out)
	}
	id := identityOf(t, out)
	if id["role"] != "tenant admin" || id["notes"] != "shared test account" {
		t.Fatalf("patch result = %+v", id)
	}
	if !strings.Contains(id["headers"].(string), "admin-token-one") {
		t.Fatal("patch without headers must not clear them")
	}

	if code, _ := authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/admin", `{"name":"user"}`, nil); code != http.StatusConflict {
		t.Fatalf("rename collision = %d, want 409", code)
	}
	if code, _ := authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/admin", `{"name":"  "}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty rename = %d, want 400", code)
	}
	if code, _ := authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/ghost", `{"role":"x"}`, nil); code != http.StatusNotFound {
		t.Fatalf("patch unknown = %d, want 404", code)
	}

	code, out = authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/admin", `{"name":"tenant-admin","broken":true,"brokenNote":"rate limited"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("rename status = %d: %+v", code, out)
	}
	if identityOf(t, out)["name"] != "tenant-admin" {
		t.Fatalf("rename result = %+v", out)
	}
	_, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/tenant-admin", "", nil)
	tl, _ := detail["timeline"].(map[string]any)
	events, _ := tl["events"].([]any)
	if len(events) < 2 {
		t.Fatalf("rename lost history: %+v", events)
	}
	last, _ := events[len(events)-1].(map[string]any)
	if last["kind"] != identityEventMarkedBroken {
		t.Fatalf("broken toggle not logged: %+v", events)
	}
	if identityOf(t, detail)["role"] != "tenant admin" {
		t.Fatal("rename dropped role")
	}
}

// ---- detail view ----------------------------------------------------------

func TestIdentityDetailMasksSecretsAndDecodesJWT(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	token := jwtFor(t, map[string]any{
		"sub": "u-42", "iss": "https://issuer.example.com", "aud": []any{"api", "web"},
		"iat":          float64(time.Now().Add(-2 * time.Hour).Unix()),
		"exp":          float64(time.Now().Add(-time.Hour).Unix()),
		"realm_access": map[string]any{"roles": []any{"admin"}},
		"scope":        "read:items write:items", "tenant": "acme",
	})
	headers := "Cookie: sid=super-secret-session-value; csrf=tok\nAuthorization: Bearer " + token
	body, _ := json.Marshal(map[string]any{"name": "admin", "headers": headers})
	if code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", string(body), nil); code != http.StatusOK {
		t.Fatal("create failed")
	}

	// Two captured flows carried the session cookie; one carried another one.
	for i := 0; i < 2; i++ {
		if _, err := st.InsertFlow(&store.Flow{
			Method: "GET", Scheme: "https", Host: "example.com", Path: "/api/items", Status: 200,
			ReqHeaders: map[string][]string{"Cookie": {"sid=super-secret-session-value"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "example.com", Path: "/api/other", Status: 200,
		ReqHeaders: map[string][]string{"Cookie": {"sid=a-different-session-value"}},
	}); err != nil {
		t.Fatal(err)
	}

	code, out := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/admin", "", nil)
	if code != http.StatusOK {
		t.Fatalf("detail status = %d", code)
	}
	raw, _ := json.Marshal(out["auth"])
	if strings.Contains(string(raw), "super-secret-session-value") || strings.Contains(string(raw), token) {
		t.Fatalf("detail leaked a secret: %s", raw)
	}
	auth, _ := out["auth"].(map[string]any)
	if auth["type"] != "bearer" {
		t.Fatalf("auth type = %v, want bearer", auth["type"])
	}
	hdrs, _ := auth["headers"].([]any)
	if len(hdrs) != 2 {
		t.Fatalf("auth headers = %+v", hdrs)
	}
	for _, raw := range hdrs {
		hv, _ := raw.(map[string]any)
		masked, _ := hv["masked"].(string)
		if !strings.Contains(masked, "…") || hv["length"].(float64) == 0 {
			t.Fatalf("header not masked: %+v", hv)
		}
	}
	cookies, _ := auth["cookies"].([]any)
	if len(cookies) != 2 {
		t.Fatalf("cookies = %+v", cookies)
	}
	c0, _ := cookies[0].(map[string]any)
	if c0["name"] != "sid" || c0["fingerprint"] != shortFingerprint("super-secret-session-value") {
		t.Fatalf("cookie view = %+v", c0)
	}
	jwt, _ := auth["jwt"].(map[string]any)
	if jwt == nil || jwt["sub"] != "u-42" || jwt["alg"] != "HS256" || jwt["issuer"] != "https://issuer.example.com" {
		t.Fatalf("jwt = %+v", jwt)
	}
	if jwt["audience"] != "api, web" || jwt["expired"] != true || jwt["expiresIn"].(float64) >= 0 {
		t.Fatalf("jwt expiry = %+v", jwt)
	}
	roles := map[string]bool{}
	for _, r := range jwt["roles"].([]any) {
		roles[r.(string)] = true
	}
	if !roles["admin"] || !roles["read:items"] {
		t.Fatalf("jwt roles = %+v", jwt["roles"])
	}
	claims, _ := jwt["claims"].(map[string]any)
	if claims["tenant"] != "acme" {
		t.Fatalf("claims = %+v", claims)
	}
	if _, nested := claims["realm_access"]; nested {
		t.Fatal("claims must keep top-level scalars only")
	}

	usage, _ := out["usage"].(map[string]any)
	if usage["count"].(float64) != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	recent, _ := usage["recent"].([]any)
	if len(recent) != 2 {
		t.Fatalf("usage recent = %+v", recent)
	}
	row, _ := recent[0].(map[string]any)
	if row["host"] != "example.com" || row["path"] != "/api/items" || row["status"].(float64) != 200 {
		t.Fatalf("usage row = %+v", row)
	}
	if ts, _ := row["ts"].(string); !strings.HasSuffix(ts, "Z") {
		t.Fatalf("usage ts not RFC3339: %+v", row)
	}

	// An anonymous identity reports no credential at all.
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", `{"name":"anon","headers":""}`, nil)
	_, out = authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/anon", "", nil)
	auth, _ = out["auth"].(map[string]any)
	if auth["type"] != "none" || auth["jwt"] != nil {
		t.Fatalf("anonymous auth = %+v", auth)
	}
	if usage, _ := out["usage"].(map[string]any); usage["count"].(float64) != 0 {
		t.Fatalf("anonymous usage = %+v", usage)
	}
}

func TestMaskSecretShortValue(t *testing.T) {
	if got := maskSecret("abc"); got != "••••" {
		t.Fatalf("maskSecret(short) = %q", got)
	}
	if got := maskSecret("abcdefghij"); got != "••••" {
		t.Fatalf("maskSecret(10 chars) = %q, want fully hidden", got)
	}
	if got := maskSecret("abcdefghijklmnopq"); got != "abcd…pq" {
		t.Fatalf("maskSecret = %q", got)
	}
}

// ---- check ----------------------------------------------------------------

// identityCheckTarget answers by Authorization so each verdict is reachable.
func identityCheckTarget(t *testing.T) (*httptest.Server, *store.Flow) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			_, _ = io.WriteString(w, "ok")
		case "Bearer stale":
			w.Header().Set("WWW-Authenticate", `Bearer realm="example"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "Bearer denied":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return srv, &store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/api/me"}
}

func TestCheckIdentityVerdicts(t *testing.T) {
	_, f := identityCheckTarget(t)
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	flowID, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}

	// A flow whose upstream is gone: a transport error.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	du, _ := url.Parse(dead.URL)
	dead.Close()
	deadPort, _ := strconv.Atoi(du.Port())
	deadID, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "http", Host: du.Hostname(), Port: deadPort, Path: "/api/me"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, headers, verdict string
		status                 int
		flowID                 int64
	}{
		{"ok-id", "Authorization: Bearer good", identityVerdictOK, 200, flowID},
		{"stale-id", "Authorization: Bearer stale", identityVerdictExpired, 401, flowID},
		{"denied-id", "Authorization: Bearer denied", identityVerdictDenied, 403, flowID},
		{"odd-id", "Authorization: Bearer other", identityVerdictError, http.StatusTeapot, flowID},
		{"dead-id", "Authorization: Bearer good", identityVerdictError, 0, deadID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"name": tc.name, "headers": tc.headers})
			authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", string(body), nil)
			code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identities/"+tc.name+"/check",
				`{"flowId":`+strconv.FormatInt(tc.flowID, 10)+`}`, nil)
			if code != http.StatusOK {
				t.Fatalf("check status = %d: %+v", code, out)
			}
			chk, _ := out["lastCheck"].(map[string]any)
			if chk == nil || chk["verdict"] != tc.verdict {
				t.Fatalf("verdict = %+v, want %s", chk, tc.verdict)
			}
			if tc.status != 0 && int(chk["status"].(float64)) != tc.status {
				t.Fatalf("status = %+v, want %d", chk["status"], tc.status)
			}
			// It is persisted on the identity.
			_, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/"+tc.name, "", nil)
			stored, _ := identityOf(t, detail)["lastCheck"].(map[string]any)
			if stored == nil || stored["verdict"] != tc.verdict || stored["ts"] == "" {
				t.Fatalf("stored lastCheck = %+v", stored)
			}
		})
	}

	// With no flowId and no source flow the request is refused, not guessed.
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", `{"name":"no-source","headers":"Authorization: Bearer good"}`, nil)
	if code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identities/no-source/check", "", nil); code != http.StatusBadRequest {
		t.Fatalf("missing flowId = %d, want 400", code)
	}
	// A source flow supplies the default.
	withAuth := *f
	withAuth.ID = 0
	withAuth.ReqHeaders = map[string][]string{"Authorization": {"Bearer good"}}
	sourceID, err := st.InsertFlow(&withAuth)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := authzReq(t, http.MethodPost,
		ts.URL+"/api/authz/identities/no-source/refresh-from-flow/"+strconv.FormatInt(sourceID, 10), "", nil); code != http.StatusOK {
		t.Fatalf("refresh = %d: %+v", code, out)
	}
	if code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identities/no-source/check", "", nil); code != http.StatusOK {
		t.Fatalf("default flow check = %d: %+v", code, out)
	}
	if code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz/identities/ghost/check", `{"flowId":1}`, nil); code != http.StatusNotFound {
		t.Fatalf("check unknown identity = %d, want 404", code)
	}
}

// ---- SSE ------------------------------------------------------------------

func TestAuthzUpdateBroadcastOnIdentityMutations(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	sink, _ := authzEventSink(t, h)

	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", `{"name":"admin","headers":"Authorization: Bearer admin-token-one"}`, nil)
	msg := waitForEvent(t, sink, `"authz.update"`)
	if !strings.Contains(msg, `"what":"identities"`) || !strings.Contains(msg, `"name":"admin"`) {
		t.Fatalf("identity event = %s", msg)
	}

	authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/admin", `{"role":"admin"}`, nil)
	waitForEvent(t, sink, `"name":"admin"`)

	authzReq(t, http.MethodDelete, ts.URL+"/api/authz/identity/admin", "", nil)
	waitForEvent(t, sink, `"name":"admin"`)
}
