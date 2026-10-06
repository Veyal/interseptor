package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

var timelineBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func tlFlow(id int64, method, scheme, host, path string, status int, req, res map[string][]string) *store.Flow {
	return &store.Flow{
		ID: id, TS: timelineBase.Add(time.Duration(id) * time.Second), Method: method, Scheme: scheme,
		Host: host, Path: path, Status: status, ReqHeaders: req, ResHeaders: res, ClientAddr: "192.0.2.10:5000",
	}
}

func tlEvents(tl authTimeline, flowID int64, kind string) []authTimelineEvent {
	var out []authTimelineEvent
	for _, s := range tl.Steps {
		if s.FlowID != flowID {
			continue
		}
		for _, e := range s.Events {
			if e.Kind == kind {
				out = append(out, e)
			}
		}
	}
	return out
}

func TestAuthTimelineHappyLoginHasNoLoss(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "app.example.com", "/login", 302, nil, map[string][]string{
			"Location": {"/dashboard"}, "Set-Cookie": {"sid=SECRETVALUE1; Path=/; Secure; HttpOnly; SameSite=Lax"}}),
		tlFlow(2, "GET", "https", "app.example.com", "/dashboard", 200, map[string][]string{"Cookie": {"sid=SECRETVALUE1"}}, nil),
	})
	if tl.LostAt != nil {
		t.Fatalf("unexpected loss: %+v", tl.LostAt)
	}
	set := tlEvents(tl, 1, "cookie-set")
	if len(set) != 1 || set[0].Name != "sid" || set[0].Fingerprint == "" {
		t.Fatalf("cookie-set = %+v", set)
	}
	if len(tlEvents(tl, 1, "redirect")) != 1 || len(tlEvents(tl, 2, "cookie-sent")) != 1 {
		t.Fatalf("missing redirect / cookie-sent events: %+v", tl.Steps)
	}
	b, _ := json.Marshal(tl)
	if strings.Contains(string(b), "SECRETVALUE1") {
		t.Fatalf("raw cookie value leaked into timeline JSON: %s", b)
	}
}

func TestAuthTimelineSecureCookieOverHTTPIsRejectedHypothesisAndLossLocated(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "http", "app.example.com", "/login", 302, nil, map[string][]string{
			"Location": {"/dashboard"}, "Set-Cookie": {"sid=v1; Path=/; Secure"}}),
		tlFlow(2, "GET", "http", "app.example.com", "/dashboard", 302, nil, map[string][]string{"Location": {"/login"}}),
	})
	rej := tlEvents(tl, 1, "cookie-rejected")
	if len(rej) != 1 || rej[0].Confidence != confidenceHypothesis {
		t.Fatalf("cookie-rejected = %+v, want one hypothesis", rej)
	}
	if len(tlEvents(tl, 1, "scheme-change")) != 0 {
		t.Fatalf("first step must not report a scheme change")
	}
}

func TestAuthTimelineLocatesWhereAuthenticatedStateWasLost(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "app.example.com", "/login", 302, nil, map[string][]string{
			"Location": {"/home"}, "Set-Cookie": {"sid=v1; Path=/; Secure"}}),
		tlFlow(2, "GET", "https", "app.example.com", "/home", 200, map[string][]string{"Cookie": {"sid=v1"}}, nil),
		tlFlow(3, "GET", "https", "app.example.com", "/account", 302, map[string][]string{"Cookie": {"sid=v1"}},
			map[string][]string{"Location": {"/login?next=/account"}}),
	})
	if tl.LostAt == nil || tl.LostAt.FlowID != 3 || tl.LostAt.Confidence != confidenceHypothesis {
		t.Fatalf("LostAt = %+v, want hypothesis at flow 3", tl.LostAt)
	}
}

func TestAuthTimelineCookieOmittedAndScopeDiagnostics(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "app.example.com", "/login", 200, nil, map[string][]string{
			"Set-Cookie": {"sid=v1; Path=/app; Secure"}}),
		tlFlow(2, "GET", "https", "app.example.com", "/app/data", 302, nil, map[string][]string{"Location": {"/login"}}),
	})
	om := tlEvents(tl, 2, "cookie-omitted")
	if len(om) != 1 || om[0].Name != "sid" || om[0].Confidence != confidenceHypothesis {
		t.Fatalf("cookie-omitted = %+v", om)
	}
	if tl.LostAt == nil || tl.LostAt.FlowID != 2 {
		t.Fatalf("LostAt = %+v, want flow 2", tl.LostAt)
	}

	dom := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "app.example.com", "/login", 200, nil, map[string][]string{
			"Set-Cookie": {"sid=v1; Domain=other.example.net; Path=/"}}),
	})
	if len(tlEvents(dom, 1, "cookie-rejected")) != 1 {
		t.Fatalf("domain mismatch not flagged: %+v", dom.Steps)
	}
}

func TestAuthTimelineSessionAndCSRFRotationAndClear(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "app.example.com", "/login", 200, nil, map[string][]string{
			"Set-Cookie": {"sid=aaa; Path=/; Secure", "XSRF-TOKEN=t1; Path=/"}}),
		tlFlow(2, "POST", "https", "app.example.com", "/step", 200, map[string][]string{"Cookie": {"sid=aaa; XSRF-TOKEN=t1"}},
			map[string][]string{"Set-Cookie": {"sid=bbb; Path=/; Secure", "XSRF-TOKEN=t2; Path=/"}}),
		tlFlow(3, "GET", "https", "app.example.com", "/logout", 200, map[string][]string{"Cookie": {"sid=bbb; XSRF-TOKEN=t2"}},
			map[string][]string{"Set-Cookie": {"sid=; Path=/; Max-Age=0"}}),
	})
	if r := tlEvents(tl, 2, "session-rotation"); len(r) != 1 || r[0].Fingerprint == r[0].PreviousFingerprint {
		t.Fatalf("session-rotation = %+v", r)
	}
	if len(tlEvents(tl, 2, "csrf-rotation")) != 1 {
		t.Fatalf("csrf-rotation missing: %+v", tl.Steps[1].Events)
	}
	if len(tlEvents(tl, 3, "cookie-cleared")) != 1 {
		t.Fatalf("cookie-cleared missing")
	}
	if tl.LostAt == nil || tl.LostAt.FlowID != 3 {
		t.Fatalf("LostAt = %+v, want clear at flow 3", tl.LostAt)
	}
}

func TestAuthTimelineSchemeHostChangeAndMFA(t *testing.T) {
	tl := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "POST", "https", "login.example.com", "/login", 302, nil, map[string][]string{"Location": {"https://app.example.com/mfa"}}),
		tlFlow(2, "GET", "https", "app.example.com", "/mfa", 200, nil, nil),
		tlFlow(3, "POST", "https", "app.example.com", "/mfa/verify", 302, nil, map[string][]string{
			"Location": {"/home"}, "Set-Cookie": {"sid=zzz; Path=/; Secure"}}),
	})
	if len(tlEvents(tl, 2, "host-change")) != 1 {
		t.Fatalf("host-change missing: %+v", tl.Steps[1].Events)
	}
	if len(tlEvents(tl, 2, "mfa-challenge")) != 1 {
		t.Fatalf("mfa-challenge missing")
	}
	if tl.MFAState != "completion-candidate" || tl.MFAConfidence != confidenceHypothesis {
		t.Fatalf("MFA = %s/%s", tl.MFAState, tl.MFAConfidence)
	}
	sw := buildAuthTimeline([]*store.Flow{
		tlFlow(1, "GET", "https", "app.example.com", "/a", 200, nil, nil),
		tlFlow(2, "GET", "http", "app.example.com", "/b", 200, nil, nil),
	})
	if len(tlEvents(sw, 2, "scheme-change")) != 1 {
		t.Fatalf("scheme-change missing")
	}
}

func TestAuthTimelineEndpointFollowsSameClientChain(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	mk := func(client, path string) int64 {
		id, err := st.InsertFlow(&store.Flow{
			TS: time.Now(), Method: "GET", Scheme: "https", Host: "app.example.com", Path: path, Status: 200, ClientAddr: client,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := mk("192.0.2.10:1000", "/login")
	mk("192.0.2.99:2000", "/other-client")
	mk("192.0.2.10:1001", "/dashboard")

	resp, err := http.Get(ts.URL + "/api/flows/" + strconv.FormatInt(first, 10) + "/auth-timeline")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var tl authTimeline
	if err := json.NewDecoder(resp.Body).Decode(&tl); err != nil {
		t.Fatal(err)
	}
	if len(tl.Steps) != 2 || tl.Steps[0].Path != "/login" || tl.Steps[1].Path != "/dashboard" {
		t.Fatalf("steps = %+v, want login then dashboard of the same client", tl.Steps)
	}
	missing, err := http.Get(ts.URL + "/api/flows/99999/auth-timeline")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing flow status = %d, want 404", missing.StatusCode)
	}
}
