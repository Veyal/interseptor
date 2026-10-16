package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// Authorization code + PKCE end to end on the control port: begin (UI only),
// the provider's redirect to the callback route, a guarded token exchange, and
// the stored token used by the next send. No token ever appears in a response.
func TestOAuthAuthCodePKCECallbackOnControlPort(t *testing.T) {
	f := newCollFixture(t)
	tu, _ := url.Parse(f.target.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: tu.Hostname()}})
	var form atomic.Value
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form.Store(r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "canary-access-AAAA1111BBBB", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer idp.Close()

	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "OAuth"}, asUI, 201, &co)
	f.must("PUT", "/api/collections/"+co.UID, map[string]any{"name": "OAuth", "rev": co.Rev,
		"auth": json.RawMessage(`{"type":"oauth2","oauth2":[{"key":"grant_type","value":"authorization_code"},` +
			`{"key":"authUrl","value":"` + idp.URL + `/authorize"},{"key":"accessTokenUrl","value":"` + idp.URL + `/token"},` +
			`{"key":"clientId","value":"cid"},{"key":"clientSecret","value":"csecret-value-123456"}]}`)}, asUI, 200, nil)
	var req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "R", "method": "GET",
		"url": f.target.URL + "/data"}, asUI, 201, &req)

	// Starting the flow is a human act.
	f.must("POST", "/api/collections/oauth/begin", map[string]any{"itemUid": req.UID}, asAI, 403, nil)
	var begin struct {
		AuthURL     string `json:"authUrl"`
		State       string `json:"state"`
		RedirectURI string `json:"redirectUri"`
	}
	f.must("POST", "/api/collections/oauth/begin", map[string]any{"itemUid": req.UID}, asUI, 200, &begin)
	au, err := url.Parse(begin.AuthURL)
	if err != nil || au.Query().Get("code_challenge_method") != "S256" || au.Query().Get("state") != begin.State {
		t.Fatalf("auth url %q: %v", begin.AuthURL, err)
	}
	if !strings.HasSuffix(begin.RedirectURI, "/api/collections/oauth/callback") || au.Query().Get("redirect_uri") != begin.RedirectURI {
		t.Fatalf("redirect uri %q", begin.RedirectURI)
	}

	// The provider redirects the browser back with a code.
	code, body := f.do("GET", "/api/collections/oauth/callback?code=abc123&state="+begin.State, nil, hdrs{})
	if code != 200 || strings.Contains(body, "canary-access") {
		t.Fatalf("callback = %d %q", code, body)
	}
	got, _ := form.Load().(url.Values)
	if got.Get("grant_type") != "authorization_code" || got.Get("code") != "abc123" || got.Get("code_verifier") == "" {
		t.Fatalf("token exchange form = %v", got)
	}
	// One-shot state.
	if code, _ := f.do("GET", "/api/collections/oauth/callback?code=abc123&state="+begin.State, nil, hdrs{}); code != http.StatusBadRequest {
		t.Fatalf("replayed state = %d, want 400", code)
	}
	if code, _ := f.do("GET", "/api/collections/oauth/callback?code=x&state=nope", nil, hdrs{}); code != http.StatusBadRequest {
		t.Fatalf("unknown state = %d, want 400", code)
	}

	if toks, _ := f.st.ListTokens(co.UID); len(toks) != 1 {
		t.Fatalf("stored tokens = %d, want 1", len(toks))
	}
	// The stored token authorises the next send; no response ever carried it.
	var res collexecResult
	out := f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID}, asUI, 200, &res)
	if res.Outcome != "sent" || strings.Contains(out, "canary-access") {
		t.Fatalf("send: %+v %s", res, out)
	}
}

func TestOAuthBeginRejectsNonOAuthAuth(t *testing.T) {
	f := newCollFixture(t)
	itemUID := f.firstRequest(f.importDemo())
	f.must("POST", "/api/collections/oauth/begin", map[string]any{"itemUid": itemUID}, asUI, 400, nil)
	f.must("POST", "/api/collections/oauth/begin", map[string]any{}, asUI, 400, nil)
}
