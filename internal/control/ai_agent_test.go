package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/store"
)

func TestMergeSeedHeadersFillsAuth(t *testing.T) {
	seed := &store.Flow{ReqHeaders: map[string][]string{
		"Cookie":        {"sess=abc"},
		"Authorization": {"Bearer tok"},
		"User-Agent":    {"Test/1"},
	}}
	got := mergeSeedHeaders(map[string][]string{}, seed)
	for _, k := range []string{"Cookie", "Authorization", "User-Agent"} {
		if len(got[k]) == 0 {
			t.Fatalf("missing seeded header %s: %+v", k, got)
		}
	}
	got = mergeSeedHeaders(map[string][]string{"Cookie": {"other=1"}}, seed)
	if got["Cookie"][0] != "other=1" {
		t.Fatalf("user cookie should win, got %v", got["Cookie"])
	}
}

func TestAgentToolSummary(t *testing.T) {
	s := agentToolSummary("send_request", map[string]any{"method": "GET", "url": "https://example.com/admin"})
	if !strings.Contains(s, "method=GET") || !strings.Contains(s, "url=https://example.com/admin") {
		t.Fatalf("summary=%q", s)
	}
}

func TestAgentGetFlow(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Host: "h", Path: "/x", Status: 200,
		ReqHeaders: map[string][]string{"Accept": {"*/*"}},
		ResHeaders: map[string][]string{"Content-Type": {"text/plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ai := &aiAPI{h}
	out, summary, ok := ai.agentGetFlow(map[string]any{"id": float64(id), "side": "both", "maxBytes": 2000})
	if !ok || summary == "" {
		t.Fatalf("get_flow failed: ok=%v summary=%q out=%q", ok, summary, out)
	}
	if !strings.Contains(out, "GET /x") || !strings.Contains(out, "200") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestAgentSendRequest(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seedtok" {
			t.Errorf("expected seeded auth header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	seedID, err := s.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Host: "h", Path: "/seed",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer seedtok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	seed, err := s.GetFlow(seedID)
	if err != nil {
		t.Fatal(err)
	}

	ai := &aiAPI{h}
	out, _, ok := ai.agentSendRequest(map[string]any{
		"method": "GET",
		"url":    target.URL + "/probe",
	}, seed)
	if !ok {
		t.Fatalf("send failed: %s", out)
	}
	if !strings.Contains(out, "flow id=") || !strings.Contains(out, "status=200") {
		t.Fatalf("unexpected result: %s", out)
	}
}

func TestExecAgentToolUnknown(t *testing.T) {
	h, _, _ := newHub(t)
	ai := &aiAPI{h}
	out, _, ok := ai.execAgentTool(aiassist.ToolCall{Name: "nope"}, nil)
	if ok || !strings.Contains(out, "unknown tool") {
		t.Fatalf("expected unknown tool error, got ok=%v out=%q", ok, out)
	}
}
