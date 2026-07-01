// Package ios configures iOS simulators and devices for HTTPS interception.
// Simulator CA install is automated via xcrun simctl; physical devices use a
// generated .mobileconfig (proxy + CA) the operator installs in Safari.
package ios

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// simctlExec runs xcrun simctl. Overridden in tests.
var simctlExec = func(args ...string) ([]byte, error) {
	cmd := exec.Command("xcrun", append([]string{"simctl"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("xcrun simctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// ideviceExec runs libimobiledevice tools when present. Overridden in tests.
var ideviceExec = func(tool string, args ...string) ([]byte, error) {
	cmd := exec.Command(tool, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// SimctlAvailable reports whether Xcode simctl is usable (macOS only).
func SimctlAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := exec.LookPath("xcrun")
	return err == nil
}

// IDeviceAvailable reports whether libimobiledevice is on PATH.
func IDeviceAvailable() bool {
	_, err := exec.LookPath("idevice_id")
	return err == nil
}

// Available reports whether any automated iOS backend exists on this machine.
func Available() bool {
	return SimctlAvailable() || IDeviceAvailable()
}

// DeviceKind distinguishes simulators from USB-connected iPhones.
const (
	KindSimulator = "simulator"
	KindPhysical  = "physical"
)

// Device is a simulator or USB-connected iOS device.
type Device struct {
	Kind     string `json:"kind"`
	UDID     string `json:"udid"`
	Name     string `json:"name"`
	State    string `json:"state,omitempty"`
	Runtime  string `json:"runtime,omitempty"`
	Booted   bool   `json:"booted,omitempty"`
	SuggestedTarget string `json:"suggestedTarget,omitempty"` // simulator | physical
}

// Simulators lists iOS simulators (booted ones first).
func Simulators() ([]Device, error) {
	if !SimctlAvailable() {
		return nil, nil
	}
	out, err := simctlExec("list", "devices", "-j")
	if err != nil {
		return nil, err
	}
	return parseSimctlDevices(out)
}

func parseSimctlDevices(raw []byte) ([]Device, error) {
	var doc struct {
		Devices map[string][]struct {
			Name   string `json:"name"`
			UDID   string `json:"udid"`
			State  string `json:"state"`
			IsAvailable bool `json:"isAvailable"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse simctl json: %w", err)
	}
	var booted, other []Device
	for runtime, list := range doc.Devices {
		if !strings.Contains(runtime, "iOS") && !strings.Contains(runtime, "tvOS") && !strings.Contains(runtime, "watchOS") {
			continue
		}
		for _, d := range list {
			if !d.IsAvailable && d.State != "Booted" {
				continue
			}
			dev := Device{
				Kind:    KindSimulator,
				UDID:    d.UDID,
				Name:    d.Name,
				State:   d.State,
				Runtime: runtimeLabel(runtime),
				Booted:  strings.EqualFold(d.State, "Booted"),
				SuggestedTarget: "simulator",
			}
			if dev.Booted {
				booted = append(booted, dev)
			} else {
				other = append(other, dev)
			}
		}
	}
	return append(booted, other...), nil
}

func runtimeLabel(runtime string) string {
	if i := strings.LastIndex(runtime, "iOS-"); i >= 0 {
		v := strings.ReplaceAll(runtime[i+4:], "-", ".")
		return "iOS " + v
	}
	return runtime
}

// PhysicalDevices lists USB-connected iOS devices via idevice_id.
func PhysicalDevices() ([]Device, error) {
	if !IDeviceAvailable() {
		return nil, nil
	}
	out, err := ideviceExec("idevice_id", "-l")
	if err != nil {
		return nil, err
	}
	var devs []Device
	for _, line := range strings.Split(string(out), "\n") {
		udid := strings.TrimSpace(line)
		if udid == "" {
			continue
		}
		name := physicalDeviceName(udid)
		devs = append(devs, Device{
			Kind:            KindPhysical,
			UDID:            udid,
			Name:            name,
			State:           "connected",
			SuggestedTarget: "physical",
		})
	}
	return devs, nil
}

func physicalDeviceName(udid string) string {
	out, err := ideviceExec("ideviceinfo", "-u", udid, "-k", "DeviceName")
	if err != nil {
		return udid
	}
	n := strings.TrimSpace(string(out))
	if n == "" {
		return udid
	}
	return n
}

// AllDevices returns simulators then physical devices.
func AllDevices() ([]Device, error) {
	sims, err := Simulators()
	if err != nil {
		return nil, err
	}
	phys, err := PhysicalDevices()
	if err != nil {
		return nil, err
	}
	return append(sims, phys...), nil
}

// ResolveDevice picks explicit udid or the sole booted simulator / connected phone.
func ResolveDevice(udid string, devices []Device) (Device, error) {
	if len(devices) == 0 {
		return Device{}, errors.New("no iOS simulator or device found — boot a simulator in Xcode or connect an iPhone with libimobiledevice")
	}
	if udid != "" {
		for _, d := range devices {
			if d.UDID == udid {
				return d, nil
			}
		}
		return Device{}, fmt.Errorf("device %q not found", udid)
	}
	var bootedSims, phys []Device
	for _, d := range devices {
		switch d.Kind {
		case KindSimulator:
			if d.Booted {
				bootedSims = append(bootedSims, d)
			}
		case KindPhysical:
			phys = append(phys, d)
		}
	}
	if len(bootedSims) == 1 {
		return bootedSims[0], nil
	}
	if len(bootedSims) > 1 {
		return Device{}, errors.New("multiple booted simulators — pick one from the device list")
	}
	if len(phys) == 1 {
		return phys[0], nil
	}
	if len(phys) > 1 {
		return Device{}, errors.New("multiple iPhones connected — pick one from the device list")
	}
	return Device{}, errors.New("no booted simulator or connected iPhone — boot a simulator or connect a device")
}

// InstallCASimulator adds the Interceptor CA to a simulator trust store.
func InstallCASimulator(d Device, certPEM []byte) error {
	if !SimctlAvailable() {
		return errors.New("Xcode simctl not available — install Xcode on macOS for simulator automation")
	}
	path, err := writeTempCert(certPEM, "interceptor-ios-ca-*.crt")
	if err != nil {
		return err
	}
	defer os.Remove(path)
	target := d.UDID
	if target == "" || d.Booted {
		target = "booted"
	}
	if _, err := simctlExec("keychain", target, "add-root-cert", path); err != nil {
		return fmt.Errorf("simulator CA install: %w", err)
	}
	return nil
}

// OpenURL opens a URL in the simulator (e.g. profile install page).
func OpenURL(d Device, rawURL string) error {
	if !SimctlAvailable() {
		return errors.New("Xcode simctl not available")
	}
	target := d.UDID
	if target == "" || d.Booted {
		target = "booted"
	}
	if _, err := simctlExec("openurl", target, rawURL); err != nil {
		return fmt.Errorf("simulator openurl: %w", err)
	}
	return nil
}

func writeTempCert(data []byte, pattern string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// LANHost returns a likely private LAN IPv4 (shared helper for Wi‑Fi proxy).
func LANHost() (string, error) {
	return lanHost()
}
