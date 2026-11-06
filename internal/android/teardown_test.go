package android

import (
	"errors"
	"strings"
	"testing"
)

// Teardown exists to put the device back the way it was. DisableProxy used to
// swallow every adb failure and return nil, which made Teardown's error branch
// dead code: an unplugged device (or a wedged adbd) still produced "Device proxy
// cleared" and left the global proxy pointed at a possibly-dead proxy. Clearing
// the proxy must be reported; removing the reverse listener stays best-effort
// because adb errors when there is no such listener.
func TestTeardownReportsFailureToClearProxy(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		for _, a := range args {
			if a == "http_proxy" {
				return []byte("error: device offline"), errors.New("exit status 1")
			}
		}
		return nil, nil
	})
	res, err := Teardown(Device{Serial: "dev", State: "device"}, 8080, nil, false)
	if err == nil {
		t.Fatalf("Teardown reported success although clearing the proxy failed: %#v", res)
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("error should say the proxy could not be cleared: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("teardown ran no adb command")
	}
}

// A missing reverse listener is normal (nothing to remove) and must not fail the
// teardown once the proxy itself was cleared.
func TestTeardownToleratesMissingReverseListener(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "--remove" {
				return []byte("error: listener 'tcp:8080' not found"), errors.New("exit status 1")
			}
		}
		return nil, nil
	})
	res, err := Teardown(Device{Serial: "dev", State: "device"}, 8080, nil, false)
	if err != nil {
		t.Fatalf("Teardown failed because there was no reverse listener to remove: %v", err)
	}
	if res.Message == "" {
		t.Fatalf("teardown result lost its message: %#v", res)
	}
}
