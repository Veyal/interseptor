//go:build darwin

package sysproxy

import (
	"errors"
	"testing"
)

// The status is what the Settings toggle shows. A proxy enabled on Wi-Fi alone
// used to read "off" whenever another service (Ethernet, a dock) was listed
// first, although traffic was going through the proxy.
func TestR3StatusIsOnWhenAnyServiceHasTheProxyOn(t *testing.T) {
	ours := darwinProxy{enabled: true, server: "127.0.0.1", port: "8080"}
	cases := []struct {
		name     string
		services []string
		web      map[string]darwinProxy
		want     bool
	}{
		{"second service only", []string{"Ethernet", "Wi-Fi"}, map[string]darwinProxy{"Wi-Fi": ours}, true},
		{"first service only", []string{"Ethernet", "Wi-Fi"}, map[string]darwinProxy{"Ethernet": ours}, true},
		{"third of three", []string{"USB LAN", "Ethernet", "Wi-Fi"}, map[string]darwinProxy{"Wi-Fi": ours}, true},
		{"none", []string{"Ethernet", "Wi-Fi"}, map[string]darwinProxy{}, false},
		{"server filled in but switched off", []string{"Ethernet", "Wi-Fi"}, map[string]darwinProxy{"Wi-Fi": {server: "127.0.0.1", port: "8080"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeNetworkSetup(t, tc.services...)
			for svc, p := range tc.web {
				f.web[svc] = p
			}
			got, err := Status()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Status = %v, want %v", got, tc.want)
			}
		})
	}
}

// After Enable the status is on, after Disable off, whatever the service order.
func TestR3StatusFollowsEnableAndDisable(t *testing.T) {
	newFakeNetworkSetup(t, "Ethernet", "Wi-Fi")
	if err := Enable("127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if on, err := Status(); err != nil || !on {
		t.Fatalf("after Enable: %v %v", on, err)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if on, err := Status(); err != nil || on {
		t.Fatalf("after Disable: %v %v", on, err)
	}
}

// A service that cannot be read does not hide one that has the proxy on; when
// no service is on, the read error is still reported.
func TestR3StatusWithAnUnreadableService(t *testing.T) {
	f := newFakeNetworkSetup(t, "Broken", "Wi-Fi")
	f.web["Wi-Fi"] = darwinProxy{enabled: true, server: "127.0.0.1", port: "8080"}
	inner := networkSetup
	networkSetup = func(args ...string) ([]byte, error) {
		if len(args) == 2 && args[1] == "Broken" {
			return []byte("not a recognized network service"), errors.New("exit status 4")
		}
		return inner(args...)
	}
	if on, err := Status(); err != nil || !on {
		t.Fatalf("Status = %v, %v; want on from Wi-Fi", on, err)
	}
	f.web["Wi-Fi"] = darwinProxy{}
	if on, err := Status(); err == nil || on {
		t.Fatalf("Status = %v, %v; want the read error when nothing is on", on, err)
	}
}
