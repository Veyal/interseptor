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

// Hit is a confirmed detection from a check.
type Hit struct {
	Evidence string
	FlowID   int64
}

// Check is one active vulnerability check. Run is given the point, the unmutated
// baseline response, and a probe to fire payloads; it returns a Hit or nil.
type Check struct {
	Class, Severity, Title, Fix string
	Run                         func(p Point, baseline Response, probe Prober) *Hit
}

// Finding is a confirmed active-scan result (shaped like a scanner issue).
type Finding struct {
	Class    string `json:"class"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Fix      string `json:"fix"`
	Point    Point  `json:"point"`
	Evidence string `json:"evidence"`
	FlowID   int64  `json:"flowId"`
}

// Options bound a run.
type Options struct {
	MaxRequests int // hard cap on probes (default 800)
	Concurrency int // parallel point×check tasks (default 6)
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
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].Kind != pts[j].Kind {
			return pts[i].Kind < pts[j].Kind
		}
		return pts[i].Name < pts[j].Name
	})
	return pts
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
	for _, p := range points {
		for _, c := range Checks {
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
					mu.Lock()
					findings = append(findings, Finding{
						Class: c.Class, Severity: c.Severity, Title: c.Title, Fix: c.Fix,
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
