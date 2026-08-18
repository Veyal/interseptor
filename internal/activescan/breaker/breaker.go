package breaker

import "sync"

// SkipThreshold is how many consecutive identical statuses trigger a skip.
const SkipThreshold = 5

// TimeoutThreshold is consecutive errors/timeouts before skip.
const TimeoutThreshold = 3

var skipStatuses = map[int]bool{
	419: true, 401: true, 403: true, 502: true,
}

// Skipped describes an endpoint removed from the active-scan queue.
type Skipped struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Method string `json:"method"`
	Reason string `json:"reason"`
}

// Tracker skips endpoints after repeated failure statuses or transport errors.
type Tracker struct {
	mu     sync.Mutex
	streak map[string]struct {
		status int
		n      int
	}
	errs    map[string]int
	skipped map[string]Skipped
}

// New returns a fresh per-run tracker.
func New() *Tracker {
	return &Tracker{
		streak: map[string]struct {
			status int
			n      int
		}{},
		errs:    map[string]int{},
		skipped: map[string]Skipped{},
	}
}

// Key builds a stable endpoint signature.
func Key(method, host, path string) string {
	return method + " " + host + " " + path
}

// ShouldSkip reports whether probing should stop for this endpoint.
func (t *Tracker) ShouldSkip(key string) (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.skipped[key]; ok {
		return true, s.Reason
	}
	return false, ""
}

// Record observes one probe result; may mark the endpoint skipped.
func (t *Tracker) Record(key, method, host, path string, status int, transportErr bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.skipped[key]; ok {
		return
	}
	if transportErr || status == 0 || status == 502 {
		t.errs[key]++
		if t.errs[key] >= TimeoutThreshold {
			t.mark(key, method, host, path, "transport_x"+itoa(TimeoutThreshold))
		}
		return
	}
	t.errs[key] = 0
	if !skipStatuses[status] {
		t.streak[key] = struct {
			status int
			n      int
		}{status, 0}
		return
	}
	st := t.streak[key]
	if st.status == status {
		st.n++
	} else {
		st = struct {
			status int
			n      int
		}{status, 1}
	}
	t.streak[key] = st
	if st.n >= SkipThreshold {
		t.mark(key, method, host, path, statusLabel(status)+"_x"+itoa(SkipThreshold))
	}
}

func (t *Tracker) mark(key, method, host, path, reason string) {
	t.skipped[key] = Skipped{Host: host, Path: path, Method: method, Reason: reason}
}

// SkippedList returns all skipped endpoints for this run.
func (t *Tracker) SkippedList() []Skipped {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Skipped, 0, len(t.skipped))
	for _, s := range t.skipped {
		out = append(out, s)
	}
	return out
}

func statusLabel(code int) string {
	switch code {
	case 419:
		return "csrf_419"
	case 401:
		return "auth_401"
	case 403:
		return "forbidden_403"
	case 502:
		return "bad_gateway_502"
	default:
		return "status_" + itoa(code)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
