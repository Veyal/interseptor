package ios

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

// ProfileOpts configures an unsigned configuration profile (CA + global HTTP proxy).
type ProfileOpts struct {
	DisplayName  string
	ProxyHost    string
	ProxyPort    int
	Organization string
}

// BuildMobileConfig returns a .mobileconfig plist installing the CA and manual HTTP proxy.
func BuildMobileConfig(certPEM []byte, opts ProfileOpts) ([]byte, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid CA PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA: %w", err)
	}
	if opts.ProxyHost == "" || opts.ProxyPort <= 0 || opts.ProxyPort > 65535 {
		return nil, fmt.Errorf("invalid proxy %q:%d", opts.ProxyHost, opts.ProxyPort)
	}
	name := opts.DisplayName
	if name == "" {
		name = "Interseptor"
	}
	org := opts.Organization
	if org == "" {
		org = "Interseptor"
	}
	rootUUID := newPayloadUUID()
	proxyUUID := newPayloadUUID()
	payloadUUID := newPayloadUUID()
	certB64 := base64.StdEncoding.EncodeToString(block.Bytes)
	certName := strings.TrimSuffix(cert.Subject.CommonName, "")
	if certName == "" {
		certName = "Interseptor CA"
	}

	// Unsigned profile — user installs via Safari; must enable full trust for the CA.
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>PayloadCertificateFileName</key>
      <string>interseptor-ca.crt</string>
      <key>PayloadContent</key>
      <data>%s</data>
      <key>PayloadDescription</key>
      <string>Trust the Interseptor CA to decrypt HTTPS during testing.</string>
      <key>PayloadDisplayName</key>
      <string>%s Root CA</string>
      <key>PayloadIdentifier</key>
      <string>com.interseptor.ca.%s</string>
      <key>PayloadType</key>
      <string>com.apple.security.root</string>
      <key>PayloadUUID</key>
      <string>%s</string>
      <key>PayloadVersion</key>
      <integer>1</integer>
    </dict>
    <dict>
      <key>PayloadDescription</key>
      <string>Route HTTP/HTTPS through the Interseptor proxy.</string>
      <key>PayloadDisplayName</key>
      <string>Interseptor Proxy</string>
      <key>PayloadIdentifier</key>
      <string>com.interseptor.proxy.%s</string>
      <key>PayloadType</key>
      <string>com.apple.proxy.http.global</string>
      <key>PayloadUUID</key>
      <string>%s</string>
      <key>PayloadVersion</key>
      <integer>1</integer>
      <key>ProxyCaptiveLoginAllowed</key>
      <false/>
      <key>ProxyServer</key>
      <string>%s</string>
      <key>ProxyServerPort</key>
      <integer>%d</integer>
      <key>ProxyType</key>
      <string>Manual</string>
    </dict>
  </array>
  <key>PayloadDescription</key>
  <string>Install Interseptor CA and HTTP proxy for mobile app testing.</string>
  <key>PayloadDisplayName</key>
  <string>%s</string>
  <key>PayloadIdentifier</key>
  <string>com.interseptor.profile.%s</string>
  <key>PayloadRemovalDisallowed</key>
  <false/>
  <key>PayloadType</key>
  <string>Configuration</string>
  <key>PayloadUUID</key>
  <string>%s</string>
  <key>PayloadVersion</key>
  <integer>1</integer>
  <key>PayloadOrganization</key>
  <string>%s</string>
</dict>
</plist>`,
		certB64, xmlEsc(certName), rootUUID, rootUUID,
		proxyUUID, proxyUUID, xmlEsc(opts.ProxyHost), opts.ProxyPort,
		xmlEsc(name), payloadUUID, payloadUUID, xmlEsc(org),
	)), nil
}

func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func newPayloadUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// SetupOpts configures one-click iOS intercept setup.
type SetupOpts struct {
	Target    string // simulator | physical | auto
	ProxyMode string // localhost (simulator) | wifi (physical, default)
	WiFiHost  string
	Port      int
	ProfileURL string // control plane URL prefix for profile download
}

// SetupResult is returned from Setup.
type SetupResult struct {
	UDID            string   `json:"udid"`
	Kind            string   `json:"kind"`
	Proxy           string   `json:"proxy,omitempty"`
	ProfileURL      string   `json:"profileUrl,omitempty"`
	Steps           []string `json:"steps"`
	NeedsUserAction bool     `json:"needsUserAction,omitempty"`
	Message         string   `json:"message"`
}

// Setup automates what iOS allows: simulator CA via simctl; all targets get a profile URL.
func Setup(d Device, certPEM []byte, opts SetupOpts) (*SetupResult, error) {
	if opts.Port <= 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("invalid proxy port %d", opts.Port)
	}
	res := &SetupResult{UDID: d.UDID, Kind: d.Kind}

	proxyHost := "127.0.0.1"
	mode := strings.ToLower(strings.TrimSpace(opts.ProxyMode))
	if mode == "" {
		if d.Kind == KindPhysical {
			mode = "wifi"
		} else {
			mode = "localhost"
		}
	}
	if mode == "wifi" {
		host := strings.TrimSpace(opts.WiFiHost)
		if host == "" {
			var err error
			host, err = lanHost()
			if err != nil {
				return nil, err
			}
		}
		proxyHost = host
	}
	res.Proxy = fmt.Sprintf("%s:%d", proxyHost, opts.Port)

	if d.Kind == KindSimulator && SimctlAvailable() {
		if err := InstallCASimulator(d, certPEM); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "simulator root CA installed via simctl")
	}

	profileURL := strings.TrimRight(opts.ProfileURL, "/") + "/api/ios/profile.mobileconfig"
	if proxyHost != "127.0.0.1" {
		profileURL += "?host=" + proxyHost + "&port=" + fmt.Sprint(opts.Port)
	}
	res.ProfileURL = profileURL

	if d.Kind == KindSimulator && SimctlAvailable() {
		if err := OpenURL(d, profileURL); err != nil {
			res.Steps = append(res.Steps, "open profile URL in simulator failed: "+err.Error())
		} else {
			res.Steps = append(res.Steps, "opened profile install URL in simulator Safari")
		}
	}

	res.NeedsUserAction = true
	switch d.Kind {
	case KindSimulator:
		res.Message = "Simulator CA installed — in Safari tap Allow → install profile → Settings → General → VPN & Device Management → trust profile. Then Settings → General → About → Certificate Trust Settings → enable full trust for Interseptor CA."
	default:
		res.Message = "Open the profile URL on the iPhone (same Wi‑Fi as this machine), install the profile, enable certificate trust, then browse the app. SSL pinning still requires Frida or a patched IPA on device."
	}
	return res, nil
}
