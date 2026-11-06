package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The human-only trust gate used to accept any caller that simply omitted the
// AI source header and set X-Interseptor-CSRF: 1, which a request re-entering
// the control port on the AI's behalf could do. It now also needs the UI
// session cookie that only the browser loading the UI shell receives.
func TestHumanOnlyGateNeedsUISessionCookie(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	trust := "/api/collections/" + collUID + "/trust"
	body := map[string]any{"confirm": true, "all": true, "capabilities": []string{CapNetSend}}

	for name, h := range map[string]hdrs{
		"headers only":         {"X-Interseptor-CSRF": "1"},
		"forged cookie":        {"X-Interseptor-CSRF": "1", "Cookie": uiSessionCookie + "=deadbeef"},
		"cookie but ai source": {"X-Interseptor-CSRF": "1", "X-Interseptor-Source": "ai", "Cookie": uiSessionCookie + "=" + uiSessionSecret},
	} {
		if code, out := f.do("POST", trust, body, h); code != http.StatusForbidden {
			t.Errorf("%s: trust = %d (%s), want 403", name, code, out)
		}
	}
	if code, out := f.do("POST", trust, body, hdrs{"X-Interseptor-CSRF": "1"}); !strings.Contains(out, "reload") {
		t.Errorf("stale-session refusal should tell the operator to reload the UI: %d %s", code, out)
	}
	f.must("POST", trust, body, asUI, 200, nil)
}

func TestUIShellIssuesSessionCookieOnlyToBrowsers(t *testing.T) {
	h, _, _ := newHub(t)
	cookieFor := func(hdr map[string]string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.serveUI(w, r)
		for _, c := range w.Result().Cookies() {
			if c.Name == uiSessionCookie {
				if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
					t.Fatalf("UI session cookie must be HttpOnly and SameSite=Strict: %+v", c)
				}
				return c.Value
			}
		}
		return ""
	}
	if got := cookieFor(nil); got != uiSessionSecret {
		t.Fatalf("UI shell did not issue the session cookie (got %q)", got)
	}
	if got := cookieFor(map[string]string{"X-Interseptor-Source": "ai"}); got != "" {
		t.Fatal("AI-source callers must not receive the UI session cookie")
	}
	if got := cookieFor(map[string]string{"Authorization": "Bearer ick_example"}); got != "" {
		t.Fatal("API-key callers must not receive the UI session cookie")
	}
	r := httptest.NewRequest(http.MethodGet, "/js/app.js", nil)
	w := httptest.NewRecorder()
	h.serveUI(w, r)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("static assets must not issue the UI session cookie")
	}
}
