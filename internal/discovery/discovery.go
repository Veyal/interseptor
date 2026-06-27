// Package discovery is a scope-aware content-discovery (forced-browse) engine:
// it brute-forces paths from a wordlist against a base URL, calibrates a
// per-directory soft-404 signature so it doesn't drown in false hits, optionally
// recurses into discovered directories, and streams results as it goes.
//
// The engine is transport-agnostic: it sends through an injected Probe (so it is
// unit-testable without a network) and asks an injected scope predicate before
// touching any URL. The control layer wires a real HTTP client (honouring the
// upstream proxy and MITM TLS) and records found endpoints as flows.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	errNoBase        = errors.New("base URL required")
	errBadBase       = errors.New("base URL must be an absolute http(s) URL")
	errEmptyWordlist = errors.New("wordlist is empty")
	errBusy          = errors.New("a discovery run is already in progress")
	errNoProbe       = errors.New("no probe configured")
)

const (
	maxThreads     = 64
	defaultThreads = 20
	defaultMaxReq  = 20000 // total request budget per run — a runaway backstop
	maxDepthCap    = 8
)

// Spec configures one discovery run.
type Spec struct {
	BaseURL    string            // e.g. https://target/path/ — probes hang off here
	Words      []string          // wordlist (comments "#…" and blanks are ignored)
	Extensions []string          // e.g. [".php",".bak"] — each word is also tried bare
	Threads    int               // max concurrent probes (1–64)
	DelayMs    int               // delay between dispatching probes (throttle)
	Recursive  bool              // recurse into discovered directories
	MaxDepth   int               // recursion depth limit (0 = base level only)
	MatchCodes []int             // statuses considered "found" (empty = anything but 404/soft-404)
	HideCodes  []int             // statuses to suppress even if otherwise matched
	FilterLen  int64             // suppress results whose body length equals this (manual soft-404 filter)
	Headers    map[string]string // sent on every probe (auth cookies, tokens, …)
	MaxReq     int               // total request budget (0 = default)
}

// Outcome is what a Probe reports for a single URL.
type Outcome struct {
	Status      int
	Length      int64
	Body        []byte // a capped sample, used for word/line counts (optional)
	ContentType string
	Location    string // redirect target (for 3xx)
}

// Result is one discovered (or notable) path.
type Result struct {
	URL         string `json:"url"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	Length      int64  `json:"length"`
	Words       int    `json:"words"`
	Lines       int    `json:"lines"`
	ContentType string `json:"contentType,omitempty"`
	Redirect    string `json:"redirect,omitempty"`
	Depth       int    `json:"depth"`
	Dir         bool   `json:"dir"`
	Error       string `json:"error,omitempty"`
	FlowID      int64  `json:"flowId,omitempty"` // set when the control layer records this hit as a flow
}

// Probe issues a single request and reports the outcome. It must be safe for
// concurrent use.
type Probe func(ctx context.Context, method, rawURL string, headers map[string]string) (Outcome, error)

// State is a snapshot of a run for the API/UI.
type State struct {
	Running   bool     `json:"running"`
	BaseURL   string   `json:"baseUrl"`
	Tried     int      `json:"tried"`
	Found     int      `json:"found"`
	Results   []Result `json:"results"`
	StartedMs int64    `json:"startedMs"`
	DoneMs    int64    `json:"doneMs"`
	Note      string   `json:"note,omitempty"`
}

// Engine runs content-discovery jobs (one at a time).
type Engine struct {
	mu       sync.Mutex
	probe    Probe
	inScope  func(rawURL string) bool
	notify   func()
	recorder Recorder

	running   bool
	baseURL   string
	tried     int
	results   []Result
	startedMs int64
	doneMs    int64
	note      string
	cancel    context.CancelFunc
}

// Recorder is an optional hook the control layer uses to persist a found URL as a
// flow when recording is enabled. It runs outside the engine lock and may block.
type Recorder func(Result) int64

// New returns an idle engine. Wire a Probe (and optionally scope/notifier) before Start.
func New() *Engine { return &Engine{} }

// SetProbe sets the request transport. Required before Start.
func (e *Engine) SetProbe(p Probe) { e.mu.Lock(); e.probe = p; e.mu.Unlock() }

// SetScope sets the in-scope predicate; URLs it rejects are never probed. A nil
// predicate (the default) treats everything as in scope.
func (e *Engine) SetScope(fn func(rawURL string) bool) { e.mu.Lock(); e.inScope = fn; e.mu.Unlock() }

// SetNotifier registers a callback fired (outside the lock) whenever results or
// running-state change, so the control layer can push SSE updates.
func (e *Engine) SetNotifier(fn func()) { e.mu.Lock(); e.notify = fn; e.mu.Unlock() }

// SetRecorder registers an optional callback that persists each found result as
// a flow and returns its store id (0 when recording is off or fails).
func (e *Engine) SetRecorder(fn Recorder) { e.mu.Lock(); e.recorder = fn; e.mu.Unlock() }

func (e *Engine) fireNotify() {
	e.mu.Lock()
	fn := e.notify
	e.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// State returns a copy-safe snapshot of the current/last run.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := State{
		Running: e.running, BaseURL: e.baseURL, Tried: e.tried,
		Found: len(e.results), StartedMs: e.startedMs, DoneMs: e.doneMs, Note: e.note,
	}
	out.Results = append([]Result(nil), e.results...)
	return out
}

// Stop cancels an in-flight run (no-op if idle).
func (e *Engine) Stop() {
	e.mu.Lock()
	c := e.cancel
	e.mu.Unlock()
	if c != nil {
		c()
	}
}

// Start validates the spec and launches the run in a goroutine. It returns an
// error only for bad input; runtime/transport errors surface as Result.Error.
func (e *Engine) Start(spec Spec) error {
	base, err := normalizeBase(spec.BaseURL)
	if err != nil {
		return err
	}
	words := cleanWords(spec.Words)
	if len(words) == 0 {
		return errEmptyWordlist
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return errBusy
	}
	if e.probe == nil {
		e.mu.Unlock()
		return errNoProbe
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.running = true
	e.cancel = cancel
	e.baseURL = base
	e.tried = 0
	e.results = nil
	e.note = ""
	e.startedMs = time.Now().UnixMilli()
	e.doneMs = 0
	e.mu.Unlock()
	e.fireNotify()

	go e.run(ctx, base, words, spec)
	return nil
}

// run executes the BFS over directories: calibrate → probe candidates → enqueue
// discovered subdirectories (when recursive), bounded by depth and a request budget.
func (e *Engine) run(ctx context.Context, base string, words []string, spec Spec) {
	threads := clamp(spec.Threads, 1, maxThreads)
	if spec.Threads == 0 {
		threads = defaultThreads
	}
	maxDepth := clamp(spec.MaxDepth, 0, maxDepthCap)
	budget := spec.MaxReq
	if budget <= 0 {
		budget = defaultMaxReq
	}
	exts := normalizeExts(spec.Extensions)
	matchSet := intSet(spec.MatchCodes)
	hideSet := intSet(spec.HideCodes)

	type dir struct {
		url   string
		depth int
	}
	queue := []dir{{url: base, depth: 0}}
	seenDir := map[string]bool{base: true}

	budgetHit := false // request budget reached — stop launching probes (a local flag,
	// NOT a ctx reassignment, so the worker goroutines reading ctx never race a write)
	for len(queue) > 0 {
		if ctx.Err() != nil || budgetHit {
			break
		}
		d := queue[0]
		queue = queue[1:]

		soft := e.calibrate(ctx, d.url, exts, spec.Headers)

		var (
			wg    sync.WaitGroup
			sem   = make(chan struct{}, threads)
			mu    sync.Mutex
			subs  []dir
			first = true
		)
		for _, w := range words {
			if budgetHit {
				break
			}
			for _, ext := range exts {
				if ctx.Err() != nil || budgetHit {
					break
				}
				e.mu.Lock()
				over := e.tried >= budget
				e.mu.Unlock()
				if over {
					e.setNote("stopped at request budget (" + itoa(budget) + ") — narrow the wordlist or raise the limit")
					budgetHit = true
					break
				}
				raw := d.url + w + ext
				if e.inScope != nil && !e.inScope(raw) {
					continue
				}
				if spec.DelayMs > 0 && !first {
					time.Sleep(time.Duration(spec.DelayMs) * time.Millisecond)
				}
				first = false
				sem <- struct{}{}
				wg.Add(1)
				go func(raw, path string, depth int) {
					defer wg.Done()
					defer func() { <-sem }()
					out, err := e.probe(ctx, "GET", raw, spec.Headers)
					e.mu.Lock()
					e.tried++
					e.mu.Unlock()
					if err != nil {
						return // transport error: not a finding, just skip
					}
					if !found(out, matchSet, hideSet, soft, spec.FilterLen) {
						return
					}
					res := buildResult(raw, path, depth, out)
					e.appendResult(res)
					if res.Dir && spec.Recursive && depth < maxDepth {
						child := strings.TrimRight(raw, "/") + "/"
						mu.Lock()
						if !seenDir[child] {
							seenDir[child] = true
							subs = append(subs, dir{url: child, depth: depth + 1})
						}
						mu.Unlock()
					}
				}(raw, pathOf(base, raw), d.depth)
			}
		}
		wg.Wait()
		queue = append(queue, subs...)
	}

	e.mu.Lock()
	e.running = false
	e.doneMs = time.Now().UnixMilli()
	e.cancel = nil
	e.mu.Unlock()
	e.fireNotify()
}

// calibrate probes an unlikely random path in dir to learn the "not found"
// signature (status + body length). Pages that soft-404 (return 200 for missing
// content) are then filtered. Returns nil if the dir genuinely 404s (clean).
func (e *Engine) calibrate(ctx context.Context, dir string, exts []string, headers map[string]string) *Outcome {
	token := "ic-" + randToken() + firstExt(exts)
	out, err := e.probe(ctx, "GET", dir+token, headers)
	e.mu.Lock()
	e.tried++
	e.mu.Unlock()
	if err != nil {
		return nil
	}
	if out.Status == 404 {
		return nil // honest 404s — no soft-404 filtering needed
	}
	sig := out
	return &sig
}

func (e *Engine) appendResult(res Result) {
	e.mu.Lock()
	rec := e.recorder
	e.mu.Unlock()
	if rec != nil && res.Status != 0 && res.Error == "" {
		if id := rec(res); id > 0 {
			res.FlowID = id
		}
	}
	e.mu.Lock()
	e.results = append(e.results, res)
	// Keep results stably ordered by depth then path for a readable, deterministic UI.
	sort.SliceStable(e.results, func(i, j int) bool {
		if e.results[i].Depth != e.results[j].Depth {
			return e.results[i].Depth < e.results[j].Depth
		}
		return e.results[i].Path < e.results[j].Path
	})
	e.mu.Unlock()
	e.fireNotify()
}

func (e *Engine) setNote(s string) {
	e.mu.Lock()
	e.note = s
	e.mu.Unlock()
}

// found applies the match/hide/soft-404/length rules to one outcome.
func found(out Outcome, matchSet, hideSet map[int]bool, soft *Outcome, filterLen int64) bool {
	if out.Status == 0 || out.Status == 404 {
		return false
	}
	if soft != nil && out.Status == soft.Status && lenClose(out.Length, soft.Length) {
		return false
	}
	if hideSet[out.Status] {
		return false
	}
	if filterLen > 0 && out.Length == filterLen {
		return false
	}
	if len(matchSet) > 0 {
		return matchSet[out.Status]
	}
	return true // default: anything that isn't a 404 / soft-404 is interesting
}

// lenClose reports whether two body lengths are within a soft-404 tolerance.
func lenClose(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	tol := b / 20
	if tol < 32 {
		tol = 32
	}
	return d <= tol
}

func buildResult(raw, path string, depth int, out Outcome) Result {
	r := Result{
		URL: raw, Path: path, Status: out.Status, Length: out.Length,
		ContentType: out.ContentType, Redirect: out.Location, Depth: depth,
	}
	if len(out.Body) > 0 {
		r.Words = len(strings.Fields(string(out.Body)))
		r.Lines = strings.Count(string(out.Body), "\n")
	}
	// A directory: a redirect whose target is just this path + "/", or a 2xx on a
	// path that already ends with "/".
	switch out.Status {
	case 301, 302, 307, 308:
		if redirectAddsSlash(raw, out.Location) {
			r.Dir = true
		}
	case 200, 201, 202, 203, 204, 206:
		if strings.HasSuffix(raw, "/") {
			r.Dir = true
		}
	}
	return r
}

// redirectAddsSlash reports whether location is just raw with a trailing slash
// appended (the classic "this is a directory" redirect).
func redirectAddsSlash(raw, location string) bool {
	if location == "" {
		return false
	}
	want := strings.TrimRight(raw, "/") + "/"
	// Compare on path suffix so absolute and relative redirects both match.
	return location == want || strings.HasSuffix(location, pathSuffix(want))
}

func pathSuffix(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.IndexByte(u[i+3:], '/'); j >= 0 {
			return u[i+3+j:]
		}
	}
	return u
}

// ---- helpers ----

func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errNoBase
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errBadBase
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func cleanWords(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, w := range in {
		w = strings.TrimSpace(w)
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		w = strings.TrimPrefix(w, "/")
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// normalizeExts returns the extension set to try per word: always the bare word
// ("") plus each provided extension (each normalized to a leading ".").
func normalizeExts(in []string) []string {
	out := []string{""}
	seen := map[string]bool{"": true}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

func firstExt(exts []string) string {
	for _, e := range exts {
		if e != "" {
			return e
		}
	}
	return ""
}

func intSet(in []int) map[int]bool {
	if len(in) == 0 {
		return nil
	}
	m := make(map[int]bool, len(in))
	for _, n := range in {
		m[n] = true
	}
	return m
}

// pathOf returns the path of raw relative to base's origin (leading slash kept).
func pathOf(base, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func randToken() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0a1b2c3d"
	}
	return hex.EncodeToString(b[:])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
