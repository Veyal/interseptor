package wsrepeater

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// A URL without a host used to be dialled as ":80" / ":443", which the OS
// resolves to the local machine: "ws:///x" reached whatever listens on
// localhost:80. Schemes other than ws/wss/http/https were sent as plain ws.
func TestSendRejectsHostlessAndForeignSchemeURLs(t *testing.T) {
	for _, raw := range []string{"ws:///path", "wss://", "ws://:80/x", "/just/a/path", "ftp://example.com/x", "file:///etc/hosts", "example.com:80"} {
		done := make(chan error, 1)
		go func() {
			_, err := Send(Request{URL: raw, Message: "x", ReadFor: 50 * time.Millisecond,
				CheckDial: func(net.IP, int) error { t.Errorf("%q: a connection was attempted", raw); return nil }})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "invalid URL") {
				t.Errorf("Send(%q) error = %v, want an invalid URL error", raw, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Send(%q) did not return", raw)
		}
	}
}

// rawServer accepts one connection and hands it to serve.
func rawServer(t *testing.T, serve func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		serve(conn)
	}()
	return ln.Addr().String()
}

// The handshake response is read before any frame cap applies. A server that
// answers with an endless header line must not be buffered without bound.
func TestSendBoundsHandshakeResponse(t *testing.T) {
	const sent = 4 << 20
	addr := rawServer(t, func(conn net.Conn) {
		br := bufio.NewReader(conn)
		for { // consume the request head
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nX-Filler: "))
		chunk := bytes.Repeat([]byte("a"), 64<<10)
		for n := 0; n < sent; n += len(chunk) {
			if _, err := conn.Write(chunk); err != nil {
				return // the client gave up: that is the point
			}
		}
		_, _ = conn.Write([]byte("\r\n\r\n"))
	})
	start := time.Now()
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "x", ReadFor: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("err = %v (res=%v), want a handshake error", err, res != nil)
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want the oversized handshake to be named", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("oversized handshake took %v to refuse", d)
	}
}

func serverFrame(fin bool, op byte, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		out = append(out, byte(n))
	case n < 1<<16:
		out = append(out, 126)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	default:
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	return append(out, payload...)
}

// maxReplyBytes bounds what one send keeps. A server that keeps starting a new
// message without finishing the previous one took the "interrupted message"
// path, which added to the total but never checked it: 64 unfinished messages
// of up to 16 MB each were all retained.
func TestSendCapsTotalAcrossUnfinishedMessages(t *testing.T) {
	const frameSize = 8 << 20
	const frames = 12 // 96 MB offered, far past the 32 MB cap
	addr := wsFragmentedServer(t, func(conn net.Conn, _ *bufio.Reader) {
		payload := make([]byte, frameSize)
		for i := 0; i < frames; i++ {
			// FIN clear and a data opcode every time: each frame abandons the last.
			if _, err := conn.Write(serverFrame(false, opBinary, payload)); err != nil {
				return
			}
		}
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "x", ReadFor: 5 * time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	total := 0
	for _, f := range recvFrames(res) {
		total += f.Len
	}
	// One message may straddle the cap, so allow a single frame of slack.
	if total > maxReplyBytes+frameSize {
		t.Fatalf("kept %d MB of replies; the cap is %d MB", total>>20, maxReplyBytes>>20)
	}
	if total == 0 {
		t.Fatal("no reply kept at all")
	}
}
