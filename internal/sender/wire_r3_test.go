package sender

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// The stored flow is evidence of what the target received. The transport, not
// the template, decides the framing header, so the flow must carry the
// Content-Length that was written (HTTP/1.1 header, HTTP/2 content-length
// field) and nothing the transport did not send.
func TestR3FramingHeadersAreRecordedAsSent(t *testing.T) {
	cases := []struct {
		name    string
		h2      bool
		method  string
		body    string
		headers map[string][]string
	}{
		{"h1 body", false, "POST", "hello", nil},
		{"h1 empty POST", false, "POST", "", nil},
		{"h1 GET without body", false, "GET", "", nil},
		{"h1 template Content-Length is wrong", false, "POST", "hello", map[string][]string{"Content-Length": {"999"}}},
		{"h1 stale Transfer-Encoding on a decoded body", false, "POST", "hello", map[string][]string{"Transfer-Encoding": {"chunked"}}},
		{"h2 body", true, "POST", "hello", nil},
		{"h2 empty POST", true, "POST", "", nil},
		{"h2 GET without body", true, "GET", "", nil},
		{"h2 template Content-Length is wrong", true, "POST", "hello", map[string][]string{"Content-Length": {"999"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc := newWireCapture(t, tc.h2)
			snd := newOptSender(t)
			var opts *SendOptions
			if tc.h2 {
				opts = &SendOptions{HTTP2: true}
			}
			f, err := snd.Send(Request{Method: tc.method, URL: wc.srv.URL + "/p", Body: []byte(tc.body), Headers: tc.headers, Options: opts})
			if err != nil || f.Status != 200 {
				t.Fatalf("send: %v %+v", err, f)
			}
			wire, proto := wc.got()
			if tc.h2 != (proto == "HTTP/2.0") {
				t.Fatalf("server saw %s", proto)
			}
			stored, err := snd.st.GetFlow(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, fl := range []*store.Flow{f, stored} {
				for _, name := range []string{"Content-Length", "Transfer-Encoding"} {
					got, want := headerValues(fl.ReqHeaders, name), wire.Values(name)
					if strings.Join(got, ",") != strings.Join(want, ",") {
						t.Errorf("flow %s = %q, the target received %q", name, got, want)
					}
				}
			}
			if tc.body != "" && wire.Get("Content-Length") != "5" {
				t.Fatalf("precondition: wire Content-Length = %q", wire.Values("Content-Length"))
			}
		})
	}
}

// A body of unknown length is what makes the transport choose chunked framing
// on HTTP/1.1 and send no content-length on HTTP/2. The sender's own requests
// always know their length, so this drives the recorder directly through the
// sender's transports.
func TestR3StreamedBodyFramingIsRecordedAsSent(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		var mu sync.Mutex
		var te []string
		var cl int64
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			mu.Lock()
			te, cl = r.TransferEncoding, r.ContentLength
			mu.Unlock()
		}))
		srv.EnableHTTP2 = h2
		if h2 {
			srv.StartTLS()
		} else {
			srv.Start()
		}
		snd := newOptSender(t)
		cl2, err := snd.clientFor(&SendOptions{HTTP2: h2})
		if err != nil {
			t.Fatal(err)
		}
		pr, pw := io.Pipe()
		go func() { io.WriteString(pw, "streamed"); pw.Close() }()
		req, _ := http.NewRequest("POST", srv.URL, pr)
		var wire sentHeaders
		req = req.WithContext(httptrace.WithClientTrace(context.Background(), wire.trace(false)))
		resp, err := cl2.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		srv.Close()
		flow := &store.Flow{ReqHeaders: map[string][]string{"Content-Length": {"8"}}}
		wire.recordOn(flow)
		mu.Lock()
		if cl != -1 {
			t.Fatalf("h2=%v precondition: server saw Content-Length %d for a streamed body", h2, cl)
		}
		wantTE := strings.Join(te, ",")
		mu.Unlock()
		if got := headerValues(flow.ReqHeaders, "Content-Length"); len(got) != 0 {
			t.Errorf("h2=%v: flow keeps Content-Length %q that was never sent", h2, got)
		}
		if got := strings.Join(headerValues(flow.ReqHeaders, "Transfer-Encoding"), ","); got != wantTE {
			t.Errorf("h2=%v: flow Transfer-Encoding = %q, the target received %q", h2, got, wantTE)
		}
	}
}

// The hand-written wire path adds Content-Length itself when the caller's raw
// headers carry no framing: that header reached the target, so it is recorded.
func TestR3RawHeaderSendRecordsAddedContentLength(t *testing.T) {
	wc := newWireCapture(t, false)
	snd := newOptSender(t)
	f, err := snd.Send(Request{Method: "POST", URL: wc.srv.URL, Body: []byte("hello"),
		Options: &SendOptions{RawHeaders: []RawHeader{{Name: "X-Probe", Value: "1"}}}})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	wire, _ := wc.got()
	if wire.Get("Content-Length") != "5" {
		t.Fatalf("precondition: wire Content-Length = %q", wire.Values("Content-Length"))
	}
	if got := headerValues(f.ReqHeaders, "Content-Length"); len(got) != 1 || got[0] != "5" {
		t.Errorf("raw send recorded Content-Length %q, the target received 5", got)
	}
	// The caller's own value is written as given and stays as given.
	f, err = snd.Send(Request{Method: "POST", URL: wc.srv.URL, Body: []byte("hello"),
		Options: &SendOptions{RawHeaders: []RawHeader{{Name: "content-length", Value: "5"}}}})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	if got := headerValues(f.ReqHeaders, "Content-Length"); len(got) != 1 || got[0] != "5" {
		t.Errorf("raw send with its own content-length recorded %q", got)
	}
}

// r3MacroTarget records the User-Agent of every request by path.
type r3MacroTarget struct {
	mu  sync.Mutex
	ua  map[string][]string
	srv *httptest.Server
}

func newR3MacroTarget(t *testing.T) *r3MacroTarget {
	t.Helper()
	mt := &r3MacroTarget{ua: map[string][]string{}}
	mt.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt.mu.Lock()
		mt.ua[r.URL.Path] = append(mt.ua[r.URL.Path], strings.Join(r.Header.Values("User-Agent"), "|"))
		mt.mu.Unlock()
		switch r.URL.Path {
		case "/macro":
			io.WriteString(w, "tok=abc")
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "1"})
		}
	}))
	t.Cleanup(mt.srv.Close)
	return mt
}

func (mt *r3MacroTarget) last(path string) string {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	v := mt.ua[path]
	if len(v) == 0 {
		return "<no request>"
	}
	return v[len(v)-1]
}

const r3GoUA = "Go-http-client/1.1"

// The token macro and the login macro serve an attack; a target must not see
// two different clients in one session. They send the attack's User-Agent
// (the session's when it defines one, else the template's) unless the macro
// request sets its own, and keep Go's default when nothing defines one.
func TestR3MacrosSendTheAttackUserAgent(t *testing.T) {
	cases := []struct {
		name      string
		template  map[string][]string
		session   []Header
		macroLine string // extra header line in the macro requests
		want      string
	}{
		{"nothing defines one: unchanged", nil, nil, "", r3GoUA},
		{"template", map[string][]string{"user-agent": {"probe/1"}}, nil, "", "probe/1"},
		{"session", nil, []Header{{Key: "User-Agent", Value: "sess/2"}}, "", "sess/2"},
		{"session wins over template, as on the attack", map[string][]string{"User-Agent": {"probe/1"}}, []Header{{Key: "user-agent", Value: "sess/2"}}, "", "sess/2"},
		{"explicit macro header wins", map[string][]string{"User-Agent": {"probe/1"}}, []Header{{Key: "User-Agent", Value: "sess/2"}}, "User-Agent: macro/3\r\n", "macro/3"},
		{"template suppresses the header", map[string][]string{"User-Agent": {""}}, nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mt := newR3MacroTarget(t)
			snd := newTestSender(t)
			if tc.session != nil {
				snd.SetSession(true, tc.session)
			}
			snd.SetMacro(Macro{Enabled: true, Target: mt.srv.URL, Request: "GET /macro HTTP/1.1\r\nHost: x\r\n" + tc.macroLine + "\r\n",
				Extract: `tok=(\w+)`, InjectMode: "header", InjectName: "X-Tok"})
			if err := snd.SetLoginMacro(LoginMacro{Enabled: true, Target: mt.srv.URL, RefreshSecs: 3600,
				Request: "POST /login HTTP/1.1\r\nHost: x\r\n" + tc.macroLine + "\r\n"}); err != nil {
				t.Fatal(err)
			}
			f, err := snd.Send(Request{URL: mt.srv.URL + "/attack", Headers: tc.template})
			if err != nil || f.Status != 200 {
				t.Fatalf("send: %v %+v", err, f)
			}
			attack := mt.last("/attack")
			for _, path := range []string{"/macro", "/login"} {
				if got := mt.last(path); got != tc.want {
					t.Errorf("%s User-Agent = %q, want %q (attack sent %q)", path, got, tc.want, attack)
				}
			}
			if tc.macroLine == "" && attack != tc.want {
				t.Errorf("attack User-Agent = %q, want %q", attack, tc.want)
			}
		})
	}
}

// A login run by hand (no attack in play) uses the session's User-Agent, and
// the dry run does the same.
func TestR3ManualLoginMacroUsesSessionUserAgent(t *testing.T) {
	mt := newR3MacroTarget(t)
	snd := newTestSender(t)
	snd.SetSession(true, []Header{{Key: "User-Agent", Value: "sess/2"}})
	m := LoginMacro{Enabled: true, Target: mt.srv.URL, Request: "POST /login HTTP/1.1\r\nHost: x\r\n\r\n"}
	if err := snd.SetLoginMacro(m); err != nil {
		t.Fatal(err)
	}
	if _, err := snd.RunLoginMacroNow(); err != nil {
		t.Fatal(err)
	}
	if got := mt.last("/login"); got != "sess/2" {
		t.Errorf("manual login User-Agent = %q, want the session's", got)
	}
	snd.SetSession(true, []Header{{Key: "User-Agent", Value: "sess/3"}})
	if _, _, err := snd.TestLoginMacro(m); err != nil {
		t.Fatal(err)
	}
	if got := mt.last("/login"); got != "sess/3" {
		t.Errorf("dry-run login User-Agent = %q, want the session's", got)
	}
	// A disabled session defines nothing.
	snd.SetSession(false, []Header{{Key: "User-Agent", Value: "sess/3"}})
	if _, _, err := snd.TestLoginMacro(m); err != nil {
		t.Fatal(err)
	}
	if got := mt.last("/login"); got != r3GoUA {
		t.Errorf("login with the session off sent %q, want Go's default", got)
	}
}
