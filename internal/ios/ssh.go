// Package ios — SSH helpers for jailbroken iOS devices (OpenSSH as root).
// Credentials are supplied per request and are never persisted by Interseptor.
package ios

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultSSHUser    = "root"
	defaultSSHPort    = 22
	profileRemotePath = "/tmp/interseptor.mobileconfig"
	sshConnectTimeout = 12 * time.Second
)

// sshCommandTimeout bounds how long we wait for a remote command to finish
// once the SSH session is open. The connection itself already has
// sshConnectTimeout; without this, a remote command that never returns
// (device stuck, shell waiting on input) would hang the HTTP handler
// goroutine forever. The x/crypto/ssh session API has no deadline/context
// support, so this is enforced with a goroutine + select: on timeout we
// close the session (best-effort — it may not kill the remote process) and
// return an error so the caller unblocks. A var (not const) so tests can
// shrink it instead of waiting out a real 30s bound.
var sshCommandTimeout = 30 * time.Second

// SSHOpts configures SSH access to a jailbroken iOS device.
type SSHOpts struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyPath  string
	KeyPEM   []byte // in-memory key material (preferred over KeyPath when set)
}

// SSHResult is returned from SSH automation steps.
type SSHResult struct {
	Host            string   `json:"host"`
	User            string   `json:"user"`
	Port            int      `json:"port,omitempty"`
	Reachable       bool     `json:"reachable"`
	Authenticated   bool     `json:"authenticated,omitempty"`
	Steps           []string `json:"steps,omitempty"`
	ProfileURL      string   `json:"profileUrl,omitempty"`
	Proxy           string   `json:"proxy,omitempty"`
	NeedsUserAction bool     `json:"needsUserAction,omitempty"`
	Message         string   `json:"message"`
	Warning         string   `json:"warning,omitempty"`
	Method          string   `json:"method,omitempty"`
}

// SSHSetupOpts configures one-click jailbroken iOS intercept setup.
type SSHSetupOpts struct {
	SSHOpts
	ProxyHost  string
	ProxyPort  int
	ProfileURL string // control plane base URL (scheme + host[:port])
}

// sshDial connects to the device. Overridden in tests.
var sshDial = dialSSH

// sshRunCmd executes a remote shell command. Overridden in tests.
var sshRunCmd = runSSHCommand

// SSHAvailable reports whether the embedded SSH client can be used (always true).
func SSHAvailable() bool {
	return true
}

// TCPReachable checks whether host:port accepts a TCP connection (no SSH auth).
func TCPReachable(host string, port int) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if port <= 0 {
		port = defaultSSHPort
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// SSHStatus verifies SSH connectivity and authentication.
func SSHStatus(opts SSHOpts) (*SSHResult, error) {
	resolved, err := resolveSSHOpts(opts)
	if err != nil {
		return nil, err
	}
	res := &SSHResult{
		Host: resolved.Host,
		User: resolved.User,
		Port: resolved.Port,
	}
	res.Reachable = TCPReachable(resolved.Host, resolved.Port)
	if !res.Reachable {
		res.Message = fmt.Sprintf("Cannot reach %s:%d — ensure OpenSSH is running on the device and the host is reachable on the network", resolved.Host, resolved.Port)
		return res, nil
	}
	client, err := sshDial(resolved)
	if err != nil {
		res.Message = "TCP reachable but SSH authentication failed: " + err.Error()
		return res, nil
	}
	defer client.Close()
	out, err := sshRunCmd(client, "uname -a")
	if err != nil {
		res.Message = "SSH session failed: " + err.Error()
		return res, nil
	}
	res.Authenticated = true
	res.Steps = append(res.Steps, "SSH connected as "+resolved.User)
	if strings.TrimSpace(out) != "" {
		res.Steps = append(res.Steps, strings.TrimSpace(out))
	}
	res.Message = fmt.Sprintf("SSH OK — connected to %s@%s:%d", resolved.User, resolved.Host, resolved.Port)
	return res, nil
}

// SSHInstallCA opens the Interseptor mobileconfig on the device for manual install.
func SSHInstallCA(opts SSHOpts, profileURL string) (*SSHResult, error) {
	profileURL = strings.TrimSpace(profileURL)
	if profileURL == "" {
		return nil, errors.New("profile URL is required")
	}
	resolved, err := resolveSSHOpts(opts)
	if err != nil {
		return nil, err
	}
	client, err := sshDial(resolved)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	method, steps, err := openProfileOnDevice(client, profileURL)
	if err != nil {
		return nil, err
	}
	res := &SSHResult{
		Host:            resolved.Host,
		User:            resolved.User,
		Port:            resolved.Port,
		Reachable:       true,
		Authenticated:   true,
		ProfileURL:      profileURL,
		Steps:           steps,
		Method:          method,
		NeedsUserAction: true,
		Message:         trustInstallMessage(),
	}
	return res, nil
}

// SSHSetup installs the configuration profile (CA + global HTTP proxy) via SSH.
func SSHSetup(opts SSHSetupOpts) (*SSHResult, error) {
	if opts.ProxyPort <= 0 || opts.ProxyPort > 65535 {
		return nil, fmt.Errorf("invalid proxy port %d", opts.ProxyPort)
	}
	proxyHost := strings.TrimSpace(opts.ProxyHost)
	if proxyHost == "" {
		var err error
		proxyHost, err = lanHost()
		if err != nil {
			return nil, err
		}
	}
	profileURL := BuildProfileURL(opts.ProfileURL, proxyHost, opts.ProxyPort)
	res, err := SSHInstallCA(opts.SSHOpts, profileURL)
	if err != nil {
		return nil, err
	}
	res.Proxy = fmt.Sprintf("%s:%d", proxyHost, opts.ProxyPort)
	res.Steps = append([]string{"proxy target " + res.Proxy}, res.Steps...)
	res.Message = trustInstallMessage() + " The profile also sets the global HTTP proxy to " + res.Proxy + "."
	res.Warning = "SSL pinning is not bypassed — use Frida or a patched IPA if the app still fails TLS."
	return res, nil
}

// BuildProfileURL returns the control-plane mobileconfig download URL.
func BuildProfileURL(baseURL, proxyHost string, proxyPort int) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	url := base + "/api/ios/profile.mobileconfig"
	if proxyHost != "" && proxyPort > 0 {
		url += "?host=" + proxyHost + "&port=" + strconv.Itoa(proxyPort)
	}
	return url
}

func trustInstallMessage() string {
	return "Profile install UI opened on device — tap Install, then Settings → General → VPN & Device Management → trust the profile → Settings → General → About → Certificate Trust Settings → enable full trust for Interseptor CA. Interseptor cannot enable full trust silently."
}

func resolveSSHOpts(opts SSHOpts) (SSHOpts, error) {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		return SSHOpts{}, errors.New("SSH host is required — set the jailbroken device IP")
	}
	user := strings.TrimSpace(opts.User)
	if user == "" {
		user = defaultSSHUser
	}
	port := opts.Port
	if port <= 0 {
		port = defaultSSHPort
	}
	if strings.TrimSpace(opts.Password) == "" && strings.TrimSpace(opts.KeyPath) == "" && len(opts.KeyPEM) == 0 {
		return SSHOpts{}, errors.New("SSH password or private key is required")
	}
	return SSHOpts{
		Host:     host,
		Port:     port,
		User:     user,
		Password: opts.Password,
		KeyPath:  strings.TrimSpace(opts.KeyPath),
		KeyPEM:   opts.KeyPEM,
	}, nil
}

func dialSSH(opts SSHOpts) (*ssh.Client, error) {
	resolved, err := resolveSSHOpts(opts)
	if err != nil {
		return nil, err
	}
	auths, err := sshAuthMethods(resolved)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            resolved.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         sshConnectTimeout,
	}
	addr := net.JoinHostPort(resolved.Host, strconv.Itoa(resolved.Port))
	return ssh.Dial("tcp", addr, cfg)
}

func sshAuthMethods(opts SSHOpts) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	if opts.Password != "" {
		auths = append(auths, ssh.Password(opts.Password))
	}
	keyPEM := opts.KeyPEM
	if len(keyPEM) == 0 && opts.KeyPath != "" {
		data, err := os.ReadFile(opts.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH private key %q: %w", opts.KeyPath, err)
		}
		keyPEM = data
	}
	if len(keyPEM) > 0 {
		signer, err := parsePrivateKey(keyPEM, opts.Password)
		if err != nil {
			return nil, err
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if len(auths) == 0 {
		return nil, errors.New("SSH password or private key is required")
	}
	return auths, nil
}

func parsePrivateKey(pem []byte, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
		if err == nil {
			return signer, nil
		}
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key: %w", err)
	}
	return signer, nil
}

// runSSHCommand executes cmd over an established SSH session and bounds the
// wait with sshCommandTimeout. The ssh package's Session type has no
// deadline/context support, so CombinedOutput runs in a goroutine and the
// caller selects on it vs. a timer; on timeout the session is closed
// (best-effort — this cannot force-kill the remote process, but it does
// guarantee the HTTP handler goroutine unblocks).
func runSSHCommand(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return string(r.out), fmt.Errorf("%s: %w: %s", cmd, r.err, strings.TrimSpace(string(r.out)))
		}
		return string(r.out), nil
	case <-time.After(sshCommandTimeout):
		_ = sess.Close()
		return "", fmt.Errorf("%s: timed out after %s waiting for the remote command", cmd, sshCommandTimeout)
	}
}

func openProfileOnDevice(client *ssh.Client, profileURL string) (method string, steps []string, err error) {
	quoted := shellQuote(profileURL)
	hasCurl, _ := remoteHasTool(client, "curl")
	hasWget, _ := remoteHasTool(client, "wget")

	if hasCurl {
		cmd := fmt.Sprintf("curl -fsSL -o %s %s && uiopen file://%s", profileRemotePath, quoted, profileRemotePath)
		if _, err := sshRunCmd(client, cmd); err == nil {
			return "curl-uiopen", []string{"downloaded profile with curl", "opened profile with uiopen"}, nil
		}
		steps = append(steps, "curl download failed — falling back to uiopen URL")
	}
	if hasWget {
		cmd := fmt.Sprintf("wget -q -O %s %s && uiopen file://%s", profileRemotePath, quoted, profileRemotePath)
		if _, err := sshRunCmd(client, cmd); err == nil {
			return "wget-uiopen", []string{"downloaded profile with wget", "opened profile with uiopen"}, nil
		}
		steps = append(steps, "wget download failed — falling back to uiopen URL")
	}
	cmd := "uiopen " + quoted
	if _, err := sshRunCmd(client, cmd); err != nil {
		return "", steps, fmt.Errorf("open profile on device: %w", err)
	}
	steps = append(steps, "opened profile URL with uiopen")
	return "uiopen-url", steps, nil
}

func remoteHasTool(client *ssh.Client, name string) (bool, error) {
	out, err := sshRunCmd(client, "command -v "+name+" 2>/dev/null")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
