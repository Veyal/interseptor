package intruder

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseBackoffHeaders(t *testing.T) {
	cap := 5 * time.Second
	cases := []struct {
		name string
		h    map[string][]string
		want time.Duration
		ok   bool
	}{
		{"retry-after seconds", map[string][]string{"Retry-After": {"2"}}, 2 * time.Second, true},
		{"ratelimit-reset", map[string][]string{"RateLimit-Reset": {"3"}}, 3 * time.Second, true},
		{"x-ratelimit-reset", map[string][]string{"X-RateLimit-Reset": {"1"}}, 1 * time.Second, true},
		{"clamped to cap", map[string][]string{"Retry-After": {"999"}}, cap, true},
		{"retry-after preferred", map[string][]string{"Retry-After": {"2"}, "RateLimit-Reset": {"4"}}, 2 * time.Second, true},
		{"none", map[string][]string{"X-Other": {"1"}}, 0, false},
		{"zero ignored", map[string][]string{"Retry-After": {"0"}}, 0, false},
	}
	for _, c := range cases {
		got, ok := parseBackoff(c.h, cap)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: parseBackoff = %v,%v want %v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// A 429 carrying Retry-After pauses the dispatcher before the next request.
func TestAdaptiveBackoffPausesDispatch(t *testing.T) {
	var n int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "1") // clamped to MaxBackoffMs below
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	e := newEngine(t)
	start := time.Now()
	if err := e.Start(Spec{
		Target:       upstream.URL,
		Template:     "GET / HTTP/1.1\nHost: h\n\n",
		AttackType:   "repeat",
		Repeat:       2,
		Threads:      1,
		MaxBackoffMs: 300, // Retry-After:1s clamps to 300ms
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	elapsed := time.Since(start)
	if len(st.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(st.Results))
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("expected a backoff pause after 429, elapsed=%s", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("backoff should be clamped to MaxBackoffMs, elapsed=%s", elapsed)
	}
}
