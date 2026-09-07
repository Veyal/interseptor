package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestSessionInspectionRedactsCookieValuesAndKeepsAttributes(t *testing.T) {
	flows := []*store.Flow{{
		ID: 7, TS: time.Unix(10, 0).UTC(), Method: "POST", Scheme: "https", Host: "example.com", Path: "/login", Status: http.StatusFound,
		ReqHeaders:  map[string][]string{"Cookie": {"sid=secret-request; csrf=csrf-value"}},
		ResHeaders:  map[string][]string{"Set-Cookie": {"sid=secret-response; Path=/; Secure; HttpOnly; SameSite=Lax"}, "Location": {"/home"}, "Authorization": {"Bearer header-secret"}},
		ResBodyHash: strings.Repeat("a", 64),
	}}

	out := buildSessionInspection(flows, []string{"anonymous"})
	if len(out.Flows) != 1 || len(out.Flows[0].RequestCookies) != 2 || len(out.Flows[0].ResponseCookies) != 1 {
		t.Fatalf("cookie observations = %#v", out.Flows[0])
	}
	json := string(mustJSON(t, out))
	for _, secret := range []string{"secret-request", "secret-response", "csrf-value", "header-secret"} {
		if strings.Contains(json, secret) {
			t.Fatalf("raw cookie value leaked into inspection: %q", secret)
		}
	}
	if !strings.Contains(json, "HttpOnly") || !strings.Contains(json, "SameSite=Lax") || !strings.Contains(json, "sid") {
		t.Fatalf("cookie attributes/name missing: %s", json)
	}
}

func TestSessionInspectionRedactsMalformedRequestTargets(t *testing.T) {
	for _, path := range []string{
		"/callback/%zz?token=private-value#private-fragment",
		"/callback/\n?token=private-value#private-fragment",
	} {
		t.Run(path, func(t *testing.T) {
			out := buildSessionInspection([]*store.Flow{{
				ID: 1, Method: "GET", Host: "example.com", Path: path,
			}}, nil)
			encoded := string(mustJSON(t, out))
			if strings.Contains(encoded, "private-value") || strings.Contains(encoded, "private-fragment") {
				t.Fatal("malformed request target exposed a query value or fragment")
			}
			if got := out.Flows[0].Path; got != "/" {
				t.Fatalf("malformed path = %q, want unavailable path fallback", got)
			}
		})
	}
	if got := safeFlowPath("/callback?token=private-value&state=private-state#private-fragment"); got != "/callback?state=…&token=…" {
		t.Fatalf("valid request target redaction = %q", got)
	}
}

func TestSessionInspectionMarksUnknownBrowserDecisionsAndCandidateTransitions(t *testing.T) {
	flows := []*store.Flow{
		{ID: 1, ClientAddr: "client-1", TS: time.Unix(1, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/login", Status: 302, ResHeaders: map[string][]string{"Set-Cookie": {"sid=one; Path=/; Secure"}, "Location": {"/mfa"}}},
		{ID: 2, ClientAddr: "client-1", TS: time.Unix(2, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/mfa", Status: 200, ReqHeaders: map[string][]string{"Cookie": {"sid=one"}}, ResHeaders: map[string][]string{"Set-Cookie": {"sid=two; Path=/; Secure"}}},
	}
	out := buildSessionInspection(flows, []string{"anonymous", "anonymous"})
	if len(out.Transitions) == 0 {
		t.Fatal("expected candidate transitions")
	}
	json := string(mustJSON(t, out))
	for _, marker := range []string{"unknown", "candidate", "rotation", "redirect"} {
		if !strings.Contains(strings.ToLower(json), marker) {
			t.Fatalf("inspection missing %q marker: %s", marker, json)
		}
	}
}

func TestSessionInspectionCookieRotationRequiresSameObservedClientScope(t *testing.T) {
	flows := []*store.Flow{
		{ID: 1, ClientAddr: "client-1", TS: time.Unix(1, 0).UTC(), Host: "one.example", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=one; Path=/"}}},
		{ID: 2, ClientAddr: "client-1", TS: time.Unix(2, 0).UTC(), Host: "two.example", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=two; Path=/"}}},
		{ID: 3, ClientAddr: "client-2", TS: time.Unix(3, 0).UTC(), Host: "one.example", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=three; Path=/"}}},
		{ID: 4, ClientAddr: "client-1", TS: time.Unix(4, 0).UTC(), Host: "one.example", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=four; Path=/"}}},
		{ID: 5, ClientAddr: "client-1", TS: time.Unix(5, 0).UTC(), Host: "a.example.com", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=five; Domain=example.com; Path=/"}}},
		{ID: 6, ClientAddr: "client-1", TS: time.Unix(6, 0).UTC(), Host: "b.example.com", Path: "/", ResHeaders: map[string][]string{"Set-Cookie": {"sid=six; Domain=example.com; Path=/"}}},
	}
	out := buildSessionInspection(flows, []string{"user", "user", "user", "user"})
	rotations := 0
	for _, transition := range out.Transitions {
		if transition.Kind == "rotation" {
			rotations++
			if transition.FlowID != 4 && transition.FlowID != 6 {
				t.Fatalf("unscoped rotation at flow %d: %#v", transition.FlowID, transition)
			}
		}
	}
	if rotations != 2 {
		t.Fatalf("rotation count=%d, want same-client/same-host or explicit cookie-domain rotations", rotations)
	}
}

func TestSessionInspectionDifferentialUsesRoleLabelsAndNoRawBodies(t *testing.T) {
	flows := []*store.Flow{
		{ID: 9, TS: time.Unix(2, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 200, ResHeaders: map[string][]string{"Content-Type": {"application/json"}}, ResBodyHash: strings.Repeat("b", 64)},
		{ID: 8, TS: time.Unix(1, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 401, ResHeaders: map[string][]string{"Content-Type": {"application/json"}}, ResBodyHash: strings.Repeat("c", 64)},
	}
	out := buildSessionInspection(flows, []string{"user", "anonymous"})
	if len(out.Flows) != 2 || out.Flows[0].ID != 8 || out.Flows[0].Role != "anonymous" {
		t.Fatalf("timeline ordering/roles = %#v", out.Flows)
	}
	if len(out.Differentials) != 1 || out.Differentials[0].StatusDifferent != true || out.Differentials[0].Roles[0] != "anonymous" {
		t.Fatalf("differential = %#v", out.Differentials)
	}
	json := string(mustJSON(t, out))
	if strings.Contains(json, strings.Repeat("b", 64)) || strings.Contains(json, strings.Repeat("c", 64)) {
		t.Fatal("raw body hashes should be represented only by short comparison fingerprints")
	}
}

func TestSessionInspectionDifferentialRetainsRepeatedRoleObservations(t *testing.T) {
	flows := []*store.Flow{
		{ID: 1, TS: time.Unix(1, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 401},
		{ID: 2, TS: time.Unix(2, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 403},
		{ID: 3, TS: time.Unix(3, 0).UTC(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 200},
	}
	out := buildSessionInspection(flows, []string{"anonymous", "anonymous", "user"})
	if len(out.Differentials) != 1 {
		t.Fatalf("differentials=%#v", out.Differentials)
	}
	diff := out.Differentials[0]
	if diff.RoleCounts["anonymous"] != 2 || len(diff.Observations) != 3 {
		t.Fatalf("repeated role observations lost: counts=%#v observations=%#v", diff.RoleCounts, diff.Observations)
	}
}

func TestSessionInspectionEmptyAndRoleNormalization(t *testing.T) {
	out := buildSessionInspection(nil, []string{"weird role"})
	if len(out.Flows) != 0 || len(out.Differentials) != 0 {
		t.Fatalf("empty inspection = %#v", out)
	}
	if normalizeSessionRole("administrator") != "admin" || normalizeSessionRole("") != "unassigned" {
		t.Fatalf("role normalization failed")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
