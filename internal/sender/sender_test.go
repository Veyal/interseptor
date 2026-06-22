package sender

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/store"
)

func TestSendCapturesAsFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Test") != "1" {
			t.Errorf("missing custom header on upstream")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(201)
		io.WriteString(w, "echo:"+string(body))
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))

	flow, err := snd.Send(Request{
		Method:  "POST",
		URL:     upstream.URL + "/submit?x=1",
		Headers: map[string][]string{"X-Test": {"1"}},
		Body:    []byte("ping"),
		Flags:   store.FlagRepeater,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != 201 || flow.Method != "POST" || flow.Path != "/submit?x=1" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if flow.ReqLen != 4 {
		t.Fatalf("expected req len 4, got %d", flow.ReqLen)
	}
	if flow.Flags&store.FlagRepeater == 0 {
		t.Fatalf("expected FlagRepeater, flags=%d", flow.Flags)
	}

	rc, err := s.OpenBody(flow.ResBodyHash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "echo:ping" {
		t.Fatalf("response body mismatch: %q", b)
	}

	// Stored as a flow; RequireFlags surfaces it, ExcludeFlags hides it.
	if got, _ := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagRepeater}); len(got) != 1 {
		t.Fatalf("RequireFlags: expected 1, got %d", len(got))
	}
	if got, _ := s.QueryFlowsFilter(store.FlowFilter{ExcludeFlags: store.FlagRepeater}); len(got) != 0 {
		t.Fatalf("ExcludeFlags: expected 0, got %d", len(got))
	}
}

func TestSendRecordsUpstreamError(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))

	flow, err := snd.Send(Request{Method: "GET", URL: "http://127.0.0.1:1/nope", Flags: store.FlagRepeater})
	if err != nil {
		t.Fatalf("Send should record errors, not return them: %v", err)
	}
	if flow.Error == "" || flow.Status != http.StatusBadGateway {
		t.Fatalf("expected errored flow, got %+v", flow)
	}
}

func TestSendRejectsBadURL(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	defer s.Close()
	snd := New(s, capture.New(s))
	if _, err := snd.Send(Request{Method: "GET", URL: "notaurl"}); err == nil {
		t.Fatal("expected error for non-absolute URL")
	}
}
