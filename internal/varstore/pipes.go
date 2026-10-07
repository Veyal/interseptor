package varstore

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // md5 is an explicit user-requested transform, not a security control
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
)

// applyPipe transforms v by one pipe ("b64", "hmac:key"...). ok is false for
// an unknown pipe.
func applyPipe(v, pipe string) (string, bool) {
	name, arg, _ := strings.Cut(pipe, ":")
	switch strings.TrimSpace(name) {
	case "b64":
		return base64.StdEncoding.EncodeToString([]byte(v)), true
	case "b64url":
		return base64.RawURLEncoding.EncodeToString([]byte(v)), true
	case "urlenc":
		return url.QueryEscape(v), true
	case "json":
		b, _ := json.Marshal(v)
		return string(b[1 : len(b)-1]), true
	case "md5":
		s := md5.Sum([]byte(v)) //nolint:gosec
		return hex.EncodeToString(s[:]), true
	case "sha256":
		s := sha256.Sum256([]byte(v))
		return hex.EncodeToString(s[:]), true
	case "hmac":
		m := hmac.New(sha256.New, []byte(arg))
		m.Write([]byte(v))
		return hex.EncodeToString(m.Sum(nil)), true
	case "upper":
		return strings.ToUpper(v), true
	case "lower":
		return strings.ToLower(v), true
	case "trim":
		return strings.TrimSpace(v), true
	}
	return "", false
}

func pathEscape(s string) string { return url.PathEscape(s) }
