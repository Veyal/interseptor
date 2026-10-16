package pmsandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// corpusCase is one synthetic Postman script idiom (example.com only) with
// the outcome the sandbox must produce.
type corpusCase struct {
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Script  string `json:"script"`
	Request *struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"request"`
	Response *struct {
		Code    int                `json:"code"`
		Body    string             `json:"body"`
		Headers map[string]string  `json:"headers"`
		Cookies []scriptctx.Cookie `json:"cookies"`
	} `json:"response"`
	Vars    scriptctx.Vars     `json:"vars"`
	Secret  []string           `json:"secret"`
	Cookies []scriptctx.Cookie `json:"cookies"`
	Info    scriptctx.Info     `json:"info"`
	Sends   map[string]struct {
		Code    int               `json:"code"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
	} `json:"sends"`
	Expect struct {
		Status      string            `json:"status"`
		Tests       [][2]string       `json:"tests"`
		Messages    map[string]string `json:"messages"`
		Env         map[string]any    `json:"env"`
		Globals     map[string]any    `json:"globals"`
		Collection  map[string]any    `json:"collection"`
		Console     []string          `json:"console"`
		Next        string            `json:"next"`
		NextStop    bool              `json:"nextStop"`
		Skip        bool              `json:"skip"`
		Sent        []string          `json:"sent"`
		Unsupported []string          `json:"unsupported"`
		CookieOps   []string          `json:"cookieOps"`
		NoLeak      []string          `json:"noLeak"`
		Request     *struct {
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Headers   map[string]string `json:"headers"`
			Body      string            `json:"body"`
			NoHeaders []string          `json:"noHeaders"`
		} `json:"request"`
	} `json:"expect"`
}

func hdrs(m map[string]string) []scriptctx.Header {
	var out []scriptctx.Header
	for k, v := range m {
		out = append(out, scriptctx.Header{Key: k, Value: v})
	}
	return out
}

func (c corpusCase) input(sent *[]string) Input {
	in := base(scriptctx.Phase(c.Phase), c.Script)
	in.Request = nil
	if c.Request != nil {
		in.Request = &scriptctx.Request{Name: "Login", Method: c.Request.Method, URL: c.Request.URL, Headers: hdrs(c.Request.Headers)}
		if c.Request.Body != "" {
			in.Request.Body = scriptctx.Body{Mode: "raw", Raw: c.Request.Body}
		}
	}
	if c.Response != nil {
		in.Response = &scriptctx.Response{Code: c.Response.Code, Status: map[int]string{200: "OK", 201: "Created"}[c.Response.Code],
			Headers: hdrs(c.Response.Headers), Body: []byte(c.Response.Body), ResponseTime: 42 * time.Millisecond, Cookies: c.Response.Cookies}
		if in.Request == nil {
			in.Request = &scriptctx.Request{Name: "Login", Method: "POST", URL: "https://api.example.com/v1/login"}
		}
	}
	in.Vars = c.Vars
	in.Vars.Secret = c.Secret
	in.Cookies = c.Cookies
	in.Info = c.Info
	if in.Info.RequestName == "" {
		in.Info.RequestName = "Login"
	}
	in.ScopeCheck = func(u string) error {
		if strings.Contains(u, "example.com") && !strings.Contains(u, "evil.example.org") {
			return nil
		}
		return fmt.Errorf("host not in scope")
	}
	in.Sender = scriptctx.SendFunc(func(_ context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
		*sent = append(*sent, r.URL)
		s, ok := c.Sends[r.URL]
		if !ok {
			return scriptctx.SendResponse{}, fmt.Errorf("no mock for %s", r.URL)
		}
		return scriptctx.SendResponse{Code: s.Code, Status: "OK", Body: []byte(s.Body), Headers: hdrs(s.Headers)}, nil
	})
	return in
}

func eqJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (c corpusCase) check(out Output, sent []string) []string {
	var bad []string
	f := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }
	e := c.Expect
	if out.Status != e.Status {
		f("status %s want %s (errors=%+v tests=%+v)", out.Status, e.Status, out.Errors, out.Tests)
	}
	for _, w := range e.Tests {
		found := false
		for _, t := range out.Tests {
			if t.Name == w[0] {
				found = true
				if t.Status != w[1] {
					f("test %q status %s want %s (%s)", w[0], t.Status, w[1], t.Message)
				}
			}
		}
		if !found {
			f("test %q missing", w[0])
		}
	}
	for name, msg := range e.Messages {
		for _, t := range out.Tests {
			if t.Name == name && t.Message != msg {
				f("test %q message %q want %q", name, t.Message, msg)
			}
		}
	}
	for _, p := range []struct {
		n    string
		want map[string]any
		got  map[string]any
	}{{"env", e.Env, out.Vars.Environment}, {"globals", e.Globals, out.Vars.Globals}, {"collection", e.Collection, out.Vars.Collection}} {
		for k, v := range p.want {
			if !eqJSON(p.got[k], v) {
				f("%s[%s]=%v want %v", p.n, k, p.got[k], v)
			}
		}
	}
	if e.Console != nil {
		var got []string
		for _, l := range out.Console {
			got = append(got, l.Level+":"+l.Text)
		}
		if !eqJSON(got, e.Console) {
			f("console %v want %v", got, e.Console)
		}
	}
	if e.Next != "" && (!out.Flow.NextSet || out.Flow.Next != e.Next) {
		f("flow %+v want next %q", out.Flow, e.Next)
	}
	if e.NextStop && !(out.Flow.NextSet && out.Flow.Next == "") {
		f("flow %+v want stop", out.Flow)
	}
	if e.Skip != out.Flow.Skip {
		f("skip=%v want %v", out.Flow.Skip, e.Skip)
	}
	if e.Sent != nil && !eqJSON(sent, e.Sent) {
		f("sent %v want %v", sent, e.Sent)
	}
	for _, u := range e.Unsupported {
		if !contains(out.Unsupported, u) {
			f("unsupported %v missing %q", out.Unsupported, u)
		}
	}
	for _, w := range e.CookieOps {
		ok := false
		for _, op := range out.CookieOps {
			ok = ok || op.Op+":"+op.Cookie.Name == w
		}
		if !ok {
			f("cookie op %s missing in %+v", w, out.CookieOps)
		}
	}
	if len(e.NoLeak) > 0 {
		b, _ := json.Marshal([]any{out.Console, out.Tests, out.Errors})
		for _, s := range e.NoLeak {
			if strings.Contains(string(b), s) {
				f("secret %q leaked in human-facing output", s)
			}
		}
	}
	if r := e.Request; r != nil {
		if out.Request == nil {
			f("no request output")
		} else {
			if r.Method != "" && out.Request.Method != r.Method {
				f("method %s want %s", out.Request.Method, r.Method)
			}
			if r.URL != "" && out.Request.URL != r.URL {
				f("url %s want %s", out.Request.URL, r.URL)
			}
			if r.Body != "" && out.Request.Body.Raw != r.Body {
				f("body %q want %q", out.Request.Body.Raw, r.Body)
			}
			for k, v := range r.Headers {
				got, ok := "", false
				for _, h := range out.Request.Headers {
					if strings.EqualFold(h.Key, k) {
						got, ok = h.Value, true
					}
				}
				if !ok || got != v {
					f("header %s=%q want %q", k, got, v)
				}
			}
			for _, k := range r.NoHeaders {
				for _, h := range out.Request.Headers {
					if strings.EqualFold(h.Key, k) {
						f("header %s should be removed", k)
					}
				}
			}
		}
	}
	return bad
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	b, err := os.ReadFile("testdata/pm_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

// TestPMCorpus is the acceptance gate: at least 90% of the idiom corpus must
// behave as specified. Every failing case is reported individually.
func TestPMCorpus(t *testing.T) {
	cs := loadCorpus(t)
	pass := 0
	for _, c := range cs {
		var sent []string
		out := Run(context.Background(), c.input(&sent))
		if bad := c.check(out, sent); len(bad) > 0 {
			t.Errorf("CASE %q:\n  %s", c.Name, strings.Join(bad, "\n  "))
			continue
		}
		pass++
	}
	rate := float64(pass) / float64(len(cs))
	t.Logf("corpus: %d/%d (%.0f%%)", pass, len(cs), rate*100)
	if rate < 0.90 {
		t.Fatalf("corpus pass rate %.0f%% < 90%%", rate*100)
	}
}
