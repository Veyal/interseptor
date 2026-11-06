package android

import (
	"strings"
	"testing"
)

// Setup is a one-click device reconfiguration: it changes the global proxy and
// installs a CA. An invalid mode must therefore be rejected *before* the first
// adb command, otherwise the device is left pointing at a proxy with no CA
// installed (the exact state a failed setup must not create).

func TestSetupRejectsUnknownCAModeBeforeTouchingDevice(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	_, err := Setup(Device{Serial: "dev", State: "device"}, testCAPEM(t), SetupOpts{ProxyMode: "usb", CAMode: "sysytem", Port: 8080})
	if err == nil || !strings.Contains(err.Error(), "sysytem") {
		t.Fatalf("expected an unknown-ca-mode error naming the value, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("Setup ran %d adb command(s) before rejecting the ca mode: %v", len(calls), calls)
	}
}

func TestSetupRejectsUnknownProxyMode(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	_, err := Setup(Device{Serial: "dev", State: "device"}, testCAPEM(t), SetupOpts{ProxyMode: "wiif", CAMode: "user", Port: 8080})
	if err == nil || !strings.Contains(err.Error(), "wiif") {
		t.Fatalf("expected an unknown-proxy-mode error naming the value, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("Setup ran %d adb command(s) for an unknown proxy mode: %v", len(calls), calls)
	}
}

// The documented modes still work.
func TestSetupAcceptedModesStillWork(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	for _, mode := range []string{"", "usb", "USB", " wifi "} {
		calls = nil
		opts := SetupOpts{ProxyMode: mode, CAMode: "auto", WiFiHost: "192.168.1.10", Port: 8080}
		if _, err := Setup(Device{Serial: "emulator-5554", State: "device"}, testCAPEM(t), opts); err != nil {
			t.Fatalf("proxy mode %q rejected: %v", mode, err)
		}
		if len(calls) == 0 {
			t.Fatalf("proxy mode %q ran no adb commands", mode)
		}
	}
}
