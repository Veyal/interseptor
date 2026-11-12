package ios

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSimctl replaces the simctl runner for one test.
func stubSimctl(t *testing.T, fn func(args ...string) ([]byte, error)) *[][]string {
	t.Helper()
	var calls [][]string
	orig := simctlExec
	simctlExec = func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return fn(args...)
	}
	t.Cleanup(func() { simctlExec = orig })
	return &calls
}

func stubIdevice(t *testing.T, fn func(tool string, args ...string) ([]byte, error)) {
	t.Helper()
	orig := ideviceExec
	ideviceExec = fn
	t.Cleanup(func() { ideviceExec = orig })
}

// fakeTools puts executable stubs for the named tools on PATH so the
// availability probes (exec.LookPath) answer without the real Xcode /
// libimobiledevice installed.
// fakeSimctlHost makes the platform gate report "macOS" for one test, so the
// simulator path is exercised on every OS the suite runs on.
func fakeSimctlHost(t *testing.T) {
	t.Helper()
	orig := simctlHostOK
	simctlHostOK = func() bool { return true }
	t.Cleanup(func() { simctlHostOK = orig })
}

func fakeTools(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// A simulator selected by UDID must be addressed by that UDID. "booted" is only
// a stand-in for "the one booted simulator" and simctl rejects it as ambiguous
// when several are booted — which is exactly when the operator picks one.
func TestSimctlTargetPrefersExplicitUDID(t *testing.T) {
	if got := simctlTarget(Device{Kind: KindSimulator, UDID: "SIM-AAA", Booted: true}); got != "SIM-AAA" {
		t.Fatalf("booted simulator with a UDID addressed as %q, want SIM-AAA", got)
	}
	if got := simctlTarget(Device{Kind: KindSimulator, UDID: "SIM-BBB"}); got != "SIM-BBB" {
		t.Fatalf("shutdown simulator addressed as %q, want SIM-BBB", got)
	}
	if got := simctlTarget(Device{Kind: KindSimulator}); got != "booted" {
		t.Fatalf("device without a UDID addressed as %q, want booted", got)
	}
}

func TestInstallCASimulatorAddressesExplicitUDID(t *testing.T) {
	fakeTools(t, "xcrun") // the simctlExec seam is stubbed below
	if !SimctlAvailable() {
		t.Skip("simctl unavailable on this platform")
	}
	calls := stubSimctl(t, func(args ...string) ([]byte, error) { return nil, nil })
	d := Device{Kind: KindSimulator, UDID: "SIM-AAA", Booted: true}
	if err := InstallCASimulator(d, []byte("pem")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("simctl calls = %v, want one keychain add-root-cert", *calls)
	}
	if got := (*calls)[0]; got[0] != "keychain" || got[1] != "SIM-AAA" || got[2] != "add-root-cert" {
		t.Fatalf("simctl args = %v, want keychain SIM-AAA add-root-cert <cert>", got)
	}
}

// A simulator backend that fails (Xcode not installed, xcrun present but simctl
// missing, simulator service wedged) must not hide a connected iPhone: physical
// devices are discovered by a different tool and were reachable before.
func TestAllDevicesKeepsPhysicalWhenSimulatorBackendFails(t *testing.T) {
	fakeTools(t, "xcrun", "idevice_id")
	stubSimctl(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("xcrun: error: unable to find utility \"simctl\"")
	})
	stubIdevice(t, func(tool string, args ...string) ([]byte, error) {
		if tool == "idevice_id" {
			return []byte("00008120-TESTUDID\n"), nil
		}
		return []byte("Test iPhone\n"), nil
	})

	devs, err := AllDevices()
	if err != nil {
		t.Fatalf("AllDevices failed although a physical device is connected: %v", err)
	}
	if len(devs) != 1 || devs[0].Kind != KindPhysical || devs[0].UDID != "00008120-TESTUDID" {
		t.Fatalf("AllDevices = %#v, want the connected iPhone", devs)
	}
}

// When neither backend yields anything, the error is still reported.
func TestAllDevicesReportsErrorWhenBothBackendsFail(t *testing.T) {
	fakeSimctlHost(t)
	fakeTools(t, "xcrun", "idevice_id")
	stubSimctl(t, func(args ...string) ([]byte, error) { return nil, errors.New("simctl broke") })
	stubIdevice(t, func(tool string, args ...string) ([]byte, error) { return nil, errors.New("usb broke") })
	if _, err := AllDevices(); err == nil {
		t.Fatal("AllDevices hid the failure of both backends")
	}
}

// A broken simulator backend with no phone attached must still say why:
// "no booted simulator" would hide that simctl itself is unusable.
func TestAllDevicesReportsSimulatorErrorWhenNothingFound(t *testing.T) {
	fakeSimctlHost(t) // the release workflow runs this on Linux, where simctl cannot exist
	fakeTools(t, "xcrun", "idevice_id")
	stubSimctl(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("xcrun: unable to find utility \"simctl\"")
	})
	stubIdevice(t, func(tool string, args ...string) ([]byte, error) {
		if tool == "idevice_id" {
			return nil, nil // no phone connected
		}
		return nil, nil
	})
	_, err := AllDevices()
	if err == nil || !strings.Contains(err.Error(), "simctl") {
		t.Fatalf("AllDevices = %v, want the simulator backend error", err)
	}
}
