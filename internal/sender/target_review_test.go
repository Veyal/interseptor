package sender

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

func TestTargetURL(t *testing.T) {
	ok := []struct{ target, path, want string }{
		{"http://example.com", "/a?b=1", "http://example.com/a?b=1"},
		{"https://example.com:8443/", "/a", "https://example.com:8443/a"},
		{"http://example.com/base/", "/a", "http://example.com/base/a"},
		{"http://example.com", "//double/slash", "http://example.com//double/slash"},
		{"http://example.com", "/a/@b", "http://example.com/a/@b"},
	}
	for _, c := range ok {
		got, err := TargetURL(c.target, c.path)
		if err != nil || got != c.want {
			t.Errorf("TargetURL(%q, %q) = %q, %v; want %q", c.target, c.path, got, err, c.want)
		}
	}
	bad := []struct{ target, path string }{
		{"http://example.com", "@other.example/a"}, // authority swap via userinfo
		{"http://example.com", ".other.example/a"}, // host suffix
		{"http://example.com", ":8081/a"},          // port swap
		{"http://example.com", "*"},                // asterisk-form
		{"http://example.com", ""},                 // nothing to request
		{"example.com", "/a"},                      // not an absolute target
	}
	for _, c := range bad {
		if got, err := TargetURL(c.target, c.path); err == nil {
			t.Errorf("TargetURL(%q, %q) = %q, want an error", c.target, c.path, got)
		}
	}
}

// The token and login macros build their URL as target + request-line path. An
// opaque request target must not move the macro request (which carries the
// login body) to another host.
func TestMacrosRefuseRetargetingRequestLine(t *testing.T) {
	var otherHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.Header().Set("Set-Cookie", "sid=stolen")
		_, _ = w.Write([]byte("token=abc"))
	}))
	defer other.Close()
	raw := "POST x:@" + strings.TrimPrefix(other.URL, "http://") + "/login HTTP/1.1\nHost: example.com\n\nuser=a&pass=b"

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, capture.New(st))

	if _, hdrs, err := s.TestLoginMacro(LoginMacro{Enabled: true, Target: target.URL, Request: raw}); err == nil || len(hdrs) != 0 {
		t.Errorf("login macro: headers=%v err=%v, want a refusal", hdrs, err)
	}
	s.SetMacro(Macro{Enabled: true, Target: target.URL, Request: raw, Extract: "token=(\\w+)", InjectName: "X-Token"})
	if tok, _, _ := s.macroToken(t.Context()); tok != "" {
		t.Errorf("token macro extracted %q from another host", tok)
	}
	if n := otherHits.Load(); n != 0 {
		t.Fatalf("macro request reached a host other than its target %d time(s)", n)
	}
}
