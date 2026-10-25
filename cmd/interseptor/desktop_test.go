package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/desktopinstall"
)

type stubDesktopInstaller struct {
	got desktopinstall.Options
	res desktopinstall.Result
	err error
}

func (s *stubDesktopInstaller) Install(_ context.Context, o desktopinstall.Options) (desktopinstall.Result, error) {
	s.got = o
	return s.res, s.err
}

func withDesktopStubs(t *testing.T, stub *stubDesktopInstaller) (*bytes.Buffer, *[]string) {
	t.Helper()
	oldNew, oldOpen, oldOut := newDesktopInstaller, openDesktopApp, desktopOut
	t.Cleanup(func() { newDesktopInstaller, openDesktopApp, desktopOut = oldNew, oldOpen, oldOut })
	out := &bytes.Buffer{}
	var opened []string
	desktopOut = out
	newDesktopInstaller = func(io.Writer) desktopInstaller { return stub }
	openDesktopApp = func(p string) error { opened = append(opened, p); return nil }
	return out, &opened
}

func TestRunInstallDesktopPassesFlagsAndPrintsOpenHint(t *testing.T) {
	stub := &stubDesktopInstaller{res: desktopinstall.Result{Path: "/Applications/Interseptor.app", Version: "2.19.0", Installed: true}}
	out, opened := withDesktopStubs(t, stub)
	if err := runInstallDesktop([]string{"--dir", "/Applications", "--version", "2.19.0", "--force"}); err != nil {
		t.Fatal(err)
	}
	if stub.got != (desktopinstall.Options{Dir: "/Applications", Version: "2.19.0", Force: true}) {
		t.Fatalf("options = %+v", stub.got)
	}
	if len(*opened) != 0 {
		t.Fatal("app must not open without --open")
	}
	if !strings.Contains(out.String(), "open /Applications/Interseptor.app") {
		t.Fatalf("missing open hint: %s", out)
	}
}

func TestRunInstallDesktopOpenFlagLaunchesApp(t *testing.T) {
	stub := &stubDesktopInstaller{res: desktopinstall.Result{Path: "/x/Interseptor.app", Version: "2.19.0"}}
	_, opened := withDesktopStubs(t, stub)
	if err := runInstallDesktop([]string{"--open"}); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 || (*opened)[0] != "/x/Interseptor.app" {
		t.Fatalf("opened = %v", *opened)
	}
}

func TestRunInstallDesktopPropagatesErrors(t *testing.T) {
	stub := &stubDesktopInstaller{err: desktopinstall.ErrUnsupportedOS}
	_, opened := withDesktopStubs(t, stub)
	err := runInstallDesktop([]string{"--open"})
	if !errors.Is(err, desktopinstall.ErrUnsupportedOS) {
		t.Fatalf("err = %v", err)
	}
	if len(*opened) != 0 {
		t.Fatal("must not open after a failure")
	}
}

func TestInstallDesktopIsKnownCommand(t *testing.T) {
	if !knownTopLevelCommand("install-desktop") {
		t.Fatal("install-desktop should be a known top-level command")
	}
}
