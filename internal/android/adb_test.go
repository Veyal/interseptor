package android

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stubADB(t *testing.T, fn func(args ...string) ([]byte, error)) {
	t.Helper()
	orig := adbExec
	adbExec = fn
	t.Cleanup(func() { adbExec = orig })
}

func TestParseDevicesSkipsHeader(t *testing.T) {
	out := "List of devices attached\nemulator-5554\tdevice product:sdk model:sdk_gphone device:generic\nR58M\tunauthorized transport_id:2\n"
	got := parseDevices(out)
	want := []Device{
		{Serial: "emulator-5554", State: "device", Model: "sdk_gphone"},
		{Serial: "R58M", State: "unauthorized", TransportID: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDevices = %#v, want %#v", got, want)
	}
}

func TestParseDevicesNoSerialNumber(t *testing.T) {
	out := "List of devices attached\n(no serial number)     device product:a54x model:SM_A546E device:a54x transport_id:1\n"
	got := parseDevices(out)
	if len(got) != 1 {
		t.Fatalf("expected 1 device, got %#v", got)
	}
	if got[0].Serial != "(no serial number)" || got[0].State != "device" || got[0].Model != "SM_A546E" || got[0].TransportID != 1 {
		t.Fatalf("parseDevices = %#v", got[0])
	}
	d, err := ResolveDevice("", got)
	if err != nil || d.Serial != "(no serial number)" || d.TransportID != 1 {
		t.Fatalf("ResolveDevice = %#v, %v", d, err)
	}
}

func TestParseDevicesEmpty(t *testing.T) {
	if got := parseDevices("List of devices attached\n"); len(got) != 0 {
		t.Fatalf("expected no devices, got %#v", got)
	}
}

func TestParseProxyValue(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8080\n": "127.0.0.1:8080",
		"null\n":           "",
		":0\r\n":           "",
		"":                 "",
	}
	for in, want := range tests {
		if got := parseProxyValue(in); got != want {
			t.Fatalf("parseProxyValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsEmulator(t *testing.T) {
	if !IsEmulator("emulator-5554") {
		t.Fatal("expected emulator")
	}
	if IsEmulator("R58M12345") {
		t.Fatal("expected physical device")
	}
}

func TestEnrichDevice(t *testing.T) {
	emu := EnrichDevice(Device{Serial: "emulator-5554", State: "device"})
	if !emu.Emulator || emu.SuggestedCAMode != "system" {
		t.Fatalf("emulator enrich = %#v", emu)
	}
	phy := EnrichDevice(Device{Serial: "ABC", State: "device"})
	if phy.Emulator || phy.SuggestedCAMode != "user" {
		t.Fatalf("physical enrich = %#v", phy)
	}
}

func TestResolveSerialSingleDevice(t *testing.T) {
	serial, err := ResolveSerial("", []Device{{Serial: "abc", State: "device"}})
	if err != nil || serial != "abc" {
		t.Fatalf("ResolveSerial = %q, %v", serial, err)
	}
}

func TestResolveSerialMultipleRequiresPick(t *testing.T) {
	_, err := ResolveSerial("", []Device{
		{Serial: "a", State: "device"},
		{Serial: "b", State: "device"},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected multiple-device error, got %v", err)
	}
}

func TestResolveSerialUnauthorized(t *testing.T) {
	_, err := ResolveSerial("", []Device{{Serial: "x", State: "unauthorized"}})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestResolveSerialExplicit(t *testing.T) {
	devs := []Device{{Serial: "a", State: "device"}, {Serial: "b", State: "device"}}
	serial, err := ResolveSerial("b", devs)
	if err != nil || serial != "b" {
		t.Fatalf("ResolveSerial explicit = %q, %v", serial, err)
	}
}

func TestResolveCAModeAuto(t *testing.T) {
	if got := resolveCAMode(Device{Serial: "emulator-5554"}, "auto"); got != "system" {
		t.Fatalf("auto emulator = %q", got)
	}
	if got := resolveCAMode(Device{Serial: "ABC123"}, "auto"); got != "user" {
		t.Fatalf("auto physical = %q", got)
	}
}

func TestSubjectHashOldStable(t *testing.T) {
	pemBytes := testCAPEM(t)
	block, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	hash := subjectHashOld(cert.RawSubject)
	if len(hash) != 8 {
		t.Fatalf("hash length = %d, want 8 hex chars", len(hash))
	}
	if hash2 := subjectHashOld(cert.RawSubject); hash2 != hash {
		t.Fatalf("hash not stable: %s vs %s", hash, hash2)
	}
}

func TestEnableProxyUSBCommands(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := EnableProxyUSB(Device{Serial: "serial1", State: "device"}, 8080); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-s", "serial1", "reverse", "tcp:8080", "tcp:8080"},
		{"-s", "serial1", "shell", "settings", "put", "global", "http_proxy", "127.0.0.1:8080"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEnableProxyUSBNoSerialUsesTransport(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	d := Device{Serial: "(no serial number)", State: "device", TransportID: 1}
	if err := EnableProxyUSB(d, 8080); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-t", "1", "reverse", "tcp:8080", "tcp:8080"},
		{"-t", "1", "shell", "settings", "put", "global", "http_proxy", "127.0.0.1:8080"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEnableProxyUSBNoSerialDefaultDevice(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	d := Device{Serial: "(no serial number)", State: "device"}
	if err := EnableProxyUSB(d, 8080); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"reverse", "tcp:8080", "tcp:8080"},
		{"shell", "settings", "put", "global", "http_proxy", "127.0.0.1:8080"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEnableProxyWiFiCommands(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := EnableProxyWiFi(Device{Serial: "dev", State: "device"}, "192.168.1.10", 8080); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-s", "dev", "shell", "settings", "put", "global", "http_proxy", "192.168.1.10:8080"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestDisableProxyCommands(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := DisableProxy(Device{Serial: "dev", State: "device"}, 8080); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-s", "dev", "shell", "settings", "put", "global", "http_proxy", ":0"},
		{"-s", "dev", "reverse", "--remove", "tcp:8080"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestInstallUserCACommands(t *testing.T) {
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := InstallUserCA(Device{Serial: "dev", State: "device"}, testCAPEM(t)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 adb calls, got %d: %#v", len(calls), calls)
	}
	if calls[0][2] != "push" {
		t.Fatalf("expected push, got %#v", calls[0])
	}
	if calls[1][len(calls[1])-1] != "file://"+certRemotePath {
		t.Fatalf("expected install URI file://%s, got %#v", certRemotePath, calls[1])
	}
}

func TestInstallSystemCACommands(t *testing.T) {
	pemBytes := testCAPEM(t)
	block, _ := pem.Decode(pemBytes)
	cert, _ := x509.ParseCertificate(block.Bytes)
	hash := subjectHashOld(cert.RawSubject)

	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := InstallSystemCA(Device{Serial: "dev", State: "device"}, pemBytes); err != nil {
		t.Fatal(err)
	}
	wantRemote := "/system/etc/security/cacerts/" + hash + ".0"
	foundPush := false
	for _, c := range calls {
		if len(c) >= 4 && c[2] == "push" && c[len(c)-1] == wantRemote {
			foundPush = true
		}
	}
	if !foundPush {
		t.Fatalf("expected push to %s, calls = %#v", wantRemote, calls)
	}
}

func TestRemoveSystemCACommands(t *testing.T) {
	pemBytes := testCAPEM(t)
	block, _ := pem.Decode(pemBytes)
	cert, _ := x509.ParseCertificate(block.Bytes)
	hash := subjectHashOld(cert.RawSubject)
	wantRemote := "/system/etc/security/cacerts/" + hash + ".0"

	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := RemoveSystemCA(Device{Serial: "dev", State: "device"}, pemBytes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range calls {
		if len(c) >= 6 && c[3] == "rm" && c[len(c)-1] == wantRemote {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rm %s, calls = %#v", wantRemote, calls)
	}
}

func TestSetupUSBUser(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) {
		return nil, nil
	})
	res, err := Setup(Device{Serial: "dev", State: "device"}, testCAPEM(t), SetupOpts{ProxyMode: "usb", CAMode: "user", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsUserAction || res.Proxy != "127.0.0.1:8080" || len(res.Steps) < 2 {
		t.Fatalf("setup result = %#v", res)
	}
}

func TestTeardownWithRemoveSystemCA(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) {
		return nil, nil
	})
	res, err := Teardown(Device{Serial: "dev", State: "device"}, 8080, testCAPEM(t), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) < 3 {
		t.Fatalf("expected teardown steps including CA removal, got %#v", res.Steps)
	}
}

func TestInstallSystemCAInvalidPEM(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("should not run")
	})
	if err := InstallSystemCA(Device{Serial: "dev", State: "device"}, []byte("not a cert")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

// TestAdbExecTimesOutOnHang proves the real (non-stubbed) adbExec no longer
// blocks forever on a wedged adb process (flaky USB, device waiting on a
// prompt): it must give up and return an error well before a real hang
// would. It points adbCommandName at this test binary re-invoked in
// "helper process" mode, which just sleeps — simulating a hung adb.
func TestAdbExecTimesOutOnHang(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}

	origName := adbCommandName
	origTimeout := adbTimeout
	origEnv := adbExtraEnv
	adbCommandName = os.Args[0]
	adbTimeout = 500 * time.Millisecond
	adbExtraEnv = []string{"GO_WANT_HELPER_PROCESS=1"}
	t.Cleanup(func() {
		adbCommandName = origName
		adbTimeout = origTimeout
		adbExtraEnv = origEnv
	})

	start := time.Now()
	_, err := adbExec("-test.run=TestAdbExecTimesOutOnHang")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from hung adb process")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("adbExec took %v, expected to return quickly after the %v bound", elapsed, adbTimeout)
	}
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test Interseptor CA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
