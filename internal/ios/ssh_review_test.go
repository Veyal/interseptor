package ios

import (
	"net"
	"testing"
	"time"
)

// A host that accepts the TCP connection but never speaks SSH (a wrong port, a
// wedged sshd, a tarpit) must not hang the dial — and the HTTP handler behind
// it — forever: the connect timeout has to cover the handshake too.
func TestDialSSHBoundsTheHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c // accept and stay silent
		}
	}()
	defer func() {
		for {
			select {
			case c := <-held:
				_ = c.Close()
			default:
				return
			}
		}
	}()

	orig := sshConnectTimeout
	sshConnectTimeout = 300 * time.Millisecond
	defer func() { sshConnectTimeout = orig }()

	addr := ln.Addr().(*net.TCPAddr)
	done := make(chan error, 1)
	go func() {
		client, err := dialSSH(SSHOpts{Host: "127.0.0.1", Port: addr.Port, Password: "x"})
		if client != nil {
			_ = client.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("dial to a silent peer succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialSSH is still blocked on a peer that never sent an SSH banner")
	}
}
