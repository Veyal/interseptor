package sender

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestIPGuardCheck(t *testing.T) {
	g := &IPGuard{BlockPrivate: true, AllowHosts: []string{"Lab.Example.com"}, OwnPorts: []int{9966}}
	cases := []struct {
		name, host, ip string
		port           int
		blocked        bool
	}{
		{"public", "example.com", "93.184.216.34", 443, false},
		{"loopback", "example.com", "127.0.0.1", 80, true},
		{"metadata", "example.com", "169.254.169.254", 80, true},
		{"rfc1918", "example.com", "10.1.2.3", 80, true},
		{"cgnat tailnet", "example.com", "100.100.1.1", 80, true},
		{"ula v6", "example.com", "fd00:ec2::254", 80, true},
		{"mapped v4 loopback", "example.com", "::ffff:127.0.0.1", 80, true},
		{"unspecified", "example.com", "0.0.0.0", 80, true},
		{"allowed host private", "lab.example.com", "10.1.2.3", 80, false},
		{"allowed host own port", "lab.example.com", "127.0.0.1", 9966, true},
	}
	for _, c := range cases {
		err := g.Check(c.host, net.ParseIP(c.ip), c.port)
		if (err != nil) != c.blocked {
			t.Errorf("%s: err=%v blocked=%v", c.name, err, c.blocked)
		}
		if err != nil && !errors.Is(err, ErrGuardBlocked) {
			t.Errorf("%s: not ErrGuardBlocked: %v", c.name, err)
		}
	}
	if err := (*IPGuard)(nil).Check("h", net.ParseIP("127.0.0.1"), 1); err != nil {
		t.Fatalf("nil guard must allow: %v", err)
	}
}

func TestGuardedDialPinsAndBlocksRebind(t *testing.T) {
	g := &IPGuard{BlockPrivate: true}
	called := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, errors.New("dialed")
	}
	// A hostname overridden to loopback models a rebinding answer.
	_, err := guardedDial(context.Background(), g, map[string]string{"evil.example.com": "127.0.0.1"}, "tcp", "evil.example.com:80", dial)
	if !errors.Is(err, ErrGuardBlocked) || called {
		t.Fatalf("rebind to loopback must be blocked before dial: err=%v called=%v", err, called)
	}
	// Allowed host dials the pinned IP, not the name.
	g.AllowHosts = []string{"evil.example.com"}
	var got string
	dial = func(_ context.Context, _, a string) (net.Conn, error) { got = a; return nil, errors.New("x") }
	_, _ = guardedDial(context.Background(), g, map[string]string{"evil.example.com": "127.0.0.1"}, "tcp", "evil.example.com:8081", dial)
	if got != "127.0.0.1:8081" {
		t.Fatalf("dial addr = %q; want pinned IP", got)
	}
}

// Review regression: names the local resolver cannot find but another resolver
// maps to loopback (127.1, 2130706433, foo.localhost) must never reach one of our
// own ports through a proxy, whatever the private-address policy says.
func TestUnverifiableHostRefusesOwnPorts(t *testing.T) {
	g := &IPGuard{OwnPorts: []int{9966}}
	if err := unverifiableHost(g, "127.1", 9966); err == nil || !strings.Contains(err.Error(), "own listener") {
		t.Fatalf("an unresolvable name on an own port must be refused, got %v", err)
	}
	if err := unverifiableHost(g, "target.invalid", 443); err != nil {
		t.Fatalf("an unresolvable name on another port is left to the upstream: %v", err)
	}
	if err := unverifiableHost(&IPGuard{BlockPrivate: true}, "target.invalid", 443); err == nil {
		t.Fatal("a private-blocking guard must refuse an unverifiable host")
	}
	if err := unverifiableHost(nil, "x.invalid", 9966); err != nil {
		t.Fatalf("no guard, no refusal: %v", err)
	}
}

// Only socks5h resolves names at the proxy; plain socks5 resolves locally again,
// so it must not receive an unverified name.
func TestRemoteResolveOnlyForSOCKS5H(t *testing.T) {
	for raw, want := range map[string]bool{"socks5h://127.0.0.1:1080": true, "socks5://127.0.0.1:1080": false, "http://127.0.0.1:8080": false} {
		u, _ := url.Parse(raw)
		if got := resolvesRemotely(u); got != want {
			t.Errorf("resolvesRemotely(%s) = %v, want %v", raw, got, want)
		}
	}
	if resolvesRemotely(nil) {
		t.Error("no upstream never resolves remotely")
	}
}
