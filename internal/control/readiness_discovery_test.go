package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestReadinessEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/readiness")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestReadinessReportsAutopilotPreflight(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "GLM_API_KEY", "ZAI_API_KEY"} {
		t.Setenv(key, "")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "exclude", Host: "evil.test"}); err != nil {
		t.Fatalf("CreateScopeRule: %v", err)
	}
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	read := func() readinessReport {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/readiness")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		var rep readinessReport
		if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return rep
	}
	check := func(rep readinessReport, id string) readinessCheck {
		t.Helper()
		for _, c := range rep.Checks {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("missing readiness check %q", id)
		return readinessCheck{}
	}

	rep := read()
	if check(rep, "scope").OK {
		t.Fatal("exclude-only scope must not satisfy autopilot preflight")
	}
	if check(rep, "ai_provider").OK {
		t.Fatal("AI provider without credentials must not satisfy autopilot preflight")
	}

	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "example.com"}); err != nil {
		t.Fatalf("CreateScopeRule: %v", err)
	}
	if err := st.SetSetting("ai.apiKey", "test-key"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	rep = read()
	if !check(rep, "scope").OK {
		t.Fatal("enabled include scope must satisfy autopilot preflight")
	}
	if !check(rep, "ai_provider").OK {
		t.Fatal("configured AI provider must satisfy autopilot preflight")
	}
}

func TestPromoteFlowToAuthz(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	id, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "app.test", Port: 443, Path: "/api/me",
		ReqHeaders: map[string][]string{"Cookie": {"session=abc"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/authz/from-flow/"+strconv.FormatInt(id, 10),
		"application/json", strings.NewReader(`{"name":"Admin","merge":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
