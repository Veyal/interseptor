package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestSessionAccessKeyCookieOnly(t *testing.T) {
	h, st, _ := newHub(t)
	token, _, err := st.CreateAPIKey("laptop", store.ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Bearer alone must not reveal a "session" key.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/session/access-key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer-only: want 401, got %d", resp.StatusCode)
	}

	// Cookie session → token returned.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/session/access-key", nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET cookie: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("cookie session: want 200, got %d", resp2.StatusCode)
	}
	var out struct {
		Token  string `json:"token"`
		Scope  string `json:"scope"`
		Prefix string `json:"prefix"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Token != token || out.Scope != store.ScopeFull || out.Prefix != token[:12] {
		t.Fatalf("unexpected body: %+v", out)
	}
}
