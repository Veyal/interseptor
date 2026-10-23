package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// A non-WebSocket upgrade (here "Upgrade: tcp", like docker attach / h2c /
// SPDY) must be spliced raw: parsing its bytes as WebSocket frames stalls or
// corrupts the stream.
func TestProxyRelaysNonWebSocketUpgradeRaw(t *testing.T) {
	upLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upLn.Close()
	go func() {
		c, err := upLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		io.Copy(c, br) // echo the upgraded stream
	}()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(c, "POST http://%s/attach HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 0\r\n\r\n", upLn.Addr(), upLn.Addr())
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: "POST"})
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade response: %v / %v", err, resp)
	}
	const msg = "x\xff\n" // parses as a masked WS header with a 64-bit length: frame parsing would block for 12 more bytes
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("echo over raw upgrade not relayed: %v", err)
	}
	if string(got) != msg {
		t.Fatalf("echo = %q, want %q", got, msg)
	}
}
