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

// ---- XXE check ----

// TestXXEDetector_Resolves verifies that a server that resolves the internal
// entity (i.e. echoes xxeCanary in the response) is flagged.
func TestXXEDetector_Resolves(t *testing.T) {
	// A probe that returns the canary when it sees the injected DOCTYPE.
	probe := func(payload string) Response {
		if strings.Contains(payload, xxeCanary) {
			// Simulate a parser that expanded the entity and echoed the result.
			return Response{Status: 200, FlowID: 10, Body: "<result>" + xxeCanary + "</result>"}
		}
		return Response{Status: 200, Body: "<result>hello</result>"}
	}
	p := Point{Kind: "body", Name: "_xml", Value: `<?xml version="1.0"?><root><name>test</name></root>`}
	if xxeCheck.Run(p, Response{Status: 200, Body: "<result>hello</result>"}, probe) == nil {
		t.Fatal("expected XXE hit when server reflects the entity canary")
	}
}

// TestXXEDetector_NoResolve confirms no false positive when the server echoes
// the injected body verbatim (including &xxe; as a literal reference, unresolved).
func TestXXEDetector_NoResolve(t *testing.T) {
	// A probe that echoes back whatever XML it received, but the entity is NOT
	// expanded — the response does not contain xxeCanary.
	probe := func(payload string) Response {
		// The raw '&xxe;' entity reference appears in the body, but the canary
		// string itself does NOT (the parser returned an error or left it as-is).
		return Response{Status: 200, Body: "<result>&amp;xxe;</result>"}
	}
	p := Point{Kind: "body", Name: "_xml", Value: `<?xml version="1.0"?><root><name>test</name></root>`}
	if xxeCheck.Run(p, Response{Status: 200, Body: "<result>hello</result>"}, probe) != nil {
		t.Fatal("unresolved entity must not be flagged as XXE")
	}
}

// TestXXEDetector_NonXMLPoint confirms the check is skipped for non-body points,
// so it does not fire on query/form/json parameters.
func TestXXEDetector_NonXMLPoint(t *testing.T) {
	probe := func(payload string) Response {
		return Response{Status: 200, Body: xxeCanary} // would flag if check ran
	}
	// query point — must be ignored
	if xxeCheck.Run(Point{Kind: "query", Name: "q", Value: "x"}, Response{}, probe) != nil {
		t.Fatal("XXE check must be skipped for non-body (query) points")
	}
	// json point — must be ignored
	if xxeCheck.Run(Point{Kind: "json", Name: "field", Value: "x"}, Response{}, probe) != nil {
		t.Fatal("XXE check must be skipped for non-body (json) points")
	}
}

// TestXXEDetector_CanaryInBaseline confirms the check is skipped when the canary
// string is already present in the baseline response (false-positive guard).
func TestXXEDetector_CanaryInBaseline(t *testing.T) {
	probe := func(payload string) Response {
		return Response{Status: 200, FlowID: 11, Body: "<result>" + xxeCanary + "</result>"}
	}
	baseWithCanary := Response{Status: 200, Body: "<result>" + xxeCanary + "</result>"}
	p := Point{Kind: "body", Name: "_xml", Value: `<?xml version="1.0"?><root><name>test</name></root>`}
	if xxeCheck.Run(p, baseWithCanary, probe) != nil {
		t.Fatal("must not flag XXE when canary already present in baseline response")
	}
}

// TestPoints_XMLBodyProducesBodyPoint verifies that Points() emits a "body"/"_xml"
// point for XML-content-typed requests.
func TestPoints_XMLBodyProducesBodyPoint(t *testing.T) {
	xmlBody := `<?xml version="1.0"?><root><id>1</id></root>`

	// application/xml content-type
	pts := Points(Target{
		Method:  "POST",
		URL:     "http://h/api",
		Headers: http.Header{"Content-Type": {"application/xml"}},
		Body:    xmlBody,
	})
	if !hasBodyPoint(pts) {
		t.Fatalf("expected a body/_xml point for application/xml, got %+v", pts)
	}

	// text/xml content-type
	pts = Points(Target{
		Method:  "POST",
		URL:     "http://h/api",
		Headers: http.Header{"Content-Type": {"text/xml; charset=utf-8"}},
		Body:    xmlBody,
	})
	if !hasBodyPoint(pts) {
		t.Fatalf("expected a body/_xml point for text/xml, got %+v", pts)
	}

	// no content-type but body starts with <?xml prolog
	pts = Points(Target{
		Method: "POST",
		URL:    "http://h/api",
		Body:   xmlBody, // starts with <?xml
	})
	if !hasBodyPoint(pts) {
		t.Fatalf("expected a body/_xml point for body starting with <?xml, got %+v", pts)
	}

	// application/xml with +xml suffix
	pts = Points(Target{
		Method:  "POST",
		URL:     "http://h/api",
		Headers: http.Header{"Content-Type": {"application/atom+xml"}},
		Body:    `<feed xmlns="http://www.w3.org/2005/Atom"><entry/></feed>`,
	})
	if !hasBodyPoint(pts) {
		t.Fatalf("expected a body/_xml point for application/atom+xml, got %+v", pts)
	}
}

// TestPoints_NonXMLBodyNoBodyPoint confirms that non-XML bodies do NOT produce a
// body point (so the XXE check doesn't run on JSON or form requests).
func TestPoints_NonXMLBodyNoBodyPoint(t *testing.T) {
	jsonPts := Points(Target{
		Method:  "POST",
		URL:     "http://h/api",
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    `{"id":1}`,
	})
	if hasBodyPoint(jsonPts) {
		t.Fatal("JSON body must not produce a body/_xml point")
	}

	formPts := Points(Target{
		Method: "POST",
		URL:    "http://h/api",
		Body:   "id=1&name=foo",
	})
	if hasBodyPoint(formPts) {
		t.Fatal("form body must not produce a body/_xml point")
	}
}

// TestXXEInjectDoctype checks that xxeInjectDoctype produces a valid DOCTYPE
// injection and that xmlFirstElement can locate the root tag.
func TestXXEInjectDoctype(t *testing.T) {
	cases := []struct {
		body     string
		wantRoot string
	}{
		{`<?xml version="1.0"?><root><x>1</x></root>`, "root"},
		{`<data><item>hello</item></data>`, "data"},
		{`<?xml version="1.0" encoding="UTF-8"?><soap:Envelope xmlns:soap="..."><soap:Body/></soap:Envelope>`, "soap:Envelope"},
	}
	for _, tc := range cases {
		got := xmlFirstElement(tc.body)
		if got != tc.wantRoot {
			t.Errorf("xmlFirstElement(%q): want %q, got %q", tc.body, tc.wantRoot, got)
		}
		injected := xxeInjectDoctype(tc.body)
		if injected == "" {
			t.Errorf("xxeInjectDoctype(%q): got empty string", tc.body)
		}
		if !strings.Contains(injected, xxeCanary) {
			t.Errorf("xxeInjectDoctype output missing canary:\n%s", injected)
		}
		if !strings.Contains(injected, "<!DOCTYPE") {
			t.Errorf("xxeInjectDoctype output missing DOCTYPE:\n%s", injected)
		}
	}
}

// TestXXEStripDoctype verifies that an existing DOCTYPE is removed before
// injection to avoid duplicate DOCTYPE declarations.
func TestXXEStripDoctype(t *testing.T) {
	body := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY bar "baz">]><root/>`
	stripped := xmlStripDoctype(body)
	if strings.Contains(strings.ToLower(stripped), "<!doctype") {
		t.Fatalf("xmlStripDoctype should have removed existing DOCTYPE:\n%s", stripped)
	}
	injected := xxeInjectDoctype(body)
	count := strings.Count(strings.ToLower(injected), "<!doctype")
	if count != 1 {
		t.Fatalf("expected exactly 1 DOCTYPE after injection, found %d:\n%s", count, injected)
	}
}

// hasBodyPoint returns true if any point in pts is a body/_xml point.
func hasBodyPoint(pts []Point) bool {
	for _, p := range pts {
		if p.Kind == "body" && p.Name == "_xml" {
			return true
		}
	}
	return false
}
