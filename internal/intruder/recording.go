package intruder

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxRLHeaderValue = 128
	barrierMaxWait   = 2 * time.Second
)

// SpecSummary is the non-secret description of a finished run: no template
// and no payload lists, only the shape of the attack.
type SpecSummary struct {
	Attack       string   `json:"attack"`
	Target       string   `json:"target"` // host only
	Threads      int      `json:"threads"`
	DelayMs      int      `json:"delayMs,omitempty"`
	Repeat       int      `json:"repeat,omitempty"`
	Barrier      bool     `json:"barrier,omitempty"`
	Method       string   `json:"method,omitempty"` // request method from the template
	Path         string   `json:"path,omitempty"`   // request path only; query values are never kept
	GrepMatch    string   `json:"grepMatch,omitempty"`
	GrepExtract  string   `json:"grepExtract,omitempty"`
	ProcessRules []string `json:"processRules,omitempty"`
}

// RunRecord is what the run sink receives once a run finishes or is stopped.
type RunRecord struct {
	State State       `json:"state"`
	Spec  SpecSummary `json:"spec"`
}

// SetRunSink registers a callback invoked once per run when it finishes or is
// stopped. It is called outside the engine lock.
func (e *Engine) SetRunSink(fn func(RunRecord)) {
	e.mu.Lock()
	e.sink = fn
	e.mu.Unlock()
}

func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to a clock-derived id so runs stay distinct.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (8 * i))
		}
	}
	return hex.EncodeToString(b[:])
}

// hostOnly returns just the host[:port] of a target URL (no userinfo, path or query).
func hostOnly(target string) string {
	u, err := url.Parse(strings.TrimSpace(target))
	if err == nil && u.Host != "" {
		return u.Host
	}
	s := target
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func clampThreads(n int) int {
	if n < 1 {
		return 1
	}
	if n > 64 {
		return 64
	}
	return n
}

// rlHeaders extracts the whitelisted rate-limit headers (lowercased names,
// values capped) from a response header map.
func rlHeaders(h map[string][]string) map[string]string {
	var out map[string]string
	for k, v := range h {
		name := strings.ToLower(k)
		if !isRLHeader(name) || len(v) == 0 {
			continue
		}
		val := strings.Join(v, ", ")
		if len(val) > maxRLHeaderValue {
			val = val[:maxRLHeaderValue]
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = val
	}
	return out
}

func isRLHeader(name string) bool {
	return name == "retry-after" || name == "x-retry-after" || name == "ratelimit" ||
		strings.HasPrefix(name, "x-ratelimit-") || strings.HasPrefix(name, "ratelimit-")
}

// barrier parks the first n workers until all have arrived (or a cap elapses).
type barrier struct {
	mu       sync.Mutex
	n        int
	arrived  int
	release  chan struct{}
	timedOut bool
}

func newBarrier(n int) *barrier { return &barrier{n: n, release: make(chan struct{})} }

// wait blocks until every worker arrived, the cap elapses, or done fires.
// It reports whether the barrier released with all workers parked.
func (b *barrier) wait(done <-chan struct{}) bool {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	timer := time.NewTimer(barrierMaxWait)
	defer timer.Stop()
	select {
	case <-b.release:
		return true
	case <-timer.C:
		b.mu.Lock()
		b.timedOut = true
		b.mu.Unlock()
		return false
	case <-done:
		return false
	}
}

func (b *barrier) failed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timedOut
}

// emitRunRecord hands the finished run to the sink, outside the lock.
func (e *Engine) emitRunRecord() {
	e.mu.Lock()
	fn := e.sink
	e.mu.Unlock()
	if fn == nil {
		return
	}
	st := e.State()
	e.mu.Lock()
	sp := e.spec
	e.mu.Unlock()
	fn(RunRecord{State: st, Spec: sp})
}

// endpointOf extracts the method and query-free path from a raw request
// template's request line. Unparseable input yields empty strings.
func endpointOf(template string) (method, path string) {
	line, _, _ := strings.Cut(template, "\n")
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", ""
	}
	path, _, _ = strings.Cut(f[1], "?")
	return strings.ToUpper(f[0]), path
}
