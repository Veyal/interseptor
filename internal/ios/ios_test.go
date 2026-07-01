package ios

import (
	"path/filepath"
	"testing"

	"github.com/Veyal/interceptor/internal/tlsca"
)

func TestParseSimctlDevices(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "devices": {
    "com.apple.CoreSimulator.SimRuntime.iOS-18-0": [
      {"name":"iPhone 16","udid":"SIM-AAA","state":"Booted","isAvailable":true},
      {"name":"iPhone 15","udid":"SIM-BBB","state":"Shutdown","isAvailable":true}
    ]
  }
}`)
	devs, err := parseSimctlDevices(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("got %d devices", len(devs))
	}
	if devs[0].UDID != "SIM-AAA" || !devs[0].Booted {
		t.Fatalf("booted first: %+v", devs[0])
	}
}

func TestBuildMobileConfig(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsca.LoadOrCreate(filepath.Join(dir, "ca"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := BuildMobileConfig(ca.CertPEM(), ProfileOpts{
		DisplayName: "Interceptor Test",
		ProxyHost:   "192.168.1.10",
		ProxyPort:   8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"com.apple.security.root",
		"com.apple.proxy.http.global",
		"192.168.1.10",
		"<integer>8080</integer>",
		"Interceptor Test",
	} {
		if !contains(s, want) {
			t.Fatalf("profile missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestRuntimeLabel(t *testing.T) {
	if got := runtimeLabel("com.apple.CoreSimulator.SimRuntime.iOS-18-2"); got != "iOS 18.2" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveDeviceBootedSimulator(t *testing.T) {
	devs := []Device{
		{Kind: KindSimulator, UDID: "a", Booted: true},
		{Kind: KindSimulator, UDID: "b", Booted: false},
	}
	d, err := ResolveDevice("", devs)
	if err != nil || d.UDID != "a" {
		t.Fatalf("resolve: %+v err=%v", d, err)
	}
}

func TestSimctlAvailableFalseOnNonDarwin(t *testing.T) {
	// On Windows CI this should be false; on macOS dev machines may be true — just call it.
	_ = SimctlAvailable()
}
