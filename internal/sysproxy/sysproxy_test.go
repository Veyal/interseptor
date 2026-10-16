package sysproxy

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// ── Dispatch + unsupported fallback (run on every OS) ─────────────────────────

func TestSupportedReportsThisPlatform(t *testing.T) {
	got := Supported()
	switch runtime.GOOS {
	case "darwin", "windows":
		if !got {
			t.Fatalf("Supported() = false on %s, want true", runtime.GOOS)
		}
	case "linux":
		// Depends on whether gsettings is installed; just ensure it answers safely.
		_ = got
	default:
		if got {
			t.Fatalf("Supported() = true on unsupported OS %s", runtime.GOOS)
		}
	}
}

func TestUnsupportedBackendEnableMentionsHostPort(t *testing.T) {
	b := unsupportedBackend{}
	if b.supported() {
		t.Fatal("unsupported backend must report supported()=false")
	}
	err := b.enable("127.0.0.1", 8080)
	if err == nil {
		t.Fatal("unsupported enable must return an error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") || !strings.Contains(err.Error(), "8080") {
		t.Fatalf("manual hint must name host and port, got: %s", err.Error())
	}
}

func TestUnsupportedBackendDisableStatusAreNoOps(t *testing.T) {
	b := unsupportedBackend{}
	if err := b.disable(); err != nil {
		t.Fatalf("unsupported disable should be a no-op, got %v", err)
	}
	on, err := b.status()
	if on || err != nil {
		t.Fatalf("unsupported status = (%v, %v), want (false, nil)", on, err)
	}
}

// ── Windows registry logic (fake registry, runs on every OS) ──────────────────

type fakeWinReg struct {
	strs      map[string]string
	dwords    map[string]uint32
	ops       []string
	refreshed int
}

func newFakeWinReg() *fakeWinReg {
	return &fakeWinReg{strs: map[string]string{}, dwords: map[string]uint32{}}
}

func (f *fakeWinReg) getString(name string) (string, bool, error) {
	v, ok := f.strs[name]
	return v, ok, nil
}
func (f *fakeWinReg) getDWord(name string) (uint32, bool, error) {
	v, ok := f.dwords[name]
	return v, ok, nil
}
func (f *fakeWinReg) setString(name, val string) error {
	f.strs[name] = val
	f.ops = append(f.ops, "setStr:"+name+"="+val)
	return nil
}
func (f *fakeWinReg) setDWord(name string, val uint32) error {
	f.dwords[name] = val
	f.ops = append(f.ops, fmt.Sprintf("setDword:%s=%d", name, val))
	return nil
}
func (f *fakeWinReg) deleteValue(name string) error {
	delete(f.strs, name)
	delete(f.dwords, name)
	f.ops = append(f.ops, "del:"+name)
	return nil
}
func (f *fakeWinReg) refresh() error { f.refreshed++; return nil }

func TestWinProxyServerShape(t *testing.T) {
	if got := winProxyServer("127.0.0.1", 8080); got != "127.0.0.1:8080" {
		t.Fatalf("winProxyServer = %q, want 127.0.0.1:8080", got)
	}
	if got := winProxyServer("10.0.0.5", 9966); got != "10.0.0.5:9966" {
		t.Fatalf("winProxyServer = %q, want 10.0.0.5:9966", got)
	}
}

func TestWinEnableWritesRegistryAndBacksUpPriorEmpty(t *testing.T) {
	reg := newFakeWinReg() // no pre-existing proxy
	if err := winEnable(reg, "127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if reg.strs[winProxyServerName] != "127.0.0.1:8080" {
		t.Fatalf("ProxyServer = %q, want 127.0.0.1:8080", reg.strs[winProxyServerName])
	}
	if reg.dwords[winProxyEnableName] != 1 {
		t.Fatalf("ProxyEnable = %d, want 1", reg.dwords[winProxyEnableName])
	}
	if reg.dwords[winBackupMarkerName] != 1 {
		t.Fatal("backup marker should be set after first enable")
	}
	if v, ok := reg.strs[winBackupServerName]; !ok || v != "" {
		t.Fatalf("backup ProxyServer = %q ok=%v, want empty saved value", v, ok)
	}
	if reg.dwords[winBackupEnableName] != 0 {
		t.Fatalf("backup ProxyEnable = %d, want 0 (was off before)", reg.dwords[winBackupEnableName])
	}
	if reg.refreshed != 1 {
		t.Fatalf("refresh called %d times, want 1", reg.refreshed)
	}
}

func TestWinEnableBacksUpPriorProxyExactlyOnce(t *testing.T) {
	reg := newFakeWinReg()
	reg.strs[winProxyServerName] = "corp.example.com:3128" // operator's existing proxy
	reg.dwords[winProxyEnableName] = 1
	if err := winEnable(reg, "127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if err := winEnable(reg, "127.0.0.1", 9000); err != nil { // enable again
		t.Fatal(err)
	}
	// The backup must still hold the ORIGINAL corporate proxy, not Interseptor's.
	if reg.strs[winBackupServerName] != "corp.example.com:3128" {
		t.Fatalf("backup ProxyServer = %q, want the original corp proxy", reg.strs[winBackupServerName])
	}
	if reg.dwords[winBackupEnableName] != 1 {
		t.Fatalf("backup ProxyEnable = %d, want 1", reg.dwords[winBackupEnableName])
	}
	if reg.strs[winProxyServerName] != "127.0.0.1:9000" {
		t.Fatalf("ProxyServer = %q, want the latest 127.0.0.1:9000", reg.strs[winProxyServerName])
	}
}

func TestWinEnableRejectsBadPort(t *testing.T) {
	reg := newFakeWinReg()
	if err := winEnable(reg, "127.0.0.1", 0); err == nil {
		t.Fatal("expected error for port 0")
	}
	if err := winEnable(reg, "127.0.0.1", 70000); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestWinDisableRestoresTheBackedUpProxy(t *testing.T) {
	reg := newFakeWinReg()
	reg.strs[winProxyServerName] = "corp.example.com:3128"
	reg.dwords[winProxyEnableName] = 1
	if err := winEnable(reg, "127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if err := winDisable(reg); err != nil {
		t.Fatal(err)
	}
	if reg.strs[winProxyServerName] != "corp.example.com:3128" {
		t.Fatalf("ProxyServer after disable = %q, want restored corp proxy", reg.strs[winProxyServerName])
	}
	if reg.dwords[winProxyEnableName] != 1 {
		t.Fatalf("ProxyEnable after disable = %d, want 1 (restored)", reg.dwords[winProxyEnableName])
	}
	for _, name := range []string{winBackupServerName, winBackupEnableName, winBackupMarkerName} {
		if _, ok := reg.strs[name]; ok {
			t.Fatalf("backup string %s should be cleared", name)
		}
		if _, ok := reg.dwords[name]; ok {
			t.Fatalf("backup dword %s should be cleared", name)
		}
	}
}

func TestWinDisableWithoutBackupJustTurnsOff(t *testing.T) {
	reg := newFakeWinReg()
	reg.strs[winProxyServerName] = "corp.example.com:3128"
	reg.dwords[winProxyEnableName] = 1
	if err := winDisable(reg); err != nil { // no prior enable → no backup
		t.Fatal(err)
	}
	if reg.dwords[winProxyEnableName] != 0 {
		t.Fatalf("ProxyEnable = %d, want 0", reg.dwords[winProxyEnableName])
	}
	if reg.strs[winProxyServerName] != "corp.example.com:3128" {
		t.Fatalf("ProxyServer should be left intact, got %q", reg.strs[winProxyServerName])
	}
	if reg.refreshed != 1 {
		t.Fatalf("refresh called %d times, want 1", reg.refreshed)
	}
}

func TestWinStatusReadsProxyEnable(t *testing.T) {
	reg := newFakeWinReg()
	if on, _ := winStatus(reg); on {
		t.Fatal("status should be false when ProxyEnable absent")
	}
	reg.dwords[winProxyEnableName] = 1
	if on, _ := winStatus(reg); !on {
		t.Fatal("status should be true when ProxyEnable=1")
	}
	reg.dwords[winProxyEnableName] = 0
	if on, _ := winStatus(reg); on {
		t.Fatal("status should be false when ProxyEnable=0")
	}
}

// ── Linux gsettings logic (fake runner, runs on every OS) ─────────────────────

type fakeGsettings struct {
	calls [][]string
	mode  string // value returned for `get … mode`
	err   error
}

func (f *fakeGsettings) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.err != nil {
		return nil, f.err
	}
	if len(args) >= 2 && args[0] == "get" && args[len(args)-1] == "mode" {
		return []byte(f.mode), nil
	}
	return nil, nil
}

func TestGnomeEnableArgs(t *testing.T) {
	got := gnomeEnableArgs("127.0.0.1", 8080)
	want := [][]string{
		{"set", "org.gnome.system.proxy.http", "host", "127.0.0.1"},
		{"set", "org.gnome.system.proxy.http", "port", "8080"},
		{"set", "org.gnome.system.proxy.https", "host", "127.0.0.1"},
		{"set", "org.gnome.system.proxy.https", "port", "8080"},
		{"set", "org.gnome.system.proxy", "mode", "manual"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gnomeEnableArgs =\n%v\nwant\n%v", got, want)
	}
}

func TestParseGnomeMode(t *testing.T) {
	cases := map[string]string{
		"'none'\n":   "none",
		"'manual'\n": "manual",
		"'auto'":     "auto",
		"manual":     "manual",
		"  'none' ":  "none",
	}
	for in, want := range cases {
		if got := parseGnomeMode(in); got != want {
			t.Errorf("parseGnomeMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinuxEnableSetsManualAndRemembersPriorMode(t *testing.T) {
	resetLinuxState()
	t.Cleanup(resetLinuxState)
	f := &fakeGsettings{mode: "'none'\n"}
	if err := linuxEnable(f.run, "127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) == 0 {
		t.Fatal("expected gsettings calls")
	}
	// First call reads the prior mode for backup.
	if !reflect.DeepEqual(f.calls[0], []string{"get", "org.gnome.system.proxy", "mode"}) {
		t.Fatalf("first call = %v, want a get of mode", f.calls[0])
	}
	// Last call switches to manual.
	last := f.calls[len(f.calls)-1]
	if !reflect.DeepEqual(last, []string{"set", "org.gnome.system.proxy", "mode", "manual"}) {
		t.Fatalf("last call = %v, want set mode manual", last)
	}
	if !linuxBackupSet || linuxPriorMode != "none" {
		t.Fatalf("prior mode not remembered: set=%v mode=%q", linuxBackupSet, linuxPriorMode)
	}
}

func TestLinuxDisableRestoresPriorMode(t *testing.T) {
	resetLinuxState()
	t.Cleanup(resetLinuxState)
	f := &fakeGsettings{mode: "'auto'\n"}
	if err := linuxEnable(f.run, "127.0.0.1", 8080); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := linuxDisable(f.run); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], []string{"set", "org.gnome.system.proxy", "mode", "auto"}) {
		t.Fatalf("disable calls = %v, want a single set mode auto", f.calls)
	}
	if linuxBackupSet {
		t.Fatal("backup flag should be cleared after disable")
	}
}

func TestLinuxDisableWithoutPriorModeSetsNone(t *testing.T) {
	resetLinuxState()
	t.Cleanup(resetLinuxState)
	f := &fakeGsettings{}
	if err := linuxDisable(f.run); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], []string{"set", "org.gnome.system.proxy", "mode", "none"}) {
		t.Fatalf("disable calls = %v, want a single set mode none", f.calls)
	}
}

func TestLinuxStatusReportsManual(t *testing.T) {
	resetLinuxState()
	t.Cleanup(resetLinuxState)
	on, err := linuxStatus((&fakeGsettings{mode: "'manual'\n"}).run)
	if err != nil || !on {
		t.Fatalf("status manual = (%v,%v), want (true,nil)", on, err)
	}
	on, err = linuxStatus((&fakeGsettings{mode: "'none'\n"}).run)
	if err != nil || on {
		t.Fatalf("status none = (%v,%v), want (false,nil)", on, err)
	}
}

func TestLinuxEnableRejectsBadPort(t *testing.T) {
	resetLinuxState()
	t.Cleanup(resetLinuxState)
	if err := linuxEnable((&fakeGsettings{mode: "'none'\n"}).run, "127.0.0.1", -1); err == nil {
		t.Fatal("expected error for negative port")
	}
}
