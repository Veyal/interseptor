package wsrepeater

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// CheckDial sees the resolved IP, so a hostname ("localhost") pointing at a
// refused destination is stopped before the connection is made.
func TestCheckDialRefusesResolvedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	var seen net.IP
	refuse := errors.New("refused by guard")
	_, err = Send(Request{URL: "ws://localhost:" + port + "/", Message: "x", CheckDial: func(ip net.IP, _ int) error {
		seen = ip
		return refuse
	}})
	if err == nil || !strings.Contains(err.Error(), "refused by guard") {
		t.Fatalf("err = %v, want guard refusal", err)
	}
	if seen == nil || !seen.IsLoopback() {
		t.Fatalf("CheckDial saw %v, want the resolved loopback IP", seen)
	}
	select {
	case <-accepted:
		t.Fatal("connection was made despite the refusal")
	default:
	}
}
