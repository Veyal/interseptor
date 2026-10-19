package control

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Quitting must not wait out the server's shutdown timeout on the UI's open
// /api/events stream: http.Server.Shutdown waits for active connections, and an
// SSE handler only returns when its stream ends.
func TestEventStreamEndsOnCloseStreams(t *testing.T) {
	h, _, _ := newHub(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h.Handler()}
	go srv.Serve(ln)
	defer srv.Close()

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if line, _ := br.ReadString('\n'); !strings.HasPrefix(line, "event: hello") {
		t.Fatalf("first line = %q", line)
	}

	h.CloseStreams()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an open event stream: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Shutdown took %s with an open event stream", d)
	}
}
