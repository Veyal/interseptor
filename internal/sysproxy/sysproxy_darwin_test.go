//go:build darwin

package sysproxy

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// ── parseServices (pure) ──────────────────────────────────────────────────────

func TestParseServicesSkipsHeaderAndDisabled(t *testing.T) {
	out := "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Thunderbolt Bridge\nUSB 10/100/1000 LAN\n"
	got := parseServices(out)
	want := []string{"Wi-Fi", "USB 10/100/1000 LAN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices = %v, want %v", got, want)
	}
}

func TestParseServicesEmpty(t *testing.T) {
	if got := parseServices("An asterisk...\n"); len(got) != 0 {
		t.Fatalf("expected no services, got %v", got)
	}
}

func TestParseServicesAllDisabled(t *testing.T) {
	out := "Header line\n*Wi-Fi\n*Ethernet\n"
	got := parseServices(out)
	if len(got) != 0 {
		t.Fatalf("expected no enabled services when all are disabled, got %v", got)
	}
}

func TestParseServicesHeaderOnly(t *testing.T) {
	got := parseServices("Header line only, no newline")
	if len(got) != 0 {
		t.Fatalf("expected empty result for header-only input, got %v", got)
	}
}

func TestParseServicesWindowsLineEndings(t *testing.T) {
	out := "Header\r\nWi-Fi\r\n*Ethernet\r\nUSB LAN\r\n"
	got := parseServices(out)
	want := []string{"Wi-Fi", "USB LAN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices CRLF = %v, want %v", got, want)
	}
}

func TestParseServicesBlankLinesSkipped(t *testing.T) {
	out := "Header\nWi-Fi\n\nEthernet\n"
	got := parseServices(out)
	want := []string{"Wi-Fi", "Ethernet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices blank lines = %v, want %v", got, want)
	}
}

func TestParseServicesSingleEnabled(t *testing.T) {
	out := "Header\nWi-Fi\n"
	got := parseServices(out)
	want := []string{"Wi-Fi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices single = %v, want %v", got, want)
	}
}

func TestParseServicesServiceNameWithSpaces(t *testing.T) {
	out := "Header\nUSB 10/100/1000 LAN\n"
	got := parseServices(out)
	want := []string{"USB 10/100/1000 LAN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices spaces = %v, want %v", got, want)
	}
}

// ── Seam-based tests (inject a fake networkSetup) ─────────────────────────────

func stubNetworkSetup(t *testing.T, fn func(args ...string) ([]byte, error)) {
	t.Helper()
	orig := networkSetup
	networkSetup = fn
	t.Cleanup(func() { networkSetup = orig })
}

func TestRunSuccess(t *testing.T) {
	var capturedArgs []string
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte(""), nil
	})
	if err := run("-setwebproxy", "Wi-Fi", "127.0.0.1", "8080"); err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}
	want := []string{"-setwebproxy", "Wi-Fi", "127.0.0.1", "8080"}
	if !reflect.DeepEqual(capturedArgs, want) {
		t.Fatalf("run passed args %v, want %v", capturedArgs, want)
	}
}

func TestRunError(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		return []byte("command not found"), errors.New("exit status 1")
	})
	err := run("-setwebproxy", "Wi-Fi", "127.0.0.1", "8080")
	if err == nil {
		t.Fatal("run should propagate command errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "networksetup") {
		t.Errorf("error should mention networksetup, got: %s", msg)
	}
	if !strings.Contains(msg, "command not found") {
		t.Errorf("error should include stderr, got: %s", msg)
	}
	if !strings.Contains(msg, "-setwebproxy") {
		t.Errorf("error should include the subcommand, got: %s", msg)
	}
}

func TestRunErrorTrimsTrailingWhitespace(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		return []byte("  some error  \n"), errors.New("exit status 2")
	})
	err := run("-setwebproxy", "s", "h", "p")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.HasSuffix(err.Error(), "\n") {
		t.Errorf("error message has trailing newline: %q", err.Error())
	}
}

func TestActiveServicesSuccess(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "-listallnetworkservices" {
			return []byte("An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Ethernet\nUSB LAN\n"), nil
		}
		return nil, fmt.Errorf("unexpected args: %v", args)
	})
	svcs, err := activeServices()
	if err != nil {
		t.Fatalf("activeServices error: %v", err)
	}
	want := []string{"Wi-Fi", "USB LAN"}
	if !reflect.DeepEqual(svcs, want) {
		t.Fatalf("activeServices = %v, want %v", svcs, want)
	}
}

func TestActiveServicesCommandFailure(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("networksetup not found")
	})
	_, err := activeServices()
	if err == nil {
		t.Fatal("activeServices should propagate command errors")
	}
}

func TestStatusOutputParsingEnabled(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		switch {
		case len(args) == 1 && args[0] == "-listallnetworkservices":
			return []byte("Header\nWi-Fi\n"), nil
		case len(args) == 2 && args[0] == "-getwebproxy":
			return []byte("Enabled: Yes\nServer: 127.0.0.1\nPort: 8080\n"), nil
		}
		return nil, fmt.Errorf("unexpected args: %v", args)
	})
	enabled, err := Status()
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if !enabled {
		t.Fatal("Status should return true when output contains 'Enabled: Yes'")
	}
}

func TestStatusOutputParsingDisabled(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		switch {
		case len(args) == 1 && args[0] == "-listallnetworkservices":
			return []byte("Header\nWi-Fi\n"), nil
		case len(args) == 2 && args[0] == "-getwebproxy":
			return []byte("Enabled: No\nServer: \nPort: 0\n"), nil
		}
		return nil, fmt.Errorf("unexpected args: %v", args)
	})
	enabled, err := Status()
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if enabled {
		t.Fatal("Status should return false when output contains 'Enabled: No'")
	}
}

func TestSupportedOnDarwin(t *testing.T) {
	if !Supported() {
		t.Fatal("Supported() must be true on darwin")
	}
}

// ── macOS integration paths via the stub ──────────────────────────────────────

func TestEnableMacOSBuildsCorrectCommands(t *testing.T) {
	var calls [][]string
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\nWi-Fi\n"), nil
		}
		return []byte(""), nil
	})

	if err := Enable("127.0.0.1", 8080); err != nil {
		t.Fatalf("Enable error: %v", err)
	}

	wantCalls := [][]string{
		{"-listallnetworkservices"},
		{"-setwebproxy", "Wi-Fi", "127.0.0.1", "8080"},
		{"-setsecurewebproxy", "Wi-Fi", "127.0.0.1", "8080"},
		{"-setwebproxystate", "Wi-Fi", "on"},
		{"-setsecurewebproxystate", "Wi-Fi", "on"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("Enable call sequence:\ngot  %v\nwant %v", calls, wantCalls)
	}
}

func TestDisableMacOSBuildsCorrectCommands(t *testing.T) {
	var calls [][]string
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\nWi-Fi\nEthernet\n"), nil
		}
		return []byte(""), nil
	})

	if err := Disable(); err != nil {
		t.Fatalf("Disable error: %v", err)
	}

	wantCalls := [][]string{
		{"-listallnetworkservices"},
		{"-setwebproxystate", "Wi-Fi", "off"},
		{"-setsecurewebproxystate", "Wi-Fi", "off"},
		{"-setwebproxystate", "Ethernet", "off"},
		{"-setsecurewebproxystate", "Ethernet", "off"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("Disable call sequence:\ngot  %v\nwant %v", calls, wantCalls)
	}
}

func TestDisableMacOSSwallowsErrors(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\nWi-Fi\n"), nil
		}
		return []byte("error"), errors.New("exit status 1")
	})

	if err := Disable(); err != nil {
		t.Fatalf("Disable must swallow errors from set*proxystate, got: %v", err)
	}
}

func TestEnableMacOSPropagatesRunError(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\nWi-Fi\n"), nil
		}
		return []byte("permission denied"), errors.New("exit status 1")
	})

	if err := Enable("127.0.0.1", 8080); err == nil {
		t.Fatal("Enable should propagate networksetup errors")
	}
}

func TestEnableMacOSListFailure(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("networksetup unavailable")
	})

	if err := Enable("127.0.0.1", 8080); err == nil {
		t.Fatal("Enable should propagate activeServices errors")
	}
}

func TestStatusMacOSEmptyServices(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\n"), nil
		}
		return nil, fmt.Errorf("should not be called")
	})

	enabled, err := Status()
	if err != nil {
		t.Fatalf("Status with no services should not error, got: %v", err)
	}
	if enabled {
		t.Fatal("Status with no services should return false")
	}
}

func TestStatusMacOSGetWebProxyError(t *testing.T) {
	stubNetworkSetup(t, func(args ...string) ([]byte, error) {
		if args[0] == "-listallnetworkservices" {
			return []byte("Header\nWi-Fi\n"), nil
		}
		return nil, errors.New("getwebproxy failed")
	})

	_, err := Status()
	if err == nil {
		t.Fatal("Status should propagate getwebproxy command errors")
	}
}
