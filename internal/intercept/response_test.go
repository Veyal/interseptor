package intercept

import (
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestApplyResponseRules(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{
		{Enabled: true, Type: "res-header", Match: `Server: .*`, Replace: "Server: redacted"},
		{Enabled: true, Type: "res-body", Match: `secret`, Replace: "REDACTED"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if !e.HasResponseRules() {
		t.Fatal("expected HasResponseRules true")
	}
	h := http.Header{"Server": {"nginx/1.21"}, "Content-Type": {"text/plain"}}
	nh, body := e.ApplyResponseRules(h, []byte("a secret value"))
	if nh.Get("Server") != "redacted" {
		t.Fatalf("res-header rule not applied: %q", nh.Get("Server"))
	}
	if string(body) != "a REDACTED value" {
		t.Fatalf("res-body rule not applied: %q", body)
	}
}

func TestHoldResponseThenForwardEdited(t *testing.T) {
	e := New()
	e.SetResponseEnabled(true)
	got := make(chan ResponseDecision, 1)
	go func() { got <- e.HoldResponse(&store.Flow{Host: "x"}, []byte("HTTP/1.1 200 OK\r\n\r\nbody")) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	q := e.ResponseQueue()
	if len(q) != 1 {
		t.Fatalf("expected 1 held response, got %d", len(q))
	}
	if err := e.ForwardResponse(q[0].ID, []byte("HTTP/1.1 200 OK\r\n\r\nedited")); err != nil {
		t.Fatalf("ForwardResponse: %v", err)
	}
	select {
	case d := <-got:
		if !d.Edited || string(d.Raw) != "HTTP/1.1 200 OK\r\n\r\nedited" {
			t.Fatalf("unexpected decision: %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestHoldResponseDisabledPassesThrough(t *testing.T) {
	e := New()
	d := e.HoldResponse(&store.Flow{}, []byte("raw"))
	if d.Drop || string(d.Raw) != "raw" {
		t.Fatalf("disabled response intercept should forward unchanged: %+v", d)
	}
}
