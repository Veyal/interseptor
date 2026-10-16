package wsrepeater

import (
	"bufio"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"testing"
	"time"
)

func TestAcceptKeyRFCVector(t *testing.T) {
	// RFC 6455 §1.3 worked example.
	if got := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("acceptKey vector wrong: %s", got)
	}
}

func TestClientFrameRoundTrip(t *testing.T) {
	enc := encodeClientFrame(opText, []byte("hello world"))
	// Client frames must set the mask bit.
	if enc[1]&0x80 == 0 {
		t.Fatal("client frame must be masked")
	}
	op, payload, err := readFrame(bufio.NewReader(bytesReader(enc)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if op != opText || string(payload) != "hello world" {
		t.Fatalf("round-trip mismatch: op=%d payload=%q", op, payload)
	}
}

func TestSendAgainstEchoServer(t *testing.T) {
	addr, stop := wsEchoServer(t)
	defer stop()

	res, err := Send(Request{URL: "ws://" + addr + "/chat", Message: "ping-42", ReadFor: time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Status != 101 {
		t.Fatalf("expected 101, got %d", res.Status)
	}
	var sawSend, sawEcho bool
	for _, f := range res.Frames {
		if f.Dir == "send" && f.Text == "ping-42" {
			sawSend = true
		}
		if f.Dir == "recv" && f.Text == "ping-42" {
			sawEcho = true
		}
	}
	if !sawSend || !sawEcho {
		t.Fatalf("expected send+echo of ping-42, got %+v", res.Frames)
	}
}

func TestSendBadHandshake(t *testing.T) {
	// A plain HTTP server that never upgrades.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		fmt.Fprint(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
	}()
	res, err := Send(Request{URL: "ws://" + ln.Addr().String() + "/", Message: "x", ReadFor: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("expected error on non-101 handshake")
	}
	if res == nil || res.Status != 400 {
		t.Fatalf("expected status 400 in result, got %+v", res)
	}
}

func TestSendPingRespondsWithPong(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	pongReceived := make(chan bool, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		tp := textproto.NewReader(br)
		tp.ReadLine()
		hdr, _ := tp.ReadMIMEHeader()
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey(hdr.Get("Sec-WebSocket-Key")))

		// Read client text frame
		_, _, _ = readFrame(br)

		// Send ping frame from server to client
		conn.Write(encodeServerFrame(opPing, []byte("heartbeat-data")))

		// Read client's reply frame
		op, payload, err := readFrame(br)
		if err == nil && op == opPong && string(payload) == "heartbeat-data" {
			pongReceived <- true
		} else {
			pongReceived <- false
		}
		// Send final reply text frame so client finishes reading
		conn.Write(encodeServerFrame(opText, []byte("done")))
	}()

	res, err := Send(Request{URL: "ws://" + ln.Addr().String() + "/", Message: "hello", ReadFor: time.Second})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	select {
	case ok := <-pongReceived:
		if !ok {
			t.Fatal("expected pong with matching payload heartbeat-data")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong")
	}
	if len(res.Frames) < 2 {
		t.Fatalf("expected at least 2 frames (send and recv), got %+v", res.Frames)
	}
}

func TestWriteHandshakeDeduplicatesHeaders(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	u, _ := url.Parse("ws://example.com/socket")
	extra := map[string]string{
		"host":                  "malicious.com",
		"UPGRADE":               "something-else",
		"Connection":            "close",
		"Sec-WebSocket-Key":     "dummy",
		"sec-websocket-version": "99",
		"X-Custom-Auth":         "bearer-token",
	}

	go func() {
		_ = writeHandshake(c1, u, "my-key", extra)
	}()

	br := bufio.NewReader(c2)
	tp := textproto.NewReader(br)
	_, _ = tp.ReadLine()
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read mime header: %v", err)
	}

	if h := hdr.Values("Host"); len(h) != 1 || h[0] != "example.com" {
		t.Fatalf("expected 1 Host header for example.com, got %v", h)
	}
	if up := hdr.Values("Upgrade"); len(up) != 1 || up[0] != "websocket" {
		t.Fatalf("expected 1 Upgrade header for websocket, got %v", up)
	}
	if cn := hdr.Values("Connection"); len(cn) != 1 || cn[0] != "Upgrade" {
		t.Fatalf("expected 1 Connection header for Upgrade, got %v", cn)
	}
	if key := hdr.Get("Sec-WebSocket-Key"); key != "my-key" {
		t.Fatalf("expected Sec-WebSocket-Key my-key, got %s", key)
	}
	if hdr.Get("X-Custom-Auth") != "bearer-token" {
		t.Fatalf("expected X-Custom-Auth header preserved")
	}
}

// ---- test helpers ----

// wsEchoServer accepts one connection, completes the WS handshake, and echoes
// the first client frame back as an unmasked server frame.
func wsEchoServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		tp := textproto.NewReader(br)
		tp.ReadLine() // request line
		hdr, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey(hdr.Get("Sec-WebSocket-Key")))
		op, payload, err := readFrame(br)
		if err != nil {
			return
		}
		conn.Write(encodeServerFrame(byte(op), payload))
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// encodeServerFrame builds an unmasked frame (server→client), test-only.
func encodeServerFrame(opcode byte, payload []byte) []byte {
	out := []byte{0x80 | opcode, byte(len(payload))}
	return append(out, payload...)
}

type br struct {
	b []byte
	i int
}

func (r *br) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func bytesReader(b []byte) *br { return &br{b: b} }
