// Package csrf contains small, reusable CSRF helpers for request analysis.
package csrf

import (
	"encoding/json"
	"net/url"
	"strings"
)

// XSRFFromCookie decodes a Laravel XSRF-TOKEN cookie into its header value.
func XSRFFromCookie(cookieLine string) string {
	for _, part := range strings.Split(cookieLine, ";") {
		part = strings.TrimSpace(part)
		key, value, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "XSRF-TOKEN") {
			continue
		}
		value, _ = url.QueryUnescape(strings.TrimSpace(value))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "{") {
			var body struct {
				Token string `json:"token"`
			}
			if json.Unmarshal([]byte(value), &body) == nil && body.Token != "" {
				return body.Token
			}
		}
		return value
	}
	return ""
}
