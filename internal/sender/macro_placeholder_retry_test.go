package sender

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Placeholder substitution must not consume the caller's template: the 401
// re-auth retry has to inject the token fetched for the retry, and the
// caller's header map must keep its placeholder.
func TestMacroPlaceholderRetryUsesFreshToken(t *testing.T) {
	var macroCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/macro":
			fmt.Fprintf(w, "t=tok%d", macroCalls.Add(1))
		case "/login":
			w.WriteHeader(http.StatusOK)
		default:
			if r.Header.Get("X-CSRF") == "tok2" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer upstream.Close()

	snd := newTestSender(t)
	snd.SetMacro(Macro{Enabled: true, Target: upstream.URL, Request: "GET /macro HTTP/1.1\r\nHost: x\r\n\r\n",
		Extract: `t=(tok\d+)`, InjectMode: "placeholder", InjectName: "§CSRF§"})
	if err := snd.SetLoginMacro(LoginMacro{Enabled: true, ReauthOn401: true, Target: upstream.URL,
		Request: "GET /login HTTP/1.1\r\nHost: x\r\n\r\n"}); err != nil {
		t.Fatal(err)
	}
	headers := map[string][]string{"X-CSRF": {"§CSRF§"}}
	f, err := snd.Send(Request{Method: "GET", URL: upstream.URL + "/api", Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (retry must re-substitute a fresh token)", f.Status)
	}
	if got := headers["X-CSRF"][0]; got != "§CSRF§" {
		t.Fatalf("caller's headers mutated: X-CSRF = %q", got)
	}
}
