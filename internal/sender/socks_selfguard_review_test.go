package sender

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

// The own-listener refusal runs in the net.Dialer Control hook, which only
// sees the address this process connects to. Through a SOCKS upstream that is
// the proxy, so a name resolving to loopback on one of the tool's own ports
// went straight through (the HTTP-proxy hop already vets this in Proxy()).
func TestSelfGuardRefusesOwnListenerThroughSOCKSUpstream(t *testing.T) {
	s, own, hits := newSelfGuardSender(t)
	_, port, _ := net.SplitHostPort(own.Listener.Addr().String())
	socksAddr, _ := newSenderSOCKS5Relay(t)
	if err := s.SetUpstreamProxy("socks5h://" + socksAddr); err != nil {
		t.Fatal(err)
	}
	f, err := s.Send(Request{Method: "POST", URL: "http://localhost.:" + port + "/api/keys"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status == http.StatusCreated || !strings.Contains(f.Error, "own listener") {
		t.Errorf("status=%d err=%q, want own-listener refusal", f.Status, f.Error)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("own listener received %d request(s) through the SOCKS upstream", n)
	}
}

// Other destinations still go through the SOCKS upstream untouched.
func TestSOCKSUpstreamStillReachesOtherPorts(t *testing.T) {
	s, _, _ := newSelfGuardSender(t)
	socksAddr, destinations := newSenderSOCKS5Relay(t)
	if err := s.SetUpstreamProxy("socks5h://" + socksAddr); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Send(Request{URL: "http://target.invalid/api"})
	select {
	case got := <-destinations:
		if got != "target.invalid:80" {
			t.Fatalf("SOCKS destination = %q", got)
		}
	default:
		t.Fatal("send never reached the SOCKS proxy")
	}
}
