package activescan

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- injection points + mutation ----

func TestPointsAndMutation(t *testing.T) {
	// query
	q := Points(Target{Method: "GET", URL: "http://h/p?a=1&b=2"})
	if len(q) != 2 {
		t.Fatalf("expected 2 query points, got %+v", q)
	}
	mutated := (Target{URL: "http://h/p?a=1&b=2"}).With(Point{"query", "a", "1"}, "PWN")
	if !strings.Contains(mutated.URL, "a=PWN") || !strings.Contains(mutated.URL, "b=2") {
		t.Fatalf("query mutation wrong: %s", mutated.URL)
	}
	// form
	hdr := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	f := Points(Target{Method: "POST", URL: "http://h/p", Headers: hdr, Body: "x=1&y=2"})
	if len(f) != 2 {
		t.Fatalf("expected 2 form points, got %+v", f)
	}
	// json
	jt := Target{Method: "POST", URL: "http://h/p", Headers: http.Header{"Content-Type": {"application/json"}}, Body: `{"u":"bob","n":5}`}
	jp := Points(jt)
	if len(jp) != 2 {
		t.Fatalf("expected 2 json points, got %+v", jp)
	}
	jm := jt.With(Point{"json", "u", "bob"}, "PWN")
	if !strings.Contains(jm.Body, `"u":"PWN"`) {
		t.Fatalf("json mutation wrong: %s", jm.Body)
	}
}

// reflectProbe returns a response that places the payload into a body template.
func reflectProbe(tmpl string) Prober {
	return func(payload string) Response {
		return Response{Status: 200, FlowID: 1, Body: strings.Replace(tmpl, "@", payload, 1)}
	}
}

func TestXSSDetector(t *testing.T) {
	if xssCheck.Run(Point{}, Response{Body: "<html></html>"}, reflectProbe("<div>@</div>")) == nil {
		t.Fatal("expected XSS hit when payload reflects unencoded")
	}
	// encoded reflection → no hit
	enc := func(payload string) Response {
		return Response{Status: 200, Body: "<div>" + html.EscapeString(payload) + "</div>"}
	}
	if xssCheck.Run(Point{}, Response{Body: "<html></html>"}, enc) != nil {
		t.Fatal("encoded reflection must not be flagged")
	}
}

func TestSQLiErrorDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "'") {
			return Response{Status: 500, FlowID: 2, Body: "You have an error in your SQL syntax near '''"}
		}
		return Response{Status: 200, Body: "ok"}
	}
	if h := sqliErrorCheck.Run(Point{Value: "1"}, Response{Status: 200, Body: "ok"}, probe); h == nil {
		t.Fatal("expected error-based SQLi hit")
	}
	clean := func(payload string) Response { return Response{Status: 200, Body: "ok"} }
	if sqliErrorCheck.Run(Point{Value: "1"}, Response{Status: 200, Body: "ok"}, clean) != nil {
		t.Fatal("no SQL error → no hit")
	}
}

func TestSQLiBooleanDetector(t *testing.T) {
	base := Response{Status: 200, Body: strings.Repeat("row", 100)}
	probe := func(payload string) Response {
		if strings.Contains(payload, "1'='1") {
			return Response{Status: 200, FlowID: 3, Body: strings.Repeat("row", 100)} // true ≈ baseline
		}
		return Response{Status: 200, FlowID: 4, Body: "no results"} // false diverges
	}
	if sqliBooleanCheck.Run(Point{Value: "1"}, base, probe) == nil {
		t.Fatal("expected boolean SQLi hit")
	}
}

func TestSSTIDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "7*731") {
			return Response{Status: 200, FlowID: 5, Body: "Hello 5117!"}
		}
		return Response{Status: 200, Body: "Hello"}
	}
	if sstiCheck.Run(Point{}, Response{Body: "Hello"}, probe) == nil {
		t.Fatal("expected SSTI hit when 7*731 evaluates to 5117")
	}
}

func TestOpenRedirectDetector(t *testing.T) {
	probe := func(payload string) Response {
		return Response{Status: 302, FlowID: 6, Headers: http.Header{"Location": {payload}}}
	}
	if openRedirectCheck.Run(Point{}, Response{}, probe) == nil {
		t.Fatal("expected open-redirect hit when Location echoes the canary")
	}
	// same-host redirect → no hit
	safe := func(payload string) Response {
		return Response{Status: 302, Headers: http.Header{"Location": {"/dashboard"}}}
	}
	if openRedirectCheck.Run(Point{}, Response{}, safe) != nil {
		t.Fatal("relative redirect must not flag")
	}
}

func TestPathTraversalDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "etc") {
			return Response{Status: 200, FlowID: 7, Body: "root:x:0:0:root:/root:/bin/bash\n"}
		}
		return Response{Status: 200, Body: "not found"}
	}
	if pathTraversalCheck.Run(Point{}, Response{Body: "ok"}, probe) == nil {
		t.Fatal("expected path-traversal hit on /etc/passwd contents")
	}
}

func TestCmdInjectionTimingDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "sleep 6") {
			return Response{Status: 200, FlowID: 8, Duration: 6 * time.Second}
		}
		return Response{Status: 200, Duration: 50 * time.Millisecond} // sleep 0 control is fast
	}
	if cmdInjectionCheck.Run(Point{Value: "h"}, Response{Status: 200, Duration: 40 * time.Millisecond}, probe) == nil {
		t.Fatal("expected timing cmd-injection hit")
	}
	// constant latency (no injection) → no hit
	flat := func(payload string) Response { return Response{Status: 200, Duration: 40 * time.Millisecond} }
	if cmdInjectionCheck.Run(Point{Value: "h"}, Response{Status: 200, Duration: 40 * time.Millisecond}, flat) != nil {
		t.Fatal("flat latency must not flag")
	}
}

// Length-based boolean SQLi must not flag on tiny responses, where a few bytes of
// natural variation reads as a large relative divergence (false positives).
func TestSQLiBooleanIgnoresTinyResponses(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "1'='1") {
			return Response{Status: 200, Body: "{}"} // true ≈ baseline (2 bytes)
		}
		return Response{Status: 200, Body: "404 not found — a longer error page"} // false diverges
	}
	if sqliBooleanCheck.Run(Point{Value: "1"}, Response{Status: 200, Body: "{}"}, probe) != nil {
		t.Fatal("tiny baseline must not flag boolean SQLi")
	}
}

// A slow sleep-6 must NOT be confirmed when the sleep-0 control never actually
// ran (Status 0 — e.g. the request budget was exhausted). A control that didn't
// execute can't rule out a genuinely-slow endpoint, so confirming on it is a
// false positive.
func TestCmdInjectionTimingRequiresControlToRun(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "sleep 6") {
			return Response{Status: 200, FlowID: 9, Duration: 6 * time.Second}
		}
		return Response{Status: 0} // control did not execute (budget exhausted / error)
	}
	if cmdInjectionCheck.Run(Point{Value: "h"}, Response{Status: 200, Duration: 40 * time.Millisecond}, probe) != nil {
		t.Fatal("must not confirm cmd-injection when the sleep-0 control did not run")
	}
}

// ---- engine wiring + budget ----

func TestRunFindsAndBoundsRequests(t *testing.T) {
	var sent int32
	send := func(tg Target) Response {
		n := atomic.AddInt32(&sent, 1)
		// a server that reflects the `q` query param unencoded (vulnerable to XSS)
		body := "<html>search: " + valueOfQuery(tg.URL, "q") + "</html>"
		return Response{Status: 200, FlowID: int64(n), Body: body}
	}
	findings, count := Run(context.Background(), Target{Method: "GET", URL: "http://victim/search?q=hi"}, send, Options{MaxRequests: 50, Concurrency: 4})

	var xss bool
	for _, f := range findings {
		if f.Class == "xss" && f.Point.Name == "q" {
			xss = true
		}
	}
	if !xss {
		t.Fatalf("expected an XSS finding on q; got %+v", findings)
	}
	if count > 50 {
		t.Fatalf("request budget exceeded: %d", count)
	}
}

func TestRunRespectsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	send := func(tg Target) Response { return Response{Status: 200} }
	_, count := Run(ctx, Target{URL: "http://h/p?a=1"}, send, Options{})
	if count > 1 { // only the baseline may have been sent
		t.Fatalf("cancelled run should issue ~no probes, issued %d", count)
	}
}

// valueOfQuery reads (and URL-decodes) a query param from a URL string.
func valueOfQuery(rawurl, key string) string {
	u, err := url.Parse(rawurl)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}
