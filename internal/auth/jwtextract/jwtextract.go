// Package jwtextract pulls JWT-shaped tokens from HTTP flows (Bearer header,
// JSON fields, URL path/query, cookies) for cross-host replay and SSO testing.
package jwtextract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Input is the request/response material to search.
type Input struct {
	ReqHeaders map[string][]string
	Path       string
	RawQuery   string
	ReqBody    []byte
	ResBody    []byte
}

var jwtRe = regexp.MustCompile(`[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// Extract returns the first JWT found and a source label (e.g. "bearer", "json:login_token").
func Extract(in Input) (token, source string) {
	if t := bearer(in.ReqHeaders); t != "" {
		return t, "bearer"
	}
	if t, src := fromJSON(in.ReqBody); t != "" {
		return t, src
	}
	if t, src := fromJSON(in.ResBody); t != "" {
		return t, src
	}
	if t, ok := fromPath(in.Path); ok {
		return t, "path"
	}
	if t, src := fromQuery(in.RawQuery); t != "" {
		return t, src
	}
	if t, src := fromCookies(in.ReqHeaders); t != "" {
		return t, src
	}
	return "", ""
}

// SourceUsesPath reports whether replay should preserve/inject the JWT in the URL path.
func SourceUsesPath(source string) bool {
	return source == "path" || strings.HasSuffix(source, ":path") || strings.HasSuffix(source, ":embedded")
}

func bearer(hdrs map[string][]string) string {
	if hdrs == nil {
		return ""
	}
	for _, v := range headerValues(hdrs, "Authorization") {
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			t := strings.TrimSpace(v[7:])
			if looksLikeJWT(t) {
				return t
			}
		}
	}
	return ""
}

// headerValues returns the values of a header whatever the case of its stored
// name. Captures keep the spelling they arrived with (lower-case for HTTP/2
// and imported flows), so an exact-key lookup missed their tokens. The
// canonical spelling comes first so the result is stable.
func headerValues(hdrs map[string][]string, name string) []string {
	out := append([]string(nil), hdrs[name]...)
	var others []string
	for k := range hdrs {
		if k != name && strings.EqualFold(k, name) {
			others = append(others, k)
		}
	}
	sort.Strings(others)
	for _, k := range others {
		out = append(out, hdrs[k]...)
	}
	return out
}

var jsonKeys = []string{"login_token", "token", "access_token", "jwt", "id_token", "login_url"}

func fromJSON(body []byte) (token, source string) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 || body[0] != '{' {
		return "", ""
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return "", ""
	}
	for _, k := range jsonKeys {
		v, ok := m[k]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmtString(v))
		if s == "" {
			continue
		}
		if k == "login_url" {
			if u, err := url.Parse(s); err == nil && u.Path != "" {
				if t, ok := fromPath(u.Path); ok {
					return t, "json:login_url:path"
				}
				if t := findJWT(u.String()); t != "" {
					return t, "json:login_url:embedded"
				}
			}
		}
		if looksLikeJWT(s) {
			return s, "json:" + k
		}
		if t := findJWT(s); t != "" {
			return t, "json:" + k + ":embedded"
		}
	}
	return "", ""
}

func fromPath(path string) (string, bool) {
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		if looksLikeJWT(seg) {
			return seg, true
		}
		if t := findJWT(seg); t != "" {
			return t, true
		}
	}
	return "", false
}

func fromQuery(raw string) (token, source string) {
	if raw == "" {
		return "", ""
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", ""
	}
	for _, k := range []string{"token", "jwt", "access_token", "login_token"} {
		for _, v := range q[k] {
			v = strings.TrimSpace(v)
			if looksLikeJWT(v) {
				return v, "query:" + k
			}
			if t := findJWT(v); t != "" {
				return t, "query:" + k + ":embedded"
			}
		}
	}
	return "", ""
}

func fromCookies(hdrs map[string][]string) (token, source string) {
	if hdrs == nil {
		return "", ""
	}
	for _, line := range headerValues(hdrs, "Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			k = strings.ToLower(strings.TrimSpace(k))
			v = strings.TrimSpace(v)
			if k != "token" && k != "jwt" && k != "access_token" && !strings.Contains(k, "jwt") {
				continue
			}
			if looksLikeJWT(v) {
				return v, "cookie:" + k
			}
			if t := findJWT(v); t != "" {
				return t, "cookie:" + k + ":embedded"
			}
		}
	}
	return "", ""
}

// looksLikeJWT reports whether s is a JWT-shaped token: three dot-separated
// segments whose header segment is base64url of a JSON object. Every JWT header
// is a JSON object, so the decoded text starts with "{" — checking that, rather
// than only the three-dot shape, keeps version strings ("v1.2.3") and asset
// filenames ("app.min.js") from being read as tokens.
func looksLikeJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return false
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	return bytes.HasPrefix(bytes.TrimSpace(hdr), []byte("{"))
}

// findJWT returns the first JWT-shaped token inside s, or "".
func findJWT(s string) string {
	for _, cand := range jwtRe.FindAllString(s, 8) {
		if looksLikeJWT(cand) {
			return cand
		}
	}
	return ""
}

func fmtString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		if len(b) >= 2 && b[0] == '"' {
			var s string
			if json.Unmarshal(b, &s) == nil {
				return s
			}
		}
		return string(b)
	}
}

// ReplayPath builds the path to replay on each host for path-mode JWT replay.
// If refPath already embeds the token, it is returned unchanged.
func ReplayPath(refPath, jwt string) string {
	if refPath == "" {
		refPath = "/"
	}
	if jwt != "" && strings.Contains(refPath, jwt) {
		return refPath
	}
	lower := strings.ToLower(refPath)
	for _, prefix := range []string{"/authbroker/", "/oauth/token/", "/sso/callback/", "/auth/"} {
		if i := strings.Index(lower, prefix); i >= 0 {
			return refPath[:i+len(prefix)] + jwt
		}
	}
	if jwt != "" {
		return "/authBroker/" + jwt
	}
	return refPath
}
