package wsrepeater

import (
	"testing"
	"time"
)

// The Result keeps the handshake as sent and as answered so the exchange can be
// recorded as a flow and replayed as evidence.
func TestSendReturnsHandshakeHeaders(t *testing.T) {
	addr, stop := wsEchoServer(t)
	defer stop()

	res, err := Send(Request{
		URL: "ws://" + addr + "/chat?token=abc", Message: "hi", ReadFor: time.Second,
		Headers: map[string]string{"X-Probe": "1", "Upgrade": "ignored"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := res.ReqHeaders["Upgrade"]; len(got) != 1 || got[0] != "websocket" {
		t.Fatalf("ReqHeaders Upgrade = %v, want single websocket", got)
	}
	if res.ReqHeaders["Sec-Websocket-Key"] == nil && res.ReqHeaders["Sec-WebSocket-Key"] == nil {
		t.Fatalf("ReqHeaders missing key: %v", res.ReqHeaders)
	}
	if got := res.ReqHeaders["X-Probe"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("ReqHeaders X-Probe = %v", got)
	}
	if res.ResHeaders["Sec-Websocket-Accept"] == nil && res.ResHeaders["Sec-WebSocket-Accept"] == nil {
		t.Fatalf("ResHeaders missing accept: %v", res.ResHeaders)
	}
}
