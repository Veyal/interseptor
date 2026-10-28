package sender

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// captureOneRequest accepts one connection, returns its request head lines and
// the body bytes as written on the wire (decoded per the request's framing).
func captureOneRequest(t *testing.T) (string, <-chan []string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	heads := make(chan []string, 1)
	bodies := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var lines []string
		chunked := false
		length := 0
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				return
			}
			l = strings.TrimRight(l, "\r\n")
			if l == "" {
				break
			}
			lines = append(lines, l)
			k, v, _ := strings.Cut(l, ":")
			switch http.CanonicalHeaderKey(strings.TrimSpace(k)) {
			case "Transfer-Encoding":
				chunked = strings.Contains(strings.ToLower(v), "chunked")
			case "Content-Length":
				for _, ch := range strings.TrimSpace(v) {
					length = length*10 + int(ch-'0')
				}
			}
		}
		heads <- lines
		var raw []byte
		if chunked {
			// Read the chunked framing verbatim up to and including the terminator.
			var sb strings.Builder
			for !strings.HasSuffix(sb.String(), "0\r\n\r\n") {
				b, err := br.ReadByte()
				if err != nil {
					break
				}
				sb.WriteByte(b)
			}
			raw = []byte(sb.String())
		} else {
			raw = make([]byte, length)
			_, _ = io.ReadFull(br, raw)
		}
		bodies <- string(raw)
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
	}()
	return ln.Addr().String(), heads, bodies
}

func TestSendKeepsChunkedTemplateFraming(t *testing.T) {
	addr, heads, bodies := captureOneRequest(t)
	snd := newTestSender(t)
	body := "5\r\nhello\r\n0\r\n\r\n"
	f, err := snd.Send(Request{
		Method:  "POST",
		URL:     "http://" + addr + "/up",
		Headers: map[string][]string{"Transfer-Encoding": {"chunked"}, "X-A": {"1"}},
		Body:    []byte(body),
	})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	lines := <-heads
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "Transfer-Encoding: chunked") {
		t.Fatalf("wire head lacks Transfer-Encoding: %v", lines)
	}
	if strings.Contains(strings.ToLower(joined), "content-length") {
		t.Fatalf("chunked template re-framed with Content-Length: %v", lines)
	}
	if !strings.Contains(joined, "X-A: 1") {
		t.Fatalf("wire head lost headers: %v", lines)
	}
	if got := <-bodies; got != body {
		t.Fatalf("wire body = %q, want %q", got, body)
	}
}

// A decoded body under a stale Transfer-Encoding header (e.g. an imported
// flow) is not chunk-framed and keeps net/http's length framing.
func TestSendDecodedBodyWithStaleTransferEncodingUsesLength(t *testing.T) {
	addr, heads, bodies := captureOneRequest(t)
	snd := newTestSender(t)
	f, err := snd.Send(Request{
		Method:  "POST",
		URL:     "http://" + addr + "/up",
		Headers: map[string][]string{"Transfer-Encoding": {"chunked"}},
		Body:    []byte("hello"),
	})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	if joined := strings.Join(<-heads, "|"); !strings.Contains(joined, "Content-Length: 5") {
		t.Fatalf("wire head = %s", joined)
	}
	if got := <-bodies; got != "hello" {
		t.Fatalf("wire body = %q", got)
	}
}
