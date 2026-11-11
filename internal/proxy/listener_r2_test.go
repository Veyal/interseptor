package proxy

import (
	"net/http"
	"testing"
)

// Server.Serve must bound the request head and the keep-alive idle wait, and
// must not set the whole-request timeouts that would cut slow bodies, long
// polls or streamed responses. (From 457abea1, whose test a merge deleted.)
func TestServeListenerBoundsRequestHeadAndIdle(t *testing.T) {
	hs := newListenerServer(http.NotFoundHandler())
	if hs.ReadHeaderTimeout <= 0 || hs.IdleTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout=%v IdleTimeout=%v, want both bounded", hs.ReadHeaderTimeout, hs.IdleTimeout)
	}
	if hs.ReadTimeout != 0 || hs.WriteTimeout != 0 {
		t.Fatalf("ReadTimeout=%v WriteTimeout=%v must stay unset", hs.ReadTimeout, hs.WriteTimeout)
	}
}
