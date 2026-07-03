// Package activescan is the deterministic active-scan engine: it enumerates the
// injection points of a request, fires per-class payloads at them through a
// caller-supplied sender, and confirms vulnerabilities with detectors. It has no
// dependency on the proxy/control — the caller wires a SendFunc (normally the
// `sender`, so probes are recorded and session-auth applied) and the scope gate.
//
// It is "without AI" by construction; the AI operates the SAME engine over MCP.
package activescan

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Point is one injection location in a request.
type Point struct {
	Kind  string `json:"kind"` // "query" | "form" | "json"
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Target is the request being scanned.
type Target struct {
	Method  string
	URL     string
	Headers http.Header
	Body    string
}

// Response is one probe's result.
type Response struct {
	FlowID   int64
	Status   int
	Headers  http.Header
	Body     string
	Duration time.Duration
}

// SendFunc issues a (possibly mutated) request and returns its response. It must
// be safe for concurrent use.
type SendFunc func(Target) Response

// Prober sends one payload into the point under test and returns the response.
type Prober func(payload string) Response

// Hit is a confirmed detection from a check. The override fields (Title/Severity/
// Detail/Fix) are empty for the built-in checks (which carry those on the Check
// itself) but populated by user-authored checks so each finding is self-describing.
type Hit struct {
	Evidence string
	FlowID   int64
	Title    string
	Severity string
	Detail   string
	Fix      string
}

// Check is one active vulnerability check. Run is given the point, the unmutated
// baseline response, and a probe to fire payloads; it returns a Hit or nil.
type Check struct {
	ID                         string
	Class, Severity, Title, Fix string
	Run                        func(p Point, baseline Response, probe Prober) *Hit
}

// Finding is a confirmed active-scan result (shaped like a scanner issue).
type Finding struct {
	Class    string `json:"class"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix"`
	Point    Point  `json:"point"`
	Evidence string `json:"evidence"`
	FlowID   int64  `json:"flowId"`
}

// Options bound a run.
type Options struct {
	MaxRequests int            // hard cap on probes (default 800)
	Concurrency int            // parallel point×check tasks (default 6)
	Disabled    map[string]bool
	Custom      []Check // user-authored (Starlark) active checks, in addition to Checks
}

// Points enumerates query, form-body, and top-level JSON-body injection points.
func Points(t Target) []Point {
	var pts []Point
	if u, err := url.Parse(t.URL); err == nil {
		for k, vs := range u.Query() {
			pts = append(pts, Point{"query", k, first(vs)})
		}
	}
	body := strings.TrimSpace(t.Body)
	if body != "" {
		ct := ""
		if t.Headers != nil {
			ct = t.Headers.Get("Content-Type")
		}
		if isXMLBody(ct, body) {
			// A single "body" point carries the entire XML body so that the
			// XXE check can replace the whole document atomically.
			// XML is checked first because XML bodies can contain '=' (e.g.
			// attribute values) that would otherwise misfire the form heuristic.
			pts = append(pts, Point{"body", "_xml", body})
		} else if strings.Contains(ct, "json") || body[0] == '{' {
			var m map[string]any
			if json.Unmarshal([]byte(t.Body), &m) == nil {
				for k, v := range m {
					if isScalar(v) {
						pts = append(pts, Point{"json", k, fmt.Sprint(v)})
					}
				}
			}
		} else if strings.Contains(t.Body, "=") {
			if vals, err := url.ParseQuery(t.Body); err == nil {
				for k, vs := range vals {
					pts = append(pts, Point{"form", k, first(vs)})
				}
			}
		}
	}

	// Path segments — each non-empty segment is an injection point (LFI, SQLi and
	// traversal frequently live in RESTful path components, not just query args).
	// Named by segment index so With() can replace exactly that segment.
	if u, err := url.Parse(t.URL); err == nil {
		segs := strings.Split(u.EscapedPath(), "/")
		for i, s := range segs {
			if s == "" {
				continue
			}
			pts = append(pts, Point{"path", strconv.Itoa(i), s})
		}
	}

	// Cookie values — parsed from the request Cookie header. Cookies reach the same
	// backend queries/filters as params and are a classic injection surface.
	if t.Headers != nil {
		for name, val := range parseCookies(t.Headers.Get("Cookie")) {
			pts = append(pts, Point{"cookie", name, val})
		}
	}

	// Header injection points — a fixed, curated set of headers that backends
	// commonly trust or reflect (log-injection SQLi via X-Forwarded-For, host-header
	// poisoning via X-Forwarded-Host, CORS reflection via Origin, …). Synthesized
	// even when absent so the specialized checks always have a point to fire on. The
	// request's Host header is deliberately NOT mutated — that would break vhost
	// routing; X-Forwarded-Host is the real poisoning vector anyway.
	for _, hn := range injectableHeaders {
		v := ""
		if t.Headers != nil {
			v = t.Headers.Get(hn)
		}
		pts = append(pts, Point{"header", hn, v})
	}

	sort.SliceStable(pts, func(i, j int) bool {
		if pk, qk := kindPriority(pts[i].Kind), kindPriority(pts[j].Kind); pk != qk {
			return pk < qk
		}
		if pts[i].Kind != pts[j].Kind {
			return pts[i].Kind < pts[j].Kind
		}
		return pts[i].Name < pts[j].Name
	})
	return pts
}

// injectableHeaders is the curated set of request headers turned into injection
// points. Kept small and fixed so the per-header probe cost stays bounded.
var injectableHeaders = []string{
	"X-Forwarded-For", "X-Forwarded-Host", "Origin", "Referer", "User-Agent",
}

// kindPriority orders injection points so the classic, highest-value body/query
// points are scanned before the broader header/cookie/path surface. Under a tight
// request budget this keeps coverage focused where injections are most likely.
func kindPriority(kind string) int {
	switch kind {
	case "query":
		return 0
	case "json":
		return 1
	case "form":
		return 2
	case "body":
		return 3
	case "path":
		return 4
	case "cookie":
		return 5
	case "header":
		return 6
	default:
		return 9
	}
}

// parseCookies splits a Cookie request-header value into name→value pairs. It is
// lenient (whitespace-trimmed, ignores malformed/valueless entries) — good enough
// to enumerate injection points without a full RFC 6265 parse.
func parseCookies(header string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// setCookie returns header with cookie name's value replaced by val (or the pair
// appended if not present), preserving the other cookies' order.
func setCookie(header, name, val string) string {
	var parts []string
	found := false
	for _, part := range strings.Split(header, ";") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		k, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(k) == name {
			parts = append(parts, name+"="+val)
			found = true
			continue
		}
		parts = append(parts, trimmed)
	}
	if !found {
		parts = append(parts, name+"="+val)
	}
	return strings.Join(parts, "; ")
}

// cloneHeader returns a deep copy of h (or a fresh Header if h is nil), so a probe
// can mutate request headers without corrupting the shared baseline Target.
func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// isXMLBody reports whether the Content-Type or the body prefix indicates XML.
// It deliberately avoids matching generic HTML (which also starts with '<') by
// requiring either an XML-indicating content-type or a leading XML declaration.
func isXMLBody(ct, body string) bool {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "xml") {
		return true
	}
	// Detect bare XML without a declared content-type via the standard XML prolog.
	return strings.HasPrefix(body, "<?xml")
}

// With returns a copy of t with point p's value replaced by payload.
func (t Target) With(p Point, payload string) Target {
	out := t
	switch p.Kind {
	case "query":
		if u, err := url.Parse(t.URL); err == nil {
			q := u.Query()
			q.Set(p.Name, payload)
			u.RawQuery = q.Encode()
			out.URL = u.String()
		}
	case "form":
		if vals, err := url.ParseQuery(t.Body); err == nil {
			vals.Set(p.Name, payload)
			out.Body = vals.Encode()
		}
	case "json":
		var m map[string]any
		if json.Unmarshal([]byte(t.Body), &m) == nil {
			m[p.Name] = payload
			if b, err := json.Marshal(m); err == nil {
				out.Body = string(b)
			}
		}
	case "body":
		// Wholesale body replacement (used by the XXE check which rewrites the
		// entire XML document rather than a single field value).
		out.Body = payload
	case "path":
		if u, err := url.Parse(t.URL); err == nil {
			segs := strings.Split(u.EscapedPath(), "/")
			if idx, err := strconv.Atoi(p.Name); err == nil && idx >= 0 && idx < len(segs) {
				segs[idx] = payload // raw payload — traversal sequences (../, %2e) survive
				newPath := strings.Join(segs, "/")
				u.RawPath = newPath
				if dec, e := url.PathUnescape(newPath); e == nil {
					u.Path = dec
				} else {
					u.Path = newPath
				}
				out.URL = u.String()
			}
		}
	case "header":
		out.Headers = cloneHeader(t.Headers)
		out.Headers.Set(p.Name, payload)
	case "cookie":
		out.Headers = cloneHeader(t.Headers)
		out.Headers.Set("Cookie", setCookie(t.Headers.Get("Cookie"), p.Name, payload))
	}
	return out
}

// Run scans t with every built-in Check, bounded by opts and cancellable via ctx
// (the kill switch). Returns the findings and the number of requests issued.
func Run(ctx context.Context, t Target, send SendFunc, opts Options) ([]Finding, int) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 6
	}
	if opts.MaxRequests <= 0 {
		opts.MaxRequests = 800
	}
	points := Points(t)
	baseline := send(t)

	var (
		mu       sync.Mutex
		findings []Finding
		count    = 1 // baseline
	)
	take := func() bool { // reserve one request from the budget
		mu.Lock()
		defer mu.Unlock()
		if count >= opts.MaxRequests {
			return false
		}
		count++
		return true
	}

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	// Built-in probes plus any user-authored (Starlark) active checks. A custom
	// check whose ID matches a built-in replaces that built-in for this run.
	checks := mergeChecks(Checks, opts.Custom)
	for _, p := range points {
		for _, c := range checks {
			if opts.Disabled != nil && opts.Disabled[c.ID] {
				continue // user toggled this module off in the Checks manager
			}
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(p Point, c Check) {
				defer wg.Done()
				defer func() { <-sem }()
				probe := func(payload string) Response {
					if ctx.Err() != nil || !take() {
						return Response{}
					}
					return send(t.With(p, payload))
				}
				if hit := c.Run(p, baseline, probe); hit != nil {
					sev, title, fix := c.Severity, c.Title, c.Fix
					if hit.Severity != "" {
						sev = hit.Severity
					}
					if hit.Title != "" {
						title = hit.Title
					}
					if hit.Fix != "" {
						fix = hit.Fix
					}
					mu.Lock()
					findings = append(findings, Finding{
						Class: c.Class, Severity: sev, Title: title, Detail: hit.Detail, Fix: fix,
						Point: p, Evidence: hit.Evidence, FlowID: hit.FlowID,
					})
					mu.Unlock()
				}
			}(p, c)
		}
	}
	wg.Wait()
	return findings, count
}

// mergeChecks interleaves built-in probes with custom checks; a custom check with
// the same ID replaces the built-in (Starlark override).
func mergeChecks(builtin, custom []Check) []Check {
	byID := make(map[string]Check, len(builtin)+len(custom))
	order := make([]string, 0, len(builtin)+len(custom))
	for _, c := range builtin {
		byID[c.ID] = c
		order = append(order, c.ID)
	}
	for _, c := range custom {
		if _, ok := byID[c.ID]; ok {
			byID[c.ID] = c
		} else {
			byID[c.ID] = c
			order = append(order, c.ID)
		}
	}
	out := make([]Check, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

// ---- helpers ----

func first(vs []string) string {
	if len(vs) > 0 {
		return vs[0]
	}
	return ""
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, json.Number:
		return true
	}
	return false
}

// mark returns a unique, distinctive marker for reflection checks.
func mark() string {
	var b [4]byte
	rand.Read(b[:])
	return fmt.Sprintf("itx%x", b)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func absdiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
