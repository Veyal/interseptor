// Package android configures a USB-connected Android device for HTTPS
// interception via adb. Every action runs only when the operator explicitly
// requests it — nothing is applied automatically on startup.
package android

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	certRemoteName = "interceptor-ca.crt"
	certRemotePath = "/sdcard/Download/" + certRemoteName
)

// adbTimeout bounds every adb invocation. Device enumeration is normally
// sub-second, but a CA push/root/remount can take a few seconds on a slow
// device — 30s comfortably covers both while still guaranteeing the HTTP
// handler goroutine can't hang forever on a wedged adb (flaky USB, device
// waiting on an on-device prompt).
var adbTimeout = 30 * time.Second

// adbCommandName and adbExtraEnv are overridden in tests to point adbExec at
// a fake binary instead of the real "adb" on PATH.
var (
	adbCommandName = "adb"
	adbExtraEnv    []string
)

// adbExec runs adb subcommands. Overridden in tests.
var adbExec = func(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbCommandName, args...)
	if len(adbExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), adbExtraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("adb %s: timed out after %s (device unresponsive?)", strings.Join(args, " "), adbTimeout)
	}
	if err != nil {
		return out, fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Available reports whether adb is on PATH.
func Available() bool {
	_, err := exec.LookPath("adb")
	return err == nil
}

// Device is a connected Android device reported by adb.
type Device struct {
	Serial          string `json:"serial"`
	State           string `json:"state"`
	Model           string `json:"model,omitempty"`
	TransportID     int    `json:"transportId,omitempty"`
	Emulator        bool   `json:"emulator,omitempty"`
	SuggestedCAMode string `json:"suggestedCAMode,omitempty"`
}

// Devices lists adb-attached devices in the "device" or "unauthorized" state.
func Devices() ([]Device, error) {
	out, err := adbExec("devices", "-l")
	if err != nil {
		return nil, err
	}
	devs := parseDevices(string(out))
	for i := range devs {
		devs[i] = EnrichDevice(devs[i])
		if devs[i].State == "device" && devs[i].Model == "" {
			devs[i].Model = deviceModel(devs[i])
		}
	}
	return devs, nil
}

// EnrichDevice adds emulator detection and suggested CA mode.
func EnrichDevice(d Device) Device {
	d.Emulator = IsEmulator(d.Serial)
	if d.Emulator {
		d.SuggestedCAMode = "system"
	} else {
		d.SuggestedCAMode = "user"
	}
	return d
}

// IsEmulator reports whether serial looks like an Android emulator (emulator-5554).
func IsEmulator(serial string) bool {
	return strings.HasPrefix(serial, "emulator-")
}

// parseDevices extracts devices from `adb devices -l` output.
func parseDevices(output string) []Device {
	var devs []Device
	for i, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if i == 0 || line == "" {
			continue
		}
		if d, ok := parseDeviceLine(line); ok {
			devs = append(devs, d)
		}
	}
	return devs
}

// parseDeviceLine parses one row from `adb devices` / `adb devices -l`.
// Serials may contain spaces (e.g. MuMu's "(no serial number)"); state is a
// known token after whitespace, not simply fields[1].
func parseDeviceLine(line string) (Device, bool) {
	if idx := strings.Index(line, "\t"); idx >= 0 {
		serial := strings.TrimSpace(line[:idx])
		rest := strings.TrimSpace(line[idx+1:])
		if serial == "" || rest == "" {
			return Device{}, false
		}
		state := strings.Fields(rest)[0]
		return Device{Serial: serial, State: state, Model: modelFromDeviceLine(line), TransportID: transportFromDeviceLine(line)}, true
	}
	for _, state := range []string{"no permissions", "unauthorized", "authorizing", "offline", "device"} {
		needle := " " + state
		if !strings.HasSuffix(line, state) {
			if idx := strings.Index(line, needle+" "); idx >= 0 {
				serial := strings.TrimSpace(line[:idx])
				if serial != "" {
					return Device{Serial: serial, State: state, Model: modelFromDeviceLine(line), TransportID: transportFromDeviceLine(line)}, true
				}
			}
			if strings.HasSuffix(line, needle) {
				serial := strings.TrimSpace(strings.TrimSuffix(line, needle))
				if serial != "" {
					return Device{Serial: serial, State: state, Model: modelFromDeviceLine(line), TransportID: transportFromDeviceLine(line)}, true
				}
			}
		}
	}
	return Device{}, false
}

func transportFromDeviceLine(line string) int {
	const prefix = "transport_id:"
	i := strings.Index(line, prefix)
	if i < 0 {
		return 0
	}
	idStr := strings.TrimSpace(line[i+len(prefix):])
	if j := strings.IndexAny(idStr, " \t"); j >= 0 {
		idStr = idStr[:j]
	}
	n, _ := strconv.Atoi(idStr)
	return n
}

// unusableSerial reports serials adb lists but rejects for `-s` (MuMu, some emulators).
func unusableSerial(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "(no serial number)")
}

func modelFromDeviceLine(line string) string {
	const prefix = "model:"
	i := strings.Index(line, prefix)
	if i < 0 {
		return ""
	}
	rest := line[i+len(prefix):]
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func deviceModel(d Device) string {
	if d.Model != "" {
		return d.Model
	}
	out, err := runDev(d, true, "shell", "getprop", "ro.product.model")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (d Device) adbArgs(singleDevice bool) []string {
	if d.TransportID > 0 && (unusableSerial(d.Serial) || d.Serial == "") {
		return []string{"-t", strconv.Itoa(d.TransportID)}
	}
	if d.Serial != "" && !unusableSerial(d.Serial) {
		return []string{"-s", d.Serial}
	}
	if singleDevice {
		return nil
	}
	if d.TransportID > 0 {
		return []string{"-t", strconv.Itoa(d.TransportID)}
	}
	return nil
}

func runDev(d Device, singleDevice bool, args ...string) ([]byte, error) {
	prefix := d.adbArgs(singleDevice)
	return adbExec(append(prefix, args...)...)
}

// ResolveDevice picks the target device: an explicit serial or the sole authorized device.
func ResolveDevice(serial string, devices []Device) (Device, error) {
	if len(devices) == 0 {
		return Device{}, errors.New("no Android device connected — enable USB debugging and authorize this computer")
	}
	if serial != "" {
		for _, d := range devices {
			if d.Serial == serial {
				if d.State == "device" {
					return d, nil
				}
				if d.State == "unauthorized" {
					return Device{}, fmt.Errorf("device %q is unauthorized — accept the USB debugging prompt on the device", serial)
				}
				return Device{}, fmt.Errorf("device %q is in state %q", serial, d.State)
			}
		}
		return Device{}, fmt.Errorf("device %q not found", serial)
	}
	var ready []Device
	for _, d := range devices {
		if d.State == "device" {
			ready = append(ready, d)
		}
	}
	if len(ready) == 0 {
		if len(devices) == 1 && devices[0].State == "unauthorized" {
			return Device{}, errors.New("device connected but unauthorized — accept the USB debugging prompt on the device")
		}
		return Device{}, errors.New("no authorized Android device connected")
	}
	if len(ready) > 1 {
		return Device{}, errors.New("multiple devices connected — pick one from the device list")
	}
	return ready[0], nil
}

// ResolveSerial picks the target device serial for display/API responses.
func ResolveSerial(serial string, devices []Device) (string, error) {
	d, err := ResolveDevice(serial, devices)
	if err != nil {
		return "", err
	}
	return d.Serial, nil
}

// LANHost returns a likely private LAN IPv4 for this machine.
func LANHost() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			return ip4.String(), nil
		}
	}
	return "", errors.New("no LAN IPv4 address found — connect to Wi‑Fi/Ethernet or pass wifiHost explicitly")
}

// ProxyValue reads the device global HTTP proxy setting.
func ProxyValue(d Device) (string, error) {
	out, err := runDev(d, true, "shell", "settings", "get", "global", "http_proxy")
	if err != nil {
		return "", err
	}
	return parseProxyValue(string(out)), nil
}

func parseProxyValue(raw string) string {
	v := strings.TrimSpace(strings.TrimRight(raw, "\r"))
	if v == "" || v == "null" || v == ":0" {
		return ""
	}
	return v
}

// EnableProxyUSB sets adb reverse and points the device global proxy at 127.0.0.1:port.
func EnableProxyUSB(d Device, port int) error {
	return enableProxy(d, "127.0.0.1", port, true)
}

// EnableProxyWiFi points the device global proxy at host:port (no adb reverse).
func EnableProxyWiFi(d Device, host string, port int) error {
	if host == "" {
		return errors.New("wifi proxy host is required")
	}
	return enableProxy(d, host, port, false)
}

func enableProxy(d Device, host string, port int, reverse bool) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid proxy port %d", port)
	}
	p := strconv.Itoa(port)
	if reverse {
		if _, err := runDev(d, true, "reverse", "tcp:"+p, "tcp:"+p); err != nil {
			return fmt.Errorf("adb reverse tcp:%s: %w", p, err)
		}
	}
	proxy := host + ":" + p
	if _, err := runDev(d, true, "shell", "settings", "put", "global", "http_proxy", proxy); err != nil {
		return fmt.Errorf("set global http_proxy: %w", err)
	}
	return nil
}

// DisableProxy clears the device global proxy and removes adb reverse for port.
func DisableProxy(d Device, port int) error {
	_, _ = runDev(d, true, "shell", "settings", "put", "global", "http_proxy", ":0")
	if port > 0 && port <= 65535 {
		_, _ = runDev(d, true, "reverse", "--remove", "tcp:"+strconv.Itoa(port))
	}
	return nil
}

// InstallUserCA pushes the CA to the device and opens the system install prompt.
func InstallUserCA(d Device, certPEM []byte) error {
	path, err := writeTempCert(certPEM, "interceptor-ca-*.crt")
	if err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := runDev(d, true, "push", path, certRemotePath); err != nil {
		return fmt.Errorf("push CA: %w", err)
	}
	uri := "file://" + certRemotePath
	if _, err := runDev(d, true, "shell", "am", "start",
		"-a", "android.credentials.INSTALL",
		"-t", "application/x-x509-ca-cert",
		"-d", uri,
	); err != nil {
		return fmt.Errorf("open CA install prompt: %w", err)
	}
	return nil
}

// InstallSystemCA installs the CA into the system trust store (rooted device or emulator).
func InstallSystemCA(d Device, certPEM []byte) error {
	remotePath, err := systemCAPath(certPEM)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(certPEM)
	path, err := writeTempCert(block.Bytes, "interceptor-ca-*.0")
	if err != nil {
		return err
	}
	defer os.Remove(path)

	if _, err := runDev(d, true, "root"); err != nil {
		return fmt.Errorf("adb root failed — system CA needs a rooted device or emulator: %w", err)
	}
	if _, err := runDev(d, true, "remount"); err != nil {
		return fmt.Errorf("adb remount failed: %w", err)
	}
	if _, err := runDev(d, true, "push", path, remotePath); err != nil {
		return fmt.Errorf("push system CA: %w", err)
	}
	if _, err := runDev(d, true, "shell", "chmod", "644", remotePath); err != nil {
		return fmt.Errorf("chmod system CA: %w", err)
	}
	return nil
}

// RemoveSystemCA deletes the Interceptor CA from the system trust store (rooted/emulator).
func RemoveSystemCA(d Device, certPEM []byte) error {
	remotePath, err := systemCAPath(certPEM)
	if err != nil {
		return err
	}
	if _, err := runDev(d, true, "root"); err != nil {
		return fmt.Errorf("adb root failed — removing system CA needs root: %w", err)
	}
	if _, err := runDev(d, true, "remount"); err != nil {
		return fmt.Errorf("adb remount failed: %w", err)
	}
	if _, err := runDev(d, true, "shell", "rm", "-f", remotePath); err != nil {
		return fmt.Errorf("remove system CA: %w", err)
	}
	return nil
}

func systemCAPath(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse CA certificate: %w", err)
	}
	hash := subjectHashOld(cert.RawSubject)
	return "/system/etc/security/cacerts/" + hash + ".0", nil
}

// SetupOpts configures a one-click Android intercept setup.
type SetupOpts struct {
	ProxyMode string // usb (default) or wifi
	CAMode    string // user, system, or auto
	WiFiHost  string
	Port      int
}

// SetupResult is returned from Setup and Teardown.
type SetupResult struct {
	Serial          string   `json:"serial"`
	Proxy           string   `json:"proxy,omitempty"`
	CAMode          string   `json:"caMode,omitempty"`
	Steps           []string `json:"steps"`
	NeedsUserAction bool     `json:"needsUserAction,omitempty"`
	Message         string   `json:"message"`
	Warning         string   `json:"warning,omitempty"`
}

// Setup enables proxy (USB or Wi‑Fi) and installs the CA in one explicit action.
func Setup(d Device, certPEM []byte, opts SetupOpts) (*SetupResult, error) {
	if opts.Port <= 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("invalid proxy port %d", opts.Port)
	}
	caMode := resolveCAMode(d, opts.CAMode)
	res := &SetupResult{Serial: d.Serial, CAMode: caMode}

	proxyMode := strings.ToLower(strings.TrimSpace(opts.ProxyMode))
	if proxyMode == "" {
		proxyMode = "usb"
	}
	switch proxyMode {
	case "wifi":
		host := opts.WiFiHost
		if host == "" {
			return nil, errors.New("wifiHost is required for wifi proxy mode")
		}
		if err := EnableProxyWiFi(d, host, opts.Port); err != nil {
			return nil, err
		}
		res.Proxy = host + ":" + strconv.Itoa(opts.Port)
		res.Steps = append(res.Steps, "wifi global proxy set to "+res.Proxy)
	default:
		if err := EnableProxyUSB(d, opts.Port); err != nil {
			return nil, err
		}
		res.Proxy = "127.0.0.1:" + strconv.Itoa(opts.Port)
		res.Steps = append(res.Steps, "usb reverse + global proxy set to "+res.Proxy)
	}

	switch caMode {
	case "system":
		if err := InstallSystemCA(d, certPEM); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "system CA installed")
		res.Message = "Setup complete — reboot the device so all apps trust the system CA"
	case "user":
		if err := InstallUserCA(d, certPEM); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "user CA install prompt opened")
		res.NeedsUserAction = true
		res.Message = "Proxy active — confirm the CA install prompt on the device (PIN/fingerprint). Most apps on Android 7+ ignore user CAs."
	default:
		return nil, fmt.Errorf("unknown ca mode %q", caMode)
	}
	return res, nil
}

func resolveCAMode(d Device, mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", "auto":
		if IsEmulator(d.Serial) {
			return "system"
		}
		return "user"
	case "user", "system":
		return m
	default:
		return m
	}
}

// Teardown clears proxy and optionally removes the system CA.
func Teardown(d Device, port int, certPEM []byte, removeSystemCA bool) (*SetupResult, error) {
	res := &SetupResult{Serial: d.Serial}
	if err := DisableProxy(d, port); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, "global proxy cleared", "usb reverse removed (if any)")
	res.Message = "Device proxy cleared"
	if removeSystemCA {
		if certPEM == nil {
			return nil, errors.New("no CA configured")
		}
		if err := RemoveSystemCA(d, certPEM); err != nil {
			res.Warning = err.Error()
			res.Message = "Proxy cleared; system CA removal failed (see warning)"
		} else {
			res.Steps = append(res.Steps, "system CA removed")
			res.Message = "Proxy cleared and system CA removed — reboot the device"
		}
	}
	return res, nil
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

// subjectHashOld matches OpenSSL's X509_subject_name_hash_old for cacerts filenames.
func subjectHashOld(rawSubject []byte) string {
	sum := md5.Sum(rawSubject)
	return fmt.Sprintf("%08x", binary.LittleEndian.Uint32(sum[:4]))
}
