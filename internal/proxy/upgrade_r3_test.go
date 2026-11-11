package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// r3Upgrade opens an upgraded stream through the proxy and returns the client
// conn with a reader positioned after the 101 head.
func r3Upgrade(t *testing.T, proxyAddr, origin, proto string) (*net.TCPConn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, strings.Replace(wsUpgradeRequest, "websocket", proto, 1), origin, origin)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: resp=%v err=%v", resp, err)
	}
	return c.(*net.TCPConn), br
}

func r3UpgradeOrigin(t *testing.T, proto string, after func(c net.Conn, br *bufio.Reader)) string {
	t.Helper()
	return tcpOrigin(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", proto)
		after(c, br)
	})
}

// An exec/attach-style upgrade: the client sends its input, shuts down its
// write side, and then reads the output. The splice used to close the upstream
// connection as soon as the client's direction ended, losing the response.
func TestR3RawUpgradeSupportsClientHalfClose(t *testing.T) {
	origin := r3UpgradeOrigin(t, "tcp", func(c net.Conn, br *bufio.Reader) {
		in, _ := io.ReadAll(br) // returns only once the client's FIN arrives
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(c, "got %d bytes: %s", len(in), in)
	})
	_, _, addr := r2Proxy(t, nil, false)
	c, br := r3Upgrade(t, addr, origin, "tcp")
	io.WriteString(c, "stdin")
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read after half-close: %v", err)
	}
	if string(got) != "got 5 bytes: stdin" {
		t.Fatalf("output after client half-close = %q, want the origin's answer", got)
	}
}

// The other direction: an origin that finishes its output while the client is
// still sending keeps receiving the client's bytes.
func TestR3RawUpgradeSupportsOriginHalfClose(t *testing.T) {
	gotc := make(chan string, 1)
	origin := r3UpgradeOrigin(t, "tcp", func(c net.Conn, br *bufio.Reader) {
		io.WriteString(c, "hello")
		c.(*net.TCPConn).CloseWrite()
		rest, _ := io.ReadAll(br)
		gotc <- string(rest)
	})
	_, _, addr := r2Proxy(t, nil, false)
	c, br := r3Upgrade(t, addr, origin, "tcp")
	if b, err := io.ReadAll(br); err != nil || string(b) != "hello" {
		t.Fatalf("origin greeting = %q, %v", b, err)
	}
	io.WriteString(c, "late client data")
	c.Close()
	select {
	case got := <-gotc:
		if got != "late client data" {
			t.Fatalf("origin received %q after its half-close", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("origin never saw the client's data after half-closing")
	}
}

// A half-closed upgraded stream whose other side neither answers nor closes is
// reaped after the idle bound instead of being held forever.
func TestR3HalfClosedUpgradeIsReapedWhenIdle(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	origin := r3UpgradeOrigin(t, "tcp", func(c net.Conn, br *bufio.Reader) { <-release })
	srv, _, addr := r2Proxy(t, nil, false)
	srv.halfCloseIdle = 150 * time.Millisecond
	c, br := r3Upgrade(t, addr, origin, "tcp")
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(br); err != nil {
		t.Fatalf("half-closed idle upgrade was not torn down: %v", err)
	}
}

// WebSocket: a client that sends its Close frame and shuts down its write side
// still receives the frames the server sends afterwards (its Close reply), and
// both directions' frames are captured.
func TestR3WebSocketHalfCloseKeepsRepliesAndCapture(t *testing.T) {
	closeFrame := []byte{0x88, 0x02, 0x03, 0xe8} // server Close, status 1000
	origin := r3UpgradeOrigin(t, "websocket", func(c net.Conn, br *bufio.Reader) {
		io.ReadAll(br) // wait for the client's FIN
		time.Sleep(50 * time.Millisecond)
		c.Write(wsTextFrame("bye", false))
		c.Write(closeFrame)
	})
	_, st, addr := r2Proxy(t, nil, false)
	c, br := r3Upgrade(t, addr, origin, "websocket")
	c.Write(wsTextFrame("hi", true))
	c.Write([]byte{0x88, 0x80, 1, 2, 3, 4}) // masked empty Close
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read after half-close: %v", err)
	}
	want := append(wsTextFrame("bye", false), closeFrame...)
	if !bytes.Equal(got, want) {
		t.Fatalf("frames after client half-close = %x, want %x", got, want)
	}
	f := waitFlows(t, st, 1)[0]
	frames := waitWSFrames(t, st, f.ID, 4)
	seen := map[string]bool{}
	for _, fr := range frames {
		seen[fmt.Sprintf("%s/%d/%s", fr.Dir, fr.Opcode, fr.Preview)] = true
	}
	for _, k := range []string{"send/1/hi", "send/8/", "recv/1/bye", "recv/8/\x03\xe8"} {
		if !seen[k] {
			t.Fatalf("frame %q not captured; got %v", k, seen)
		}
	}
}

// A WebSocket whose stream ends inside a frame is not a clean end: the tunnel
// is torn down at once, as before.
func TestR3WebSocketTruncatedFrameTearsDown(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	origin := r3UpgradeOrigin(t, "websocket", func(c net.Conn, br *bufio.Reader) { <-release })
	srv, _, addr := r2Proxy(t, nil, false)
	srv.halfCloseIdle = 30 * time.Second
	c, br := r3Upgrade(t, addr, origin, "websocket")
	c.Write([]byte{0x81, 0x85, 1, 2, 3, 4, 'a'}) // 5-byte frame, 1 byte sent
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(br); err != nil {
		t.Fatalf("truncated WebSocket stream was not torn down promptly: %v", err)
	}
}

// A client that goes away while the origin is still thinking about the upgrade
// must release the upstream connection at once, not after the response bound.
func TestR3UpgradeWaitEndsWhenClientGoes(t *testing.T) {
	closed := make(chan time.Time, 1)
	origin := tcpOrigin(t, func(c net.Conn) {
		io.Copy(io.Discard, c) // reads the request, never answers; returns on FIN/RST
		closed <- time.Now()
	})
	srv, st, addr := r2Proxy(t, nil, false)
	srv.upgradeResponse = 20 * time.Second

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, wsUpgradeRequest, origin, origin)
	time.Sleep(100 * time.Millisecond) // let the proxy forward the handshake
	start := time.Now()
	c.Close()
	select {
	case at := <-closed:
		if d := at.Sub(start); d > 2*time.Second {
			t.Fatalf("upstream released %v after the client left", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream connection still held 3s after the client disconnected")
	}
	f := waitFlows(t, st, 1)[0]
	if f.Error != errClientAborted || f.Status != 0 {
		t.Fatalf("flow status=%d error=%q, want a client abort", f.Status, f.Error)
	}
}

// Bytes a client sends before the 101 arrives (optimistic upgrade) are not
// taken for a disconnect and still reach the origin after the handshake.
func TestR3UpgradeWatcherKeepsEarlyClientBytes(t *testing.T) {
	gotc := make(chan string, 1)
	origin := tcpOrigin(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		b := make([]byte, 5)
		io.ReadFull(br, b)
		gotc <- string(b)
	})
	_, _, addr := r2Proxy(t, nil, false)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, strings.Replace(wsUpgradeRequest, "websocket", "tcp", 1), origin, origin)
	time.Sleep(50 * time.Millisecond)
	io.WriteString(c, "early")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: resp=%v err=%v", resp, err)
	}
	select {
	case got := <-gotc:
		if got != "early" {
			t.Fatalf("origin received %q, want the client's early bytes", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("early client bytes never reached the origin")
	}
}

func r3StreamResponse() *http.Response {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/events", nil)
	return &http.Response{
		StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Proto: "HTTP/1.1",
		Header:        http.Header{"Content-Type": {"text/event-stream"}},
		ContentLength: -1,
		Body:          io.NopCloser(strings.NewReader("data: x\n\n")),
		Request:       req,
	}
}

func r3CaptureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func r3RuleServer(t *testing.T, rules []store.Rule, holdResponses bool) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := intercept.New()
	if err := eng.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	eng.SetResponseEnabled(holdResponses)
	return New(st, nil, nil, eng, nil)
}

// Body rules and the response hold do not apply to an open-ended stream; the
// operator is told why (once per 10 s), instead of the skip being silent.
func TestR3EndlessStreamSkipIsLogged(t *testing.T) {
	bodyRule := []store.Rule{{Enabled: true, Type: "res-body", Match: "x", Replace: "y"}}
	headRule := []store.Rule{{Enabled: true, Type: "res-header", Match: "X-A: 1", Replace: "X-A: 2"}}
	cases := []struct {
		name  string
		rules []store.Rule
		hold  bool
		want  bool
	}{
		{"body rule", bodyRule, false, true},
		{"response hold", nil, true, true},
		{"header rule only", headRule, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := r3RuleServer(t, tc.rules, tc.hold)
			endlessSkipLog.Store(0)
			buf := r3CaptureLog(t)
			flow := srv.newFlow(&store.Flow{Host: "example.com", Path: "/events"})
			for i := 0; i < 3; i++ {
				if _, _, _, transformed, _ := srv.maybeInterceptResponse(flow, r3StreamResponse()); transformed {
					t.Fatal("an open-ended stream was buffered")
				}
			}
			n := strings.Count(buf.String(), "skipped for streaming response example.com/events")
			if tc.want && n != 1 {
				t.Fatalf("skip logged %d times, want once (rate-limited): %q", n, buf.String())
			}
			if !tc.want && n != 0 {
				t.Fatalf("header-only rules logged a skip: %q", buf.String())
			}
		})
	}
}
