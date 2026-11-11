package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The app starts the server with the resume fallback, and relays the server's
// startup notice to the shell once the UI is ready.
func TestShellModeRequestsResumeFallbackAndRelaysNotice(t *testing.T) {
	srv := fakeControlPlane(t)
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env")
	bin := filepath.Join(dir, serverBinaryName)
	script := "#!/bin/sh\n" +
		"trap 'exit 0' TERM\n" +
		"echo \"$INTERSEPTOR_RESUME_FALLBACK\" > " + envOut + "\n" +
		"echo \"2026/01/02 03:04:05 startup notice: The project folder /Volumes/x/acme is not available, so the default project was opened.\"\n" +
		"echo \"Interseptor vX · UI on " + srv.URL + " · data /x\"\n" +
		"while :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &syncBuffer{}
	parentGone := make(chan struct{})
	pw := newProtocolWriter(out)
	l := &launcher{
		bin:          bin,
		url:          "http://127.0.0.1:1",
		client:       srv.Client(),
		log:          io.Discard,
		logName:      "test.log",
		signals:      make(chan os.Signal),
		parentGone:   parentGone,
		onReady:      pw.ready,
		onNotice:     pw.notice,
		readyTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- l.run() }()

	want := "INTERSEPTOR_NOTICE The project folder /Volumes/x/acme is not available, so the default project was opened.\n"
	waitFor(t, func() bool { return strings.Contains(out.String(), want) })
	got := out.String()
	if r, n := strings.Index(got, readyPrefix), strings.Index(got, noticePrefix); r < 0 || n < r {
		t.Errorf("the notice must follow READY (the shell has a window by then), got %q", got)
	}
	close(parentGone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after the parent went away")
	}
	env, err := os.ReadFile(envOut)
	if err != nil || strings.TrimSpace(string(env)) != "default" {
		t.Fatalf("child INTERSEPTOR_RESUME_FALLBACK = %q (%v), want default", env, err)
	}
}

func TestNoticeSnifferKeepsOnlyNoticeLines(t *testing.T) {
	s := &noticeSniffer{}
	io.WriteString(s, "2026/01/02 03:04:05 proxy on 127.0.0.1:8080\n2026/01/02 03:04:05 startup no")
	io.WriteString(s, "tice: first\npartial startup notice: never finished")
	if got := s.take(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("take() = %q, want [first]", got)
	}
	if got := s.take(); len(got) != 0 {
		t.Fatalf("a notice was delivered twice: %q", got)
	}
	// A child that never ends a line cannot grow the buffer without bound.
	io.WriteString(s, strings.Repeat("x", 4*sniffBufferMax))
	if n := s.buffered(); n > sniffBufferMax {
		t.Fatalf("buffered %d bytes, bound is %d", n, sniffBufferMax)
	}
}

func TestNoticeLineIsOneLine(t *testing.T) {
	if got := noticeLine("a\nINTERSEPTOR_READY http://evil.example.com\n b"); got != "INTERSEPTOR_NOTICE a INTERSEPTOR_READY http://evil.example.com b\n" {
		t.Fatalf("noticeLine = %q", got)
	}
}
