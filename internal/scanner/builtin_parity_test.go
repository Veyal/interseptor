package scanner

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/checkscript"
	"github.com/Veyal/interseptor/internal/store"
)

// runTemplate compiles a built-in's Starlark template and runs it over a flow,
// the way the scanner does when the user saves the template as an override.
func runTemplate(t *testing.T, id string, f *store.Flow, reqBody, resBody string) []store.Issue {
	t.Helper()
	src, ok := BuiltinTemplate(id)
	if !ok {
		t.Fatalf("no template for %q", id)
	}
	c, logs, err := checkscript.CompileWithLogs(id, src)
	if err != nil {
		t.Fatalf("compile %q: %v (printed %q)", id, err, logs)
	}
	issues, runLogs, err := c.RunWithLogs(checkscript.Flow{
		ID: f.ID, Method: f.Method, Scheme: f.Scheme, Host: f.Host, Port: f.Port,
		Path: f.Path, Status: f.Status, Mime: f.Mime,
		ReqHeaders: f.ReqHeaders, ResHeaders: f.ResHeaders,
		ReqBody: reqBody, ResBody: resBody,
	})
	if err != nil {
		t.Fatalf("run %q: %v (printed %q)", id, err, runLogs)
	}
	return issues
}

// Saving a built-in's template disables the compiled Go check, so a template
// that returns [] silently turns the check off. None may be a stub.
func TestNoBuiltinTemplateIsAnEmptyStub(t *testing.T) {
	for _, b := range BuiltinChecks {
		src, ok := BuiltinTemplate(b.ID)
		if !ok {
			t.Fatalf("missing template for %q", b.ID)
		}
		var body []string
		for _, line := range strings.Split(src, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			body = append(body, line)
		}
		if len(body) == 2 && body[0] == "def check(flow):" && body[1] == "return []" {
			t.Errorf("the %q template is a stub that returns []; saving it would disable the check", b.ID)
		}
		if strings.Contains(src, "customize") {
			t.Errorf("the %q template still advertises itself as a placeholder to customize", b.ID)
		}
	}
}

// The reflected-param template must find what the Go check finds.
func TestReflectedParamTemplateMatchesTheGoCheck(t *testing.T) {
	cases := []struct {
		name          string
		flow          *store.Flow
		reqBody, resp string
		want          bool
	}{
		{
			name: "query parameter echoed into HTML",
			flow: &store.Flow{
				ID: 1, Method: "GET", Scheme: "https", Host: "example.com",
				Path: "/search?q=needle-value", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
			},
			resp: "<html><body>You searched for needle-value</body></html>",
			want: true,
		},
		{
			name: "url-encoded query parameter echoed decoded",
			flow: &store.Flow{
				ID: 2, Method: "GET", Scheme: "https", Host: "example.com",
				Path: "/search?q=hello%20needle", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
			},
			resp: "<html>hello needle</html>",
			want: true,
		},
		{
			name: "form body parameter echoed into HTML",
			flow: &store.Flow{
				ID: 3, Method: "POST", Scheme: "https", Host: "example.com",
				Path: "/comment", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
			},
			reqBody: "comment=reflected-text&other=1",
			resp:    "<html><p>reflected-text</p></html>",
			want:    true,
		},
		{
			name: "short value is not a candidate",
			flow: &store.Flow{
				ID: 4, Method: "GET", Scheme: "https", Host: "example.com",
				Path: "/search?q=abc", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
			},
			resp: "<html>abc</html>",
			want: false,
		},
		{
			name: "not reflected",
			flow: &store.Flow{
				ID: 5, Method: "GET", Scheme: "https", Host: "example.com",
				Path: "/search?q=needle-value", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
			},
			resp: "<html>nothing here</html>",
			want: false,
		},
		{
			name: "JSON response is not an HTML sink",
			flow: &store.Flow{
				ID: 6, Method: "GET", Scheme: "https", Host: "example.com",
				Path: "/api?q=needle-value", Status: 200, Mime: "application/json",
				ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
			},
			resp: `{"q":"needle-value"}`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			star := hasTitle(runTemplate(t, checkReflectedParam, tc.flow, tc.reqBody, tc.resp),
				"Request parameter reflected in HTML response")
			goSide := hasTitle(onlyCheck(tc.flow, []byte(tc.reqBody), []byte(tc.resp), checkReflectedParam),
				"Request parameter reflected in HTML response")
			if star != tc.want {
				t.Errorf("Starlark template found = %v, want %v", star, tc.want)
			}
			if goSide != tc.want {
				t.Errorf("Go check found = %v, want %v", goSide, tc.want)
			}
		})
	}
}

// The open-redirect template must find what the Go check finds.
func TestOpenRedirectTemplateMatchesTheGoCheck(t *testing.T) {
	redirect := func(id int64, path, location string) *store.Flow {
		return &store.Flow{
			ID: id, Method: "GET", Scheme: "https", Host: "example.com", Path: path,
			Status:     302,
			ResHeaders: map[string][]string{"Location": {location}},
		}
	}
	cases := []struct {
		name    string
		flow    *store.Flow
		reqBody string
		want    bool
	}{
		{
			name: "next parameter sends the browser off-host",
			flow: redirect(1, "/login?next=https://attacker.example/landing", "https://attacker.example/landing"),
			want: true,
		},
		{
			name: "redirect parameter in the form body",
			flow: func() *store.Flow {
				f := redirect(2, "/login", "https://attacker.example/x")
				f.Method = "POST"
				return f
			}(),
			reqBody: "user=a&redirect=https://attacker.example/x",
			want:    true,
		},
		{
			name: "on-host redirect is fine",
			flow: redirect(3, "/login?next=https://example.com/dashboard", "https://example.com/dashboard"),
			want: false,
		},
		{
			name: "relative Location is fine",
			flow: redirect(4, "/login?next=/dashboard-page", "/dashboard-page"),
			want: false,
		},
		{
			name: "Location not influenced by any parameter",
			flow: redirect(5, "/login?user=someone", "https://other.example/fixed"),
			want: false,
		},
		{
			name: "a 200 is not a redirect",
			flow: func() *store.Flow {
				f := redirect(6, "/login?next=https://attacker.example/x", "https://attacker.example/x")
				f.Status = 200
				return f
			}(),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			star := hasTitle(runTemplate(t, checkOpenRedirect, tc.flow, tc.reqBody, ""),
				"Potential open redirect via request parameter")
			goSide := hasTitle(onlyCheck(tc.flow, []byte(tc.reqBody), nil, checkOpenRedirect),
				"Potential open redirect via request parameter")
			if star != tc.want {
				t.Errorf("Starlark template found = %v, want %v", star, tc.want)
			}
			if goSide != tc.want {
				t.Errorf("Go check found = %v, want %v", goSide, tc.want)
			}
		})
	}
}

// Both templates must be able to see every parameter: without query_params a
// check can only look up names it already knows, which is why these two were
// stubs.
func TestTemplatesUseTheParameterEnumerationAttribute(t *testing.T) {
	for _, id := range []string{checkReflectedParam, checkOpenRedirect} {
		src, _ := BuiltinTemplate(id)
		if !strings.Contains(src, "flow.query_params") {
			t.Errorf("the %q template does not enumerate flow.query_params", id)
		}
	}
}

// onlyCheck runs the built-in Go checks with everything but id disabled.
func onlyCheck(f *store.Flow, reqBody, resBody []byte, id string) []store.Issue {
	disabled := make(map[string]bool, len(BuiltinChecks))
	for _, b := range BuiltinChecks {
		if b.ID != id {
			disabled[b.ID] = true
		}
	}
	return AnalyzeWithDisabled(f, reqBody, resBody, disabled)
}

func hasTitle(issues []store.Issue, title string) bool {
	for _, is := range issues {
		if is.Title == title {
			return true
		}
	}
	return false
}
