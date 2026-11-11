//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// Closing the terminal sends SIGHUP. Unhandled, it kills the process without
// the shutdown path, and a system proxy Interseptor enabled stays pointed at a
// dead port. It must arrive on the same channel as SIGTERM.
func TestShutdownSignalsIncludeHangup(t *testing.T) {
	has := func(sigs []os.Signal, want os.Signal) bool {
		for _, s := range sigs {
			if s == want {
				return true
			}
		}
		return false
	}
	sigs := shutdownSignals(func(os.Signal) bool { return false })
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !has(sigs, want) {
			t.Errorf("shutdown signals %v do not include %v", sigs, want)
		}
	}
	// Under nohup SIGHUP is ignored on purpose. Notify would re-enable it and
	// the process would stop when the terminal closes, which nohup exists to prevent.
	sigs = shutdownSignals(func(s os.Signal) bool { return s == syscall.SIGHUP })
	if has(sigs, syscall.SIGHUP) {
		t.Error("SIGHUP is handled although the process was started with it ignored (nohup)")
	}
	if !has(sigs, syscall.SIGTERM) {
		t.Error("SIGTERM dropped")
	}
}

func TestHangupReachesTheShutdownChannel(t *testing.T) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, shutdownSignals(func(os.Signal) bool { return false })...)
	defer signal.Stop(stop)
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-stop:
		if got != syscall.SIGHUP {
			t.Fatalf("got %v, want SIGHUP", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not reach the shutdown channel")
	}
}
