package curlgen

import (
	"strings"
	"testing"
)

// curl turns --data-raw into a POST unless the method is forced, so a GET
// carrying a body must emit -X 'GET' to reproduce the original request.
func TestBuildGETWithBodyForcesMethod(t *testing.T) {
	out := Build("GET", "http://h/search", nil, []byte(`{"q":1}`))
	if !strings.Contains(out, "-X 'GET'") {
		t.Fatalf("GET with body must emit -X 'GET': %s", out)
	}
	if !strings.Contains(out, "--data-raw") {
		t.Fatalf("body missing: %s", out)
	}
}
