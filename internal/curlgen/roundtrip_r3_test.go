package curlgen

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type r3Seen struct {
	method, uri string
	header      http.Header
	body        []byte
}

// r3Run executes the generated command with bash against a loopback server and
// returns what the server received.
func r3Run(t *testing.T, method, pathAndQuery string, headers http.Header, body []byte) r3Seen {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	seen := make(chan r3Seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- r3Seen{r.Method, r.RequestURI, r.Header.Clone(), b}
	}))
	defer srv.Close()
	cmd := Build(method, srv.URL+pathAndQuery, headers, body)
	c := exec.Command("bash", "-c", cmd+" --max-time 5 -s -o /dev/null")
	c.WaitDelay = 2 * time.Second
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v\n%s\n%s", err, out, cmd)
	}
	select {
	case s := <-seen:
		return s
	case <-time.After(3 * time.Second):
		t.Fatalf("server saw no request for:\n%s", cmd)
	}
	return r3Seen{}
}

// Hostile header values and bodies must reach the target byte for byte: the
// command is copied into a terminal, so anything the shell or curl would
// expand, substitute or reinterpret is a fidelity (and safety) bug.
func TestR3HostileValuesSurviveBashAndCurl(t *testing.T) {
	marker := t.TempDir() + "/pwned"
	hostile := []string{
		`'; touch ` + marker + `; '`,
		"$(touch " + marker + ")",
		"`touch " + marker + "`",
		`"double" \back\slash \'`,
		"$HOME ${PATH} !! !$ ~ * ? [a-z] {a,b} # & | ; < > ( )",
		"@/etc/passwd",
		"tab\there  two spaces",
		"percent %s %n %41",
		"unicode \u00e9 \u4e2d \U0001f600",
		"'''",
		`\`,
	}
	for _, v := range hostile {
		h := http.Header{"X-Probe": {v}, "Content-Type": {"text/plain"}}
		got := r3Run(t, "POST", "/p", h, []byte(v))
		if g := got.header.Get("X-Probe"); g != v {
			t.Errorf("header value %q arrived as %q", v, g)
		}
		if !bytes.Equal(got.body, []byte(v)) {
			t.Errorf("body %q arrived as %q", v, got.body)
		}
		if got.header.Get("Content-Type") != "text/plain" {
			t.Errorf("Content-Type arrived as %q", got.header.Get("Content-Type"))
		}
	}
	if _, err := exec.Command("test", "-e", marker).CombinedOutput(); err == nil {
		t.Fatal("a hostile value was executed by the shell")
	}
}

// Bodies with newlines, CRLF, leading '@' or '-', '&' and '=' (which curl's
// plain -d would treat specially) arrive unchanged.
func TestR3BodiesArriveUnchanged(t *testing.T) {
	bodies := []string{
		"line1\nline2\r\nline3\n",
		"@file-looking",
		"-starts-with-dash",
		"a=1&b=2&c=%20+",
		"{\"k\": \"v'\\\"\"}",
		strings.Repeat("A", 100_000),
		"trailing newline\n",
		"\xff\xfe binary-ish \x01\x7f",
	}
	for _, b := range bodies {
		got := r3Run(t, "PUT", "/p", http.Header{"Content-Type": {"application/octet-stream"}}, []byte(b))
		if !bytes.Equal(got.body, []byte(b)) {
			t.Errorf("body %.40q (len %d) arrived as %.40q (len %d)", b, len(b), got.body, len(got.body))
		}
		if got.method != "PUT" {
			t.Errorf("method arrived as %q", got.method)
		}
	}
}

// The request target is sent as captured: curl must not expand [] and {} as
// URL globs, collapse dot segments, or drop a query string.
func TestR3RequestTargetArrivesUnchanged(t *testing.T) {
	targets := []string{
		"/a/../b/./c?x=1",
		"/items[1]?ids[]=1&ids[]=2",
		"/tmpl/{a,b}/x?q={1..3}",
		"/q?redirect=http://example.com/&a='quoted'&b=$(id)",
		"/p%2e%2e/%2F?x=%00",
	}
	for _, target := range targets {
		got := r3Run(t, "GET", target, nil, nil)
		if got.uri != target {
			t.Errorf("target %q arrived as %q", target, got.uri)
		}
	}
}

// The command reproduces the captured request, so it must not add headers the
// capture did not have, nor send different values for the ones curl defaults.
func TestR3NoHeadersAreInvented(t *testing.T) {
	captured := http.Header{"X-Only": {"1"}}
	got := r3Run(t, "POST", "/p", captured, []byte("a=1"))
	for _, name := range []string{"User-Agent", "Accept", "Content-Type"} {
		if v := got.header.Values(name); len(v) != 0 {
			t.Errorf("curl added %s: %q, which the captured request did not carry", name, v)
		}
	}
	// What the capture does carry is sent as captured.
	captured = http.Header{"User-Agent": {"probe/1"}, "Accept": {"text/x"}, "Content-Type": {"application/json"}}
	got = r3Run(t, "POST", "/p", captured, []byte("{}"))
	for name, want := range captured {
		if v := got.header.Get(name); v != want[0] {
			t.Errorf("%s arrived as %q, want %q", name, v, want[0])
		}
	}
}

// A HEAD request must complete: "-X HEAD" makes curl wait for a body that a
// HEAD response never has.
func TestR3HeadRequestCompletes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	lines := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		lines <- strings.SplitN(string(buf[:n]), "\r\n", 2)[0]
		// keep-alive HEAD response announcing a body it will not send
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n")
		time.Sleep(4 * time.Second)
	}()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	cmd := Build("HEAD", "http://"+ln.Addr().String()+"/h", nil, nil)
	start := time.Now()
	c := exec.Command("bash", "-c", cmd+" --max-time 3 -s -o /dev/null")
	c.WaitDelay = 2 * time.Second
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("HEAD command failed after %v: %v %s\n%s", time.Since(start), err, out, cmd)
	}
	if got := <-lines; got != "HEAD /h HTTP/1.1" {
		t.Fatalf("request line = %q", got)
	}
}
