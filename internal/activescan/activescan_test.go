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
	if countKind(q, "query") != 2 {
		t.Fatalf("expected 2 query points, got %+v", q)
	}
	mutated := (Target{URL: "http://h/p?a=1&b=2"}).With(Point{"query", "a", "1"}, "PWN")
	if !strings.Contains(mutated.URL, "a=PWN") || !strings.Contains(mutated.URL, "b=2") {
		t.Fatalf("query mutation wrong: %s", mutated.URL)
	}
	// form
	hdr := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	f := Points(Target{Method: "POST", URL: "http://h/p", Headers: hdr, Body: "x=1&y=2"})
	if countKind(f, "form") != 2 {
		t.Fatalf("expected 2 form points, got %+v", f)
	}
	// json
	jt := Target{Method: "POST", URL: "http://h/p", Headers: http.Header{"Content-Type": {"application/json"}}, Body: `{"u":"bob","n":5}`}
	jp := Points(jt)
	if countKind(jp, "json") != 2 {
		t.Fatalf("expected 2 json points, got %+v", jp)
	}
	jm := jt.With(Point{"json", "u", "bob"}, "PWN")
	if !strings.Contains(jm.Body, `"u":"PWN"`) {
		t.Fatalf("json mutation wrong: %s", jm.Body)
	}
}

// TestPathCookieHeaderPoints verifies the extended injection surface: path
// segments, cookie values, and the curated header set are all enumerated, and
// that mutation replaces exactly the targeted location without corrupting the
// shared baseline Target's headers.
func TestPathCookieHeaderPoints(t *testing.T) {
	hdr := http.Header{"Cookie": {"sid=abc; theme=dark"}, "User-Agent": {"curl/8"}}
	tg := Target{Method: "GET", URL: "http://h/api/users/42", Headers: hdr}
	pts := Points(tg)

	kinds := map[string]int{}
	var pathSeg, cookieName string
	for _, p := range pts {
		kinds[p.Kind]++
		if p.Kind == "path" && p.Value == "42" {
			pathSeg = p.Name
		}
		if p.Kind == "cookie" && p.Name == "sid" {
			cookieName = p.Name
		}
	}
	if kinds["path"] < 2 { // "api","users","42"
		t.Fatalf("expected path segment points, got %+v", pts)
	}
	if kinds["cookie"] != 2 {
		t.Fatalf("expected 2 cookie points, got %d (%+v)", kinds["cookie"], pts)
	}
	if kinds["header"] != len(injectableHeaders) {
		t.Fatalf("expected %d header points, got %d", len(injectableHeaders), kinds["header"])
	}
	if pathSeg == "" || cookieName == "" {
		t.Fatalf("did not find expected path/cookie points: %+v", pts)
	}

	// query/form/json/body points must sort before path/cookie/header (budget priority).
	mixed := Points(Target{Method: "GET", URL: "http://h/a/b?q=1", Headers: hdr})
	if mixed[0].Kind != "query" {
		t.Fatalf("query point should sort first, got %q first: %+v", mixed[0].Kind, mixed)
	}

	// path mutation replaces the right segment, keeping traversal sequences literal.
	pm := tg.With(Point{"path", pathSeg, "42"}, "../../../etc/passwd")
	if !strings.Contains(pm.URL, "../../../etc/passwd") || strings.Contains(pm.URL, "/42") {
		t.Fatalf("path mutation wrong: %s", pm.URL)
	}

	// cookie mutation replaces one value, preserves the other, and does NOT mutate
	// the baseline Target's header map.
	cm := tg.With(Point{"cookie", "sid", "abc"}, "PWN")
	if got := cm.Headers.Get("Cookie"); !strings.Contains(got, "sid=PWN") || !strings.Contains(got, "theme=dark") {
		t.Fatalf("cookie mutation wrong: %s", got)
	}
	if tg.Headers.Get("Cookie") != "sid=abc; theme=dark" {
		t.Fatalf("baseline Cookie header was mutated: %s", tg.Headers.Get("Cookie"))
	}

	// header mutation sets the header on a cloned map, leaving baseline intact.
	hm := tg.With(Point{"header", "User-Agent", "curl/8"}, "PWN")
	if hm.Headers.Get("User-Agent") != "PWN" {
		t.Fatalf("header mutation wrong: %s", hm.Headers.Get("User-Agent"))
	}
	if tg.Headers.Get("User-Agent") != "curl/8" {
		t.Fatalf("baseline User-Agent header was mutated: %s", tg.Headers.Get("User-Agent"))
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

func TestSQLiTimeDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "SLEEP(6)") || strings.Contains(payload, "pg_sleep(6)") || strings.Contains(payload, "0:0:6") {
			return Response{Status: 200, FlowID: 30, Duration: 6 * time.Second}
		}
		return Response{Status: 200, Duration: 40 * time.Millisecond} // 0-delay control is fast
	}
	if sqliTimeCheck.Run(Point{Value: "1"}, Response{Status: 200, Duration: 30 * time.Millisecond}, probe) == nil {
		t.Fatal("expected time-based SQLi hit")
	}
	// flat latency → no hit
	flat := func(payload string) Response { return Response{Status: 200, Duration: 30 * time.Millisecond} }
	if sqliTimeCheck.Run(Point{Value: "1"}, Response{Status: 200, Duration: 30 * time.Millisecond}, flat) != nil {
		t.Fatal("flat latency must not flag time-based SQLi")
	}
	// slow-but-uncontrolled (control never ran) → no hit
	uncontrolled := func(payload string) Response {
		if strings.Contains(payload, "SLEEP(6)") {
			return Response{Status: 200, FlowID: 31, Duration: 6 * time.Second}
		}
		return Response{Status: 0} // control did not execute
	}
	if sqliTimeCheck.Run(Point{Value: "1"}, Response{Status: 200, Duration: 30 * time.Millisecond}, uncontrolled) != nil {
		t.Fatal("must not confirm time-based SQLi when the 0-delay control did not run")
	}
}

func TestNoSQLDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.ContainsAny(payload, `'"\`) {
			return Response{Status: 500, FlowID: 32, Body: `MongoServerError: unexpected token in JSON at position 5`}
		}
		return Response{Status: 200, Body: "ok"}
	}
	if nosqlCheck.Run(Point{Value: "1"}, Response{Status: 200, Body: "ok"}, probe) == nil {
		t.Fatal("expected NoSQL error-based hit")
	}
	// baseline already errors → cannot attribute → no hit
	base := Response{Status: 500, Body: "MongoServerError: boom"}
	if nosqlCheck.Run(Point{Value: "1"}, base, probe) != nil {
		t.Fatal("must not flag NoSQL when baseline already errors")
	}
}

func TestLDAPDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, ")(") {
			return Response{Status: 500, FlowID: 33, Body: "javax.naming.directory.InvalidSearchFilterException: Bad search filter"}
		}
		return Response{Status: 200, Body: "ok"}
	}
	if ldapCheck.Run(Point{Value: "admin"}, Response{Status: 200, Body: "ok"}, probe) == nil {
		t.Fatal("expected LDAP error-based hit")
	}
}

func TestXPathDetector(t *testing.T) {
	probe := func(payload string) Response {
		if strings.Contains(payload, "'") {
			return Response{Status: 500, FlowID: 34, Body: "Error: Expression must evaluate to a node-set."}
		}
		return Response{Status: 200, Body: "ok"}
	}
	if xpathCheck.Run(Point{Value: "1"}, Response{Status: 200, Body: "ok"}, probe) == nil {
		t.Fatal("expected XPath error-based hit")
	}
}

func TestHostHeaderDetector(t *testing.T) {
	// Reflected into an absolute URL in the body → hit.
	body := func(payload string) Response {
		if payload == hostHeaderCanary {
			return Response{Status: 200, FlowID: 35, Body: `<a href="https://` + hostHeaderCanary + `/reset">reset</a>`}
		}
		return Response{Status: 200, Body: "ok"}
	}
	if hostHeaderCheck.Run(Point{Kind: "header", Name: "X-Forwarded-Host"}, Response{Status: 200, Body: "ok"}, body) == nil {
		t.Fatal("expected host-header hit when canary lands in an absolute URL")
	}
	// Reflected only as plain text (not a URL) → no hit (strict).
	plain := func(payload string) Response {
		return Response{Status: 200, FlowID: 36, Body: "Welcome, host " + hostHeaderCanary}
	}
	if hostHeaderCheck.Run(Point{Kind: "header", Name: "X-Forwarded-Host"}, Response{Status: 200, Body: "ok"}, plain) != nil {
		t.Fatal("plain-text reflection (no URL context) must not flag host-header injection")
	}
	// Wrong point (query) → skipped.
	if hostHeaderCheck.Run(Point{Kind: "query", Name: "q"}, Response{Status: 200}, body) != nil {
		t.Fatal("host-header check must only fire on the X-Forwarded-Host header point")
	}
}

func TestCORSReflectionDetector(t *testing.T) {
	// Reflected Origin with credentials → High.
	creds := func(payload string) Response {
		return Response{Status: 200, FlowID: 37, Headers: http.Header{
			"Access-Control-Allow-Origin":      {corsCanaryOrigin},
			"Access-Control-Allow-Credentials": {"true"},
		}}
	}
	h := corsReflectionCheck.Run(Point{Kind: "header", Name: "Origin"}, Response{Status: 200}, creds)
	if h == nil || h.Severity != "High" {
		t.Fatalf("expected High CORS-with-credentials hit, got %+v", h)
	}
	// Reflected without credentials → Medium (base severity, no override).
	noCreds := func(payload string) Response {
		return Response{Status: 200, FlowID: 38, Headers: http.Header{"Access-Control-Allow-Origin": {corsCanaryOrigin}}}
	}
	if corsReflectionCheck.Run(Point{Kind: "header", Name: "Origin"}, Response{Status: 200}, noCreds) == nil {
		t.Fatal("expected CORS reflection hit without credentials")
	}
	// Not reflected → no hit.
	safe := func(payload string) Response {
		return Response{Status: 200, Headers: http.Header{"Access-Control-Allow-Origin": {"https://trusted.example"}}}
	}
	if corsReflectionCheck.Run(Point{Kind: "header", Name: "Origin"}, Response{Status: 200}, safe) != nil {
		t.Fatal("a server that does not reflect the attacker Origin must not flag")
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

// TestRunHonorsDisabledChecks confirms a check toggled off in the Checks manager
// is skipped by the engine (no probes sent, no finding) — same toggle surface as
// passive checks.
func TestRunHonorsDisabledChecks(t *testing.T) {
	send := func(tg Target) Response {
		// Server reflects q unencoded — would be XSS-vulnerable if the check ran.
		return Response{Status: 200, FlowID: 1, Body: "<html>search: " + valueOfQuery(tg.URL, "q") + "</html>"}
	}
	// Baseline: XSS fires when enabled.
	on, _ := Run(context.Background(), Target{Method: "GET", URL: "http://victim/search?q=hi"}, send, Options{MaxRequests: 50})
	var xss bool
	for _, f := range on {
		if f.Class == "xss" {
			xss = true
		}
	}
	if !xss {
		t.Fatalf("XSS should fire when enabled; got %+v", on)
	}
	// With active-xss disabled, no XSS finding.
	off, _ := Run(context.Background(), Target{Method: "GET", URL: "http://victim/search?q=hi"}, send, Options{MaxRequests: 50, Disabled: map[string]bool{"active-xss": true}})
	for _, f := range off {
		if f.Class == "xss" {
			t.Fatalf("disabled active-xss must not fire; got %+v", off)
		}
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

// countKind returns how many points in pts have the given kind.
func countKind(pts []Point, kind string) int {
	n := 0
	for _, p := range pts {
		if p.Kind == kind {
			n++
		}
	}
	return n
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

// ---- CRLF injection / HTTP response splitting ----

// TestCRLFDetector_HeaderSplitting verifies that when a server naively echoes a
// query parameter into a response header (the classic response-splitting scenario)
// the check raises a High finding.  The probe simulates a vulnerable endpoint by
// URL-decoding the payload and using net/http's Header.Set so that if a
// CR+LF sequence is present the extra header is actually added.
func TestCRLFDetector_HeaderSplitting(t *testing.T) {
	// Simulate a server that naively takes the payload value, url-decodes it, and
	// sets it verbatim as (part of) a response header value.  A real vulnerable
	// server would split on \r\n, producing additional headers.
	vulnerable := func(payload string) Response {
		// URL-decode the payload (one layer) to simulate what the server does.
		decoded, err := url.QueryUnescape(payload)
		if err != nil {
			decoded = payload
		}
		hdrs := http.Header{}
		// If the decoded payload contains \r\n followed by a header-like line,
		// the server would emit that extra header.  Simulate by scanning for \r\n.
		if idx := strings.Index(decoded, "\r\n"); idx >= 0 {
			extra := decoded[idx+2:] // everything after the first CRLF
			// extra is in the form "HeaderName: value" — parse and add it.
			if colon := strings.Index(extra, ":"); colon > 0 {
				name := strings.TrimSpace(extra[:colon])
				value := strings.TrimSpace(extra[colon+1:])
				hdrs.Set(name, value)
			}
		}
		return Response{Status: 200, FlowID: 20, Headers: hdrs}
	}
	if crlfCheck.Run(Point{Kind: "query", Name: "redirect", Value: "home"}, Response{Status: 200, Headers: http.Header{}}, vulnerable) == nil {
		t.Fatal("expected CRLF hit: server echoes param into header and CR+LF causes header injection")
	}
}

// TestCRLFDetector_Stripped verifies no false positive when the server strips
// CR/LF characters before using the parameter in a response header.
func TestCRLFDetector_Stripped(t *testing.T) {
	safe := func(payload string) Response {
		// Safe server: strip \r and \n before using the value.
		cleaned := strings.NewReplacer("\r", "", "\n", "").Replace(payload)
		_ = cleaned // header value is used but canary header is never set
		return Response{Status: 200, FlowID: 21, Headers: http.Header{"X-Safe": {"ok"}}}
	}
	if crlfCheck.Run(Point{Kind: "query", Name: "redirect", Value: "home"}, Response{Status: 200, Headers: http.Header{}}, safe) != nil {
		t.Fatal("safe server (strips CR/LF) must not produce a CRLF finding")
	}
}

// TestCRLFDetector_NoReflection verifies no false positive when the parameter is
// not reflected into response headers at all (the common, non-vulnerable case).
func TestCRLFDetector_NoReflection(t *testing.T) {
	noReflect := func(payload string) Response {
		// The response headers bear no relation to the input.
		return Response{Status: 200, FlowID: 22, Headers: http.Header{"Content-Type": {"text/html"}}}
	}
	if crlfCheck.Run(Point{Kind: "query", Name: "q", Value: "test"}, Response{Status: 200, Headers: http.Header{}}, noReflect) != nil {
		t.Fatal("non-reflective endpoint must not produce a CRLF finding")
	}
}

// TestCRLFDetector_BaselineGuard verifies that an endpoint already emitting the
// canary header in its baseline response is not mis-flagged.
func TestCRLFDetector_BaselineGuard(t *testing.T) {
	// Every probe response also contains the canary (it was already there).
	alwaysCanary := func(payload string) Response {
		return Response{Status: 200, FlowID: 23, Headers: http.Header{crlfHeaderName: {"canary"}}}
	}
	baseWithCanary := Response{Status: 200, Headers: http.Header{crlfHeaderName: {"canary"}}}
	if crlfCheck.Run(Point{Kind: "query", Name: "q", Value: "x"}, baseWithCanary, alwaysCanary) != nil {
		t.Fatal("must not flag CRLF when canary header already present in baseline")
	}
}

// TestCRLFDetector_SetCookieInjection verifies that injecting a Set-Cookie
// header via response splitting is also detected (secondary signal).
func TestCRLFDetector_SetCookieInjection(t *testing.T) {
	// Simulate a server that produces a Set-Cookie containing our canary name
	// (some vulnerable servers split specifically on Set-Cookie lines).
	setCookieVuln := func(payload string) Response {
		decoded, _ := url.QueryUnescape(payload)
		hdrs := http.Header{}
		if strings.Contains(decoded, "\r\n") {
			// The secondary signal: Set-Cookie contains the canary name.
			hdrs.Set("Set-Cookie", crlfHeaderName+"=canary; Path=/")
		}
		return Response{Status: 200, FlowID: 24, Headers: hdrs}
	}
	if crlfCheck.Run(Point{Kind: "query", Name: "redirect", Value: "home"}, Response{Status: 200, Headers: http.Header{}}, setCookieVuln) == nil {
		t.Fatal("expected CRLF hit via Set-Cookie secondary signal")
	}
}
