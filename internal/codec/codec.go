// Package codec provides the small, pure encode/decode transforms behind the
// Decoder tool and the MCP `decode` tool: base64, URL, hex, HTML entities, and
// JWT inspection, plus a best-effort "smart" auto-decode.
package codec

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
)

// Ops lists every supported operation id (also the order shown in the UI).
var Ops = []string{
	"base64encode", "base64decode",
	"urlencode", "urldecode",
	"hexencode", "hexdecode",
	"htmlencode", "htmldecode",
	"jwtdecode", "smart",
}

// Apply runs one operation over s.
func Apply(op, s string) (string, error) {
	switch op {
	case "base64encode":
		return base64.StdEncoding.EncodeToString([]byte(s)), nil
	case "base64decode":
		return b64decode(s)
	case "urlencode":
		return url.QueryEscape(s), nil
	case "urldecode":
		v, err := url.QueryUnescape(s)
		if err != nil {
			return "", err
		}
		return v, nil
	case "hexencode":
		return hex.EncodeToString([]byte(s)), nil
	case "hexdecode":
		b, err := hex.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "htmlencode":
		return html.EscapeString(s), nil
	case "htmldecode":
		return html.UnescapeString(s), nil
	case "jwtdecode":
		return jwtDecode(s)
	case "smart":
		return smart(s), nil
	default:
		return "", fmt.Errorf("unknown op %q", op)
	}
}

// b64decode accepts standard or URL-safe base64, with or without padding.
func b64decode(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("not valid base64")
}

// jwtDecode splits a JWT and pretty-prints its header and payload as JSON.
func jwtDecode(s string) (string, error) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("not a JWT (expected header.payload.signature)")
	}
	seg := func(p string) string {
		b, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil {
			if b2, e2 := base64.RawStdEncoding.DecodeString(p); e2 == nil {
				b = b2
			} else {
				return p
			}
		}
		var any interface{}
		if json.Unmarshal(b, &any) == nil {
			out, _ := json.MarshalIndent(any, "", "  ")
			return string(out)
		}
		return string(b)
	}
	out := "── header ──\n" + seg(parts[0]) + "\n\n── payload ──\n" + seg(parts[1])
	if len(parts) >= 3 {
		out += "\n\n── signature ──\n" + parts[2] + "  (not verified)"
	}
	return out, nil
}

// smart makes a best-effort guess at how s is encoded and decodes one layer.
func smart(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return s
	}
	// JWT: three dot-separated base64url segments.
	if parts := strings.Split(t, "."); len(parts) == 3 && looksB64(parts[0]) && looksB64(parts[1]) {
		if out, err := jwtDecode(t); err == nil {
			return out
		}
	}
	// percent-encoding
	if strings.Contains(t, "%") {
		if v, err := url.QueryUnescape(t); err == nil && v != t {
			return v
		}
	}
	// base64 → printable
	if looksB64(t) {
		if v, err := b64decode(t); err == nil && isMostlyPrintable(v) {
			return v
		}
	}
	// hex
	if len(t)%2 == 0 && isHex(t) {
		if b, err := hex.DecodeString(t); err == nil && isMostlyPrintable(string(b)) {
			return string(b)
		}
	}
	return s
}

func looksB64(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '-' || r == '_' || r == '=') {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func isMostlyPrintable(s string) bool {
	if s == "" {
		return false
	}
	bad := 0
	for _, r := range s {
		if r == '�' || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			bad++
		}
	}
	return bad*10 < len(s) // <10% control/replacement bytes
}
