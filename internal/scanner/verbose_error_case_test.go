package scanner

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// The verbose-error check looked for lowercase markers only, so the spelled
// forms real 5xx responses use — "Exception", "Stack", "TraceId" — never
// matched while the shipped Starlark template (which lower-cases the body) did.
func TestVerboseErrorMarkerIsCaseInsensitive(t *testing.T) {
	flow := func() *store.Flow {
		return &store.Flow{
			Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/api", Status: 500,
			Mime: "application/json",
			ResHeaders: map[string][]string(http.Header{
				"Content-Type":              {"application/json"},
				"Strict-Transport-Security": {"max-age=63072000"},
			}),
		}
	}
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"capitalised Exception", `<h1>Application Exception</h1>`, true},
		{"capitalised Stack", `<p>Stack: java.lang.IllegalState</p>`, true},
		{"capitalised TraceId", `{"TraceId":"3f9a"}`, true},
		{"lowercase exception", `{"error":"NullPointerException"}`, true},
		{"plain 5xx page with no diagnostic", `<h1>Something went wrong</h1>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(flow(), nil, []byte(tc.body))
			if has(got, "Verbose error discloses internal details") != tc.want {
				t.Fatalf("verbose-error = %v, want %v: %s", !tc.want, tc.want, titles(got))
			}
		})
	}
}

// A stack frame with none of the marker words (" at java.lang.Thread.run(") is
// caught by the frame regex, but the evidence was looked up by marker only, so
// that finding shipped with an empty evidence field.
func TestVerboseErrorStackFrameCarriesEvidence(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/api", Status: 500,
		Mime: "application/json",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"error":"boom"} at com.acme.service.Handler.process(`))
	var found bool
	for _, is := range got {
		if is.Title == "Verbose error discloses internal details" {
			found = true
			if !strings.Contains(is.Evidence, "at com.acme.service.Handler.process(") {
				t.Fatalf("evidence = %q, want the stack frame", is.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("expected a verbose-error finding; got: %s", titles(got))
	}
}
