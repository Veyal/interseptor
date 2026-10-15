package testbrowser

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestBrowserArgs(t *testing.T) {
	got := BrowserArgs("127.0.0.1:8080", "/tmp/profile", "")
	want := []string{
		"--proxy-server=127.0.0.1:8080",
		"--proxy-bypass-list=<-loopback>",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
		"--ignore-certificate-errors",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BrowserArgs =\n%v\nwant\n%v", got, want)
	}
}

func TestBrowserArgsAppendsStartURL(t *testing.T) {
	got := BrowserArgs("127.0.0.1:8080", "/tmp/p", "https://example.com/")
	if got[len(got)-1] != "https://example.com/" {
		t.Fatalf("start URL not appended last: %v", got)
	}
}

// stub swaps the package seams for the duration of a test.
func stub(t *testing.T, resolve func() (string, error), dir func() (string, error), start func(string, []string) (int, error)) {
	t.Helper()
	or, od, os_ := resolveBrowser, profileDir, startDetached
	if resolve != nil {
		resolveBrowser = resolve
	}
	if dir != nil {
		profileDir = dir
	}
	if start != nil {
		startDetached = start
	}
	t.Cleanup(func() { resolveBrowser, profileDir, startDetached = or, od, os_ })
}

func TestLaunchNoBrowserReturnsHonestError(t *testing.T) {
	started := false
	stub(t,
		func() (string, error) { return "", errors.New("no headless Chrome found: set INTERSEPTOR_CHROME") },
		func() (string, error) { return "/tmp/p", nil },
		func(string, []string) (int, error) { started = true; return 0, nil },
	)
	_, err := Launch("127.0.0.1:8080")
	if err == nil {
		t.Fatal("Launch must fail when no browser is found")
	}
	if !strings.Contains(err.Error(), "INTERSEPTOR_CHROME") {
		t.Fatalf("error should carry the discovery hint, got: %v", err)
	}
	if started {
		t.Fatal("must not start a browser when discovery failed")
	}
	if Available() {
		t.Fatal("Available() should be false when discovery fails")
	}
}

func TestLaunchStartsBrowserWithProxyAndIsolatedProfile(t *testing.T) {
	var gotBin string
	var gotArgs []string
	stub(t,
		func() (string, error) { return "/fake/chrome", nil },
		func() (string, error) { return "/tmp/isolated-profile", nil },
		func(bin string, args []string) (int, error) { gotBin, gotArgs = bin, args; return 4242, nil },
	)
	res, err := Launch("127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if gotBin != "/fake/chrome" || res.Browser != "/fake/chrome" {
		t.Fatalf("browser = %q / %q, want /fake/chrome", gotBin, res.Browser)
	}
	if res.PID != 4242 {
		t.Fatalf("pid = %d, want 4242", res.PID)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--proxy-server=127.0.0.1:8080") {
		t.Fatalf("args missing proxy: %v", gotArgs)
	}
	if !strings.Contains(joined, "--user-data-dir=/tmp/isolated-profile") {
		t.Fatalf("args missing isolated profile: %v", gotArgs)
	}
	if res.ProfileDir != "/tmp/isolated-profile" {
		t.Fatalf("profileDir = %q", res.ProfileDir)
	}
}

func TestDefaultProfileDirIsIsolatedAndCreated(t *testing.T) {
	dir, err := defaultProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dir, "interseptor") || !strings.Contains(dir, "test-browser-profile") {
		t.Fatalf("profile dir is not clearly isolated: %q", dir)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("profile dir not created: %v", err)
	}
}
