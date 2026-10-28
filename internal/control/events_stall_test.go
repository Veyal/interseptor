package control

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A client that stops reading must not pin the SSE handler in a blocked write
// forever: the per-write deadline ends the stream so the goroutine exits and
// Shutdown is not held up.
func TestEventStreamDropsStalledClient(t *testing.T) {
	orig := sseWriteTimeout
	sseWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sseWriteTimeout = orig })

	h, _, _ := newHub(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h.Handler()}
	go srv.Serve(ln)
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	if _, err := io.WriteString(conn, "GET /api/events HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	clients := func() int {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.clients)
	}
	deadline := time.Now().Add(5 * time.Second)
	for clients() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("event stream never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Never read: flood the stream until the kernel buffers fill and the
	// handler's write blocks, then expect it to give up and unsubscribe.
	pad := strings.Repeat("a", 256<<10)
	for clients() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("stalled SSE client still subscribed: handler blocked in write")
		}
		h.broadcast(map[string]any{"type": "pad", "pad": pad})
		time.Sleep(2 * time.Millisecond)
	}
}
