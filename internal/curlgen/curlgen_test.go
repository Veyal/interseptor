package curlgen

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuildGET(t *testing.T) {
	out := Build("GET", "https://victim.test/a?x=1", http.Header{"Accept": {"*/*"}}, nil)
	if strings.Contains(out, "-X GET") {
		t.Fatalf("GET should not emit -X: %s", out)
	}
	for _, want := range []string{"curl --path-as-is -k", "'https://victim.test/a?x=1'", "-H 'Accept: */*'"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

// A non-standard method is single-quoted like every other value, so a method
// carrying shell metacharacters can't inject when the command is pasted.
func TestBuildMethodIsQuoted(t *testing.T) {
	out := Build("DELETE", "https://victim.test/a", nil, nil)
	if !strings.Contains(out, "-X 'DELETE'") {
		t.Fatalf("method not single-quoted: %s", out)
	}
}

func TestBuildPOSTWithBodyAndEscaping(t *testing.T) {
	h := http.Header{
		"Content-Type":   {"application/json"},
		"Content-Length": {"99"}, // must be dropped
		"Authorization":  {"Bearer abc"},
	}
	out := Build("post", "https://victim.test/login", h, []byte(`{"u":"o'malley"}`))
	if !strings.Contains(out, "-X 'POST'") {
		t.Fatalf("POST should emit -X 'POST': %s", out)
	}
	if strings.Contains(out, "Content-Length") {
		t.Fatalf("Content-Length should be dropped: %s", out)
	}
	if !strings.Contains(out, `--data-raw '{"u":"o'\''malley"}'`) {
		t.Fatalf("body not shell-escaped: %s", out)
	}
	if !strings.Contains(out, "-H 'Authorization: Bearer abc'") {
		t.Fatalf("missing auth header: %s", out)
	}
}

func TestBuildIsStableAndMultiline(t *testing.T) {
	h := http.Header{"B": {"2"}, "A": {"1"}}
	out := Build("GET", "http://h/x", h, nil)
	// Headers are emitted in sorted order for reproducibility.
	if strings.Index(out, "A: 1") > strings.Index(out, "B: 2") {
		t.Fatalf("headers not sorted: %s", out)
	}
	if !strings.Contains(out, "\\\n") {
		t.Fatalf("expected line continuations: %s", out)
	}
	// Re-running yields identical output.
	if Build("GET", "http://h/x", h, nil) != out {
		t.Fatal("output not deterministic")
	}
}
