//go:build darwin

package sysproxy

import (
	"reflect"
	"strings"
	"testing"
)

// fakeNetworkSetup models networksetup's proxy state for a set of services.
type fakeNetworkSetup struct {
	services []string
	web      map[string]darwinProxy // "-getwebproxy" state per service
	secure   map[string]darwinProxy
	sets     [][]string
}

func (f *fakeNetworkSetup) run(args ...string) ([]byte, error) {
	show := func(p darwinProxy) []byte {
		en := "No"
		if p.enabled {
			en = "Yes"
		}
		port := p.port
		if port == "" {
			port = "0"
		}
		return []byte("Enabled: " + en + "\nServer: " + p.server + "\nPort: " + port + "\nAuthenticated Proxy Enabled: 0\n")
	}
	switch args[0] {
	case "-listallnetworkservices":
		return []byte("An asterisk (*) denotes that a network service is disabled.\n" + strings.Join(f.services, "\n") + "\n"), nil
	case "-getwebproxy":
		return show(f.web[args[1]]), nil
	case "-getsecurewebproxy":
		return show(f.secure[args[1]]), nil
	}
	f.sets = append(f.sets, append([]string(nil), args...))
	apply := func(m map[string]darwinProxy) {
		p := m[args[1]]
		switch {
		case strings.HasSuffix(args[0], "state"):
			p.enabled = args[2] == "on"
		default:
			p.server, p.port, p.enabled = args[2], args[3], true // networksetup turns it on
		}
		m[args[1]] = p
	}
	if strings.HasPrefix(args[0], "-setsecure") {
		apply(f.secure)
	} else {
		apply(f.web)
	}
	return nil, nil
}

func newFakeNetworkSetup(t *testing.T, services ...string) *fakeNetworkSetup {
	t.Helper()
	f := &fakeNetworkSetup{services: services, web: map[string]darwinProxy{}, secure: map[string]darwinProxy{}}
	resetDarwinState()
	t.Cleanup(resetDarwinState)
	stubNetworkSetup(t, f.run)
	return f
}

// A proxy the user had configured before (a corporate proxy, another tool) is
// put back when Interseptor turns its own off, instead of being switched off.
func TestDisableRestoresThePriorProxyOnMacOS(t *testing.T) {
	f := newFakeNetworkSetup(t, "Wi-Fi", "Ethernet")
	corp := darwinProxy{enabled: true, server: "proxy.example.com", port: "3128"}
	f.web["Wi-Fi"], f.secure["Wi-Fi"] = corp, corp
	// Ethernet: a server is filled in but the proxy is switched off.
	f.web["Ethernet"] = darwinProxy{server: "old.example.com", port: "8000"}

	if err := Enable("127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	ours := darwinProxy{enabled: true, server: "127.0.0.1", port: "8080"}
	for _, svc := range f.services {
		if f.web[svc] != ours || f.secure[svc] != ours {
			t.Fatalf("%s not pointed at Interseptor: web=%+v secure=%+v", svc, f.web[svc], f.secure[svc])
		}
	}
	// Enabling again (a port change) must not record our own proxy as "prior".
	if err := Enable("127.0.0.1", 8081); err != nil {
		t.Fatal(err)
	}

	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if f.web["Wi-Fi"] != corp || f.secure["Wi-Fi"] != corp {
		t.Errorf("Wi-Fi: web=%+v secure=%+v, want the prior proxy %+v back", f.web["Wi-Fi"], f.secure["Wi-Fi"], corp)
	}
	if got, want := f.web["Ethernet"], (darwinProxy{server: "old.example.com", port: "8000"}); got != want {
		t.Errorf("Ethernet web = %+v, want %+v (server kept, still off)", got, want)
	}
	if f.secure["Ethernet"].enabled {
		t.Errorf("Ethernet secure proxy left on: %+v", f.secure["Ethernet"])
	}

	// The record is used once: a later Disable with nothing recorded just turns off.
	f.sets = nil
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-setwebproxystate", "Wi-Fi", "off"}, {"-setsecurewebproxystate", "Wi-Fi", "off"},
		{"-setwebproxystate", "Ethernet", "off"}, {"-setsecurewebproxystate", "Ethernet", "off"},
	}
	if !reflect.DeepEqual(f.sets, want) {
		t.Errorf("second Disable:\ngot  %v\nwant %v", f.sets, want)
	}
}

// After a crash the OS proxy still points at Interseptor. The next run must not
// take that for the user's own setting and "restore" a dead port on exit.
func TestEnableDoesNotRecordInterseptorsOwnProxyAsPrior(t *testing.T) {
	f := newFakeNetworkSetup(t, "Wi-Fi")
	stale := darwinProxy{enabled: true, server: "127.0.0.1", port: "8080"}
	f.web["Wi-Fi"], f.secure["Wi-Fi"] = stale, stale
	if err := Enable("127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if f.web["Wi-Fi"].enabled || f.secure["Wi-Fi"].enabled {
		t.Fatalf("proxy left on at a dead port: web=%+v secure=%+v", f.web["Wi-Fi"], f.secure["Wi-Fi"])
	}
}

func TestParseDarwinProxy(t *testing.T) {
	got := parseDarwinProxy("Enabled: Yes\nServer: proxy.example.com\nPort: 3128\nAuthenticated Proxy Enabled: 0\n")
	if want := (darwinProxy{enabled: true, server: "proxy.example.com", port: "3128"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// "Authenticated Proxy Enabled: 1" is not the proxy's own Enabled line.
	got = parseDarwinProxy("Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 1\n")
	if want := (darwinProxy{}); got != want {
		t.Errorf("got %+v, want the zero value", got)
	}
}
