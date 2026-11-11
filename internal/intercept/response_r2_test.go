package intercept

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A body rule that matches nothing must hand back the caller's slice, not a
// full copy of it: regexp.ReplaceAll copies its input even without a match, so
// every response passing an enabled rule was duplicated in memory.
func TestNonMatchingResponseBodyRuleDoesNotCopyBody(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x"}}); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("a"), 1<<20)
	_, got := e.ApplyResponseRules(http.Header{}, body, false)
	if len(got) != len(body) || &got[0] != &body[0] {
		t.Fatal("a non-matching body rule returned a copy of the body")
	}
	allocs := testing.AllocsPerRun(5, func() { e.ApplyResponseRules(http.Header{}, body, false) })
	if allocs > 8 {
		t.Fatalf("non-matching rule allocated %v times per response", allocs)
	}
	// A matching rule still rewrites, and never edits its input in place.
	if err := e.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "a{4}$", Replace: "END"}}); err != nil {
		t.Fatal(err)
	}
	_, got = e.ApplyResponseRules(http.Header{}, body, false)
	if !bytes.HasSuffix(got, []byte("aEND")) || body[len(body)-1] != 'a' {
		t.Fatalf("matching rule: suffix %q, input tail %q", got[len(got)-4:], body[len(body)-4:])
	}
}
