package intruder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A request line whose target is an opaque URI ("GET x:@host/a") parses to a
// path that does not start with "/". Appending it to the attack target moves
// the authority: "http://target" + "@other/a" is a request to other with
// "target" as userinfo. The engine must refuse such a line instead of sending
// the attack to a host the caller never named (and that a scope check on the
// target never saw).
func TestRequestLineCannotRetargetAttack(t *testing.T) {
	var targetHits, otherHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { otherHits.Add(1) }))
	defer other.Close()
	otherAuthority := strings.TrimPrefix(other.URL, "http://")

	for _, line := range []string{
		"GET x:@" + otherAuthority + "/a HTTP/1.1",
		"GET x:@" + otherAuthority + "/§p§ HTTP/1.1",
	} {
		e := newEngine(t)
		if err := e.Start(Spec{Target: target.URL, Template: line + "\nHost: example.com\n\n", AttackType: "repeat", Repeat: 2}); err != nil {
			t.Fatalf("Start(%q): %v", line, err)
		}
		st := waitDone(t, e)
		for _, r := range st.Results {
			if r.Error == "" || r.FlowID != 0 {
				t.Errorf("%q: result %+v, want a refused request", line, r)
			}
		}
	}
	if n := otherHits.Load(); n != 0 {
		t.Fatalf("attack reached a host other than the target %d time(s)", n)
	}

	// An ordinary origin-form line, and an absolute-form one, still go to the target.
	for _, line := range []string{"GET /ok HTTP/1.1", "GET http://ignored.example/ok HTTP/1.1"} {
		e := newEngine(t)
		if err := e.Start(Spec{Target: target.URL, Template: line + "\nHost: example.com\n\n", AttackType: "repeat", Repeat: 1}); err != nil {
			t.Fatalf("Start(%q): %v", line, err)
		}
		st := waitDone(t, e)
		if len(st.Results) != 1 || st.Results[0].Error != "" || st.Results[0].Status != 200 {
			t.Fatalf("%q: %+v", line, st.Results)
		}
	}
	if targetHits.Load() != 2 || otherHits.Load() != 0 {
		t.Fatalf("target hits=%d other hits=%d", targetHits.Load(), otherHits.Load())
	}
}
