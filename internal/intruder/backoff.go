package intruder

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultMaxBackoff caps one adaptive pause when the spec does not set one.
	defaultMaxBackoff = 120 * time.Second
	// hardMaxBackoff bounds even an operator-supplied cap so a hostile
	// Retry-After cannot stall a run indefinitely.
	hardMaxBackoff = 10 * time.Minute
)

// backoffCap resolves the per-pause cap from the spec.
func backoffCap(maxBackoffMs int) time.Duration {
	if maxBackoffMs == 0 {
		return defaultMaxBackoff
	}
	d := time.Duration(maxBackoffMs) * time.Millisecond
	if d > hardMaxBackoff {
		return hardMaxBackoff
	}
	return d
}

// parseBackoff reads a pacing hint from a 429/503 response's headers:
// Retry-After (seconds or an HTTP-date), else RateLimit-Reset, else
// X-RateLimit-Reset (both seconds remaining). The value is clamped to cap. It
// returns ok=false when no usable, positive hint is present.
func parseBackoff(h map[string][]string, cap time.Duration) (time.Duration, bool) {
	if d, ok := parseRetryAfter(headerVal(h, "Retry-After")); ok {
		return clampBackoff(d, cap), d > 0
	}
	for _, name := range []string{"RateLimit-Reset", "X-RateLimit-Reset"} {
		if secs, ok := parseSeconds(headerVal(h, name)); ok {
			d := time.Duration(secs) * time.Second
			return clampBackoff(d, cap), d > 0
		}
	}
	return 0, false
}

func clampBackoff(d, cap time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > cap {
		return cap
	}
	return d
}

// parseRetryAfter handles the two RFC 7231 forms: delta-seconds or an HTTP-date.
func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, ok := parseSeconds(v); ok {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func parseSeconds(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// extendPause moves the dispatch resume time out to at least until.
func (e *Engine) extendPause(until time.Time) {
	e.mu.Lock()
	if until.After(e.pauseUntil) {
		e.pauseUntil = until
	}
	e.mu.Unlock()
}

// waitBackoff blocks the dispatcher until the current pause (if any) elapses,
// re-reading it so a pause extended by an in-flight 429 is honoured. It returns
// false when ctx is cancelled.
func (e *Engine) waitBackoff(ctx context.Context) bool {
	for {
		e.mu.Lock()
		wait := time.Until(e.pauseUntil)
		e.mu.Unlock()
		if wait <= 0 {
			return true
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
}

// noteRateLimit records an adaptive pause when a response asks for one.
func (e *Engine) noteRateLimit(status int, resHeaders map[string][]string, cap time.Duration) {
	if cap < 0 || (status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable) {
		return
	}
	if d, ok := parseBackoff(resHeaders, cap); ok && d > 0 {
		e.extendPause(time.Now().Add(d))
	}
}
