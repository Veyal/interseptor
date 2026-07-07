package ios

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBuildProfileURL(t *testing.T) {
	t.Parallel()
	got := BuildProfileURL("http://192.168.1.5:9966", "192.168.1.5", 8080)
	want := "http://192.168.1.5:9966/api/ios/profile.mobileconfig?host=192.168.1.5&port=8080"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	if shellQuote("http://x") != "'http://x'" {
		t.Fatal(shellQuote("http://x"))
	}
	if shellQuote("it's") != "'it'\\''s'" {
		t.Fatal(shellQuote("it's"))
	}
}

func TestResolveSSHOpts(t *testing.T) {
	t.Parallel()
	_, err := resolveSSHOpts(SSHOpts{})
	if err == nil {
		t.Fatal("expected host error")
	}
	opts, err := resolveSSHOpts(SSHOpts{Host: "10.0.0.2", Password: "alpine"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.User != defaultSSHUser || opts.Port != defaultSSHPort {
		t.Fatalf("defaults: %+v", opts)
	}
}

func TestSSHAvailable(t *testing.T) {
	if !SSHAvailable() {
		t.Fatal("expected SSH client available")
	}
}

func TestTCPReachableInvalid(t *testing.T) {
	if TCPReachable("", 22) {
		t.Fatal("empty host should not be reachable")
	}
}

// TestRunSSHCommandTimesOutOnHang proves runSSHCommand no longer blocks
// forever once the SSH session is open but the remote command never
// finishes (device stuck, shell waiting on input): it must give up and
// return an error well before a real hang would. It connects a real
// ssh.Client to an in-process test server whose session handler accepts the
// "exec" request but never sends a reply or closes the channel — modeling a
// wedged remote command.
func TestRunSSHCommandTimesOutOnHang(t *testing.T) {
	client, cleanup := newHangingSSHServer(t)
	defer cleanup()

	origTimeout := sshCommandTimeout
	sshCommandTimeout = 500 * time.Millisecond
	defer func() { sshCommandTimeout = origTimeout }()

	start := time.Now()
	_, err := runSSHCommand(client, "sleep 300")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from hung remote command")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("runSSHCommand took %v, expected to return quickly after the timeout bound", elapsed)
	}
}

// newHangingSSHServer starts a minimal in-process SSH server that completes
// the handshake and accepts session channels, but never responds to any
// request on them (simulating a remote command that hangs indefinitely). It
// returns a connected *ssh.Client and a cleanup func.
func newHangingSSHServer(t *testing.T) (*ssh.Client, func()) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for newCh := range chans {
			if newCh.ChannelType() != "session" {
				_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			ch, requests, err := newCh.Accept()
			if err != nil {
				continue
			}
			// Deliberately never reply to exec/shell requests and never
			// close/write to the channel — this is the "hang" being tested.
			go func() {
				for range requests {
					// swallow requests silently; no reply, no exit-status
				}
			}()
			_ = ch
		}
		_ = sshConn.Close()
	}()

	addr := ln.Addr().String()
	clientCfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		ln.Close()
		t.Fatalf("client dial: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		_ = ln.Close()
		<-serverDone
	}
	return client, cleanup
}
