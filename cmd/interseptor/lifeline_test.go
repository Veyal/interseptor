package main

import (
	"io"
	"testing"
	"time"
)

func TestStdinLifelineDisabledWithoutEnv(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	for _, v := range []string{"", "0", "true"} {
		getenv := func(string) string { return v }
		if ch := stdinLifeline(getenv, r); ch != nil {
			t.Errorf("%s=%q: lifeline armed, want nil (never fires)", exitOnStdinEOFEnv, v)
		}
	}
}

func TestStdinLifelineFiresOnEOF(t *testing.T) {
	r, w := io.Pipe()
	getenv := func(k string) string {
		if k == exitOnStdinEOFEnv {
			return "1"
		}
		return ""
	}
	ch := stdinLifeline(getenv, r)
	if ch == nil {
		t.Fatal("lifeline not armed with the env var set")
	}
	// Data on stdin is drained and ignored; only EOF means the supervisor left.
	if _, err := w.Write([]byte("noise\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Fatal("fired while stdin was still open")
	case <-time.After(50 * time.Millisecond):
	}
	w.Close()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("did not fire after stdin reached EOF")
	}
}
