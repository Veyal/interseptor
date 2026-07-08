package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestSessionHeadersScopeGated(t *testing.T) {
	var gotCookie, otherCookie string
	blockOther := true
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCookie = r.Header.Get("Cookie")
		if blockOther && otherCookie != "" {
			t.Fatalf("session leaked to other host: %q", otherCookie)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	h, s, _ := newHub(t)
	_, _ = s.CreateScopeRule(&store.ScopeRule{Ord: 0, Enabled: true, Action: "include", Host: "127.0.0.1"})
	h.refreshScope()

	_ = s.SetSetting("session.enabled", "1")
	_ = s.SetSetting("session.headers", "Cookie: session=secret")
	h.snd.SetSession(true, parseSessionHeaders("Cookie: session=secret"))

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	postRepeater := func(rawURL string) {
		body, _ := json.Marshal(map[string]any{"method": "GET", "url": rawURL})
		resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("POST repeater: %v", err)
		}
		resp.Body.Close()
	}

	postRepeater(target.URL + "/in")
	if gotCookie != "session=secret" {
		t.Fatalf("in-scope send: cookie = %q", gotCookie)
	}

	otherPort, _ := url.Parse(other.URL)
	outOfScopeURL := "http://localhost:" + otherPort.Port() + "/out"
	otherCookie = ""
	postRepeater(outOfScopeURL)
	if otherCookie != "" {
		t.Fatalf("out-of-scope send leaked cookie: %q", otherCookie)
	}

	blockOther = false
	_ = s.SetSetting("session.unscoped", "1")
	otherCookie = ""
	postRepeater(outOfScopeURL)
	if otherCookie != "session=secret" {
		t.Fatalf("unscoped send should inject cookie, got %q", otherCookie)
	}
}

func TestSessionWithoutScopeRulesDoesNotInject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("Cookie"); c != "" {
			t.Fatalf("unexpected cookie without scope: %q", c)
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	h, _, _ := newHub(t)
	_ = h.st.SetSetting("session.enabled", "1")
	h.snd.SetSession(true, parseSessionHeaders("Cookie: sid=1"))

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body, _ := json.Marshal(map[string]any{"method": "GET", "url": upstream.URL})
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
}
