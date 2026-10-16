package intercept

import (
	"context"
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
	nh, body := e.ApplyResponseRules(h, []byte("a secret value"), false)
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

func TestHoldResponseContextCancelsAndRemovesResponse(t *testing.T) {
	e := New()
	e.SetResponseEnabled(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan ResponseDecision, 1)
	go func() { got <- e.HoldResponseContext(ctx, &store.Flow{}, []byte("HTTP/1.1 200 OK\r\n\r\nbody")) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(e.ResponseQueue()) != 1 {
		t.Fatal("response was not held")
	}
	cancel()
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatalf("canceled response decision = %+v, want drop", d)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled response hold did not return")
	}
	if len(e.ResponseQueue()) != 0 {
		t.Fatal("canceled response remained held")
	}
}

// A filter that selects which requests are held must select which responses are
// held too. Without this, turning response interception on holds every in-scope
// response -- images, fonts, analytics -- and the queue floods.
func TestHoldResponseAppliesInterceptFilter(t *testing.T) {
	flow := &store.Flow{Method: "GET", Host: "api.example.com", Path: "/v1/orders"}
	other := &store.Flow{Method: "GET", Host: "cdn.example.com", Path: "/app.css"}
	res := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true}")

	for _, tc := range []struct {
		name           string
		target, filter string
		flow           *store.Flow
		raw            []byte
		hold           bool
	}{
		{"url matches", "url", `^api\.example\.com`, flow, res, true},
		{"url does not match", "url", `^api\.example\.com`, other, res, false},
		{"method matches", "method", `^GET$`, flow, res, true},
		{"method does not match", "method", `^POST$`, flow, res, false},
		{"response header matches", "header", `application/json`, flow, res, true},
		{"response header does not match", "header", `text/css`, flow, res, false},
		{"response body matches", "body", `"ok":true`, flow, res, true},
		{"response body does not match", "body", `nothing here`, flow, res, false},
		// "any" is the default target, and an operator writes it against the
		// exchange: the request line has to be matchable from the response side
		// or a path filter would silently hold nothing.
		{"any matches the request line", "any", `/v1/orders`, flow, res, true},
		{"any matches the response", "any", `application/json`, flow, res, true},
		{"any matches neither", "any", `/admin`, flow, res, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			e.SetResponseEnabled(true)
			if err := e.SetInterceptFilter(true, tc.target, tc.filter); err != nil {
				t.Fatalf("SetInterceptFilter: %v", err)
			}
			got := make(chan ResponseDecision, 1)
			go func() { got <- e.HoldResponse(tc.flow, tc.raw) }()

			if !tc.hold {
				select {
				case d := <-got:
					if d.Drop || string(d.Raw) != string(tc.raw) {
						t.Fatalf("filtered response was not forwarded unchanged: %+v", d)
					}
				case <-time.After(time.Second):
					t.Fatal("filtered response was held")
				}
				if n := len(e.ResponseQueue()); n != 0 {
					t.Fatalf("filtered response entered the queue (%d)", n)
				}
				return
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			q := e.ResponseQueue()
			if len(q) != 1 {
				t.Fatalf("expected the matching response to be held, queue has %d", len(q))
			}
			if err := e.ForwardResponse(q[0].ID, tc.raw); err != nil {
				t.Fatalf("ForwardResponse: %v", err)
			}
			<-got
		})
	}
}

// Turning the filter off must hold everything again.
func TestHoldResponseWithoutFilterHoldsEverything(t *testing.T) {
	e := New()
	e.SetResponseEnabled(true)
	if err := e.SetInterceptFilter(false, "url", `^api\.example\.com`); err != nil {
		t.Fatalf("SetInterceptFilter: %v", err)
	}
	raw := []byte("HTTP/1.1 200 OK\r\n\r\nx")
	go e.HoldResponse(&store.Flow{Host: "cdn.example.com", Path: "/app.css"}, raw)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(e.ResponseQueue()); n != 1 {
		t.Fatalf("expected the response to be held with the filter off, queue has %d", n)
	}
	e.SetResponseEnabled(false)
}
