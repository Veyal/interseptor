package android

import "testing"

// `adb shell settings put global http_proxy <host:port>` is run by the device's
// shell, so a host carrying shell metacharacters would execute on the device.
// Only plain IPs and DNS hostnames may reach that command.
func TestEnableProxyWiFiRejectsShellMetacharacters(t *testing.T) {
	var calls int
	stubADB(t, func(args ...string) ([]byte, error) {
		calls++
		return nil, nil
	})
	dev := Device{Serial: "dev", State: "device"}
	for _, bad := range []string{"a;reboot", "x$(id)", "h`id`", "a b", "h|sh", "h&&id", "h>f", "'q'", "-h", "h.example.com.;", ""} {
		if err := EnableProxyWiFi(dev, bad, 8080); err == nil {
			t.Fatalf("host %q accepted", bad)
		}
	}
	if calls != 0 {
		t.Fatalf("adb ran %d times for rejected hosts", calls)
	}
	for _, ok := range []string{"192.168.1.10", "proxy.example.com", "my-laptop.local", "fd00::1"} {
		if err := EnableProxyWiFi(dev, ok, 8080); err != nil {
			t.Fatalf("host %q rejected: %v", ok, err)
		}
	}
}

func TestSetupWiFiRejectsUnsafeHost(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) {
		t.Fatalf("adb must not run: %v", args)
		return nil, nil
	})
	_, err := Setup(Device{Serial: "dev", State: "device"}, nil, SetupOpts{ProxyMode: "wifi", WiFiHost: "1.2.3.4;reboot", Port: 8080, CAMode: "user"})
	if err == nil {
		t.Fatal("unsafe wifi host accepted by Setup")
	}
}
