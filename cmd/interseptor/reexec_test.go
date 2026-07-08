package main

import (
	"net"
	"testing"
	"time"
)

func TestListenRetryBindsFreePort(t *testing.T) {
	t.Setenv("INTERSEPTOR_REEXEC", "") // no retry window — must bind immediately
	ln, err := listenRetry("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenRetry on a free port: %v", err)
	}
	ln.Close()
}

func TestListenRetryFailsFastWhenTaken(t *testing.T) {
	t.Setenv("INTERSEPTOR_REEXEC", "") // not a re-exec → single attempt, fail fast
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if ln, err := listenRetry(held.Addr().String()); err == nil {
		ln.Close()
		t.Fatal("expected listenRetry to fail fast on an occupied port without INTERSEPTOR_REEXEC")
	}
}

func TestListenRetryWaitsForRelease(t *testing.T) {
	t.Setenv("INTERSEPTOR_REEXEC", "1") // simulate a project-switch handoff
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := held.Addr().String()
	// Release the port shortly after, mimicking the predecessor exiting.
	go func() { time.Sleep(200 * time.Millisecond); held.Close() }()
	ln, err := listenRetry(addr)
	if err != nil {
		t.Fatalf("listenRetry should bind once the predecessor releases the port: %v", err)
	}
	ln.Close()
}
