package capture

import (
	"strings"
)

// authKeywords is the set of path segments (lowercase) that identify auth
// endpoints. Matching is segment-exact or substring-of-path for multi-word
// keywords that naturally appear as part of longer segments (e.g. "oauth",
// "token"). All comparisons are done on lowercase input.
//
// The keywords were chosen conservatively:
//   - They appear as complete path segments on real auth endpoints.
//   - None are substrings of common non-auth words that would cause false
//     positives (e.g. "log" ⊂ "blog", "dialog" — so we use "login" not "log").
var authKeywords = map[string]struct{}{
	"login":    {},
	"logout":   {},
	"signin":   {},
	"signout":  {},
	"signup":   {},
	"register": {},
	"auth":     {},
	"oauth":    {},
	"oauth2":   {},
	"token":    {},
	"sso":      {},
	"saml":     {},
	"password": {},
	"reset":    {},
	"mfa":      {},
	"2fa":      {},
	"totp":     {},
	"verify":   {},
}

// isAuthPath reports whether path looks like an authentication endpoint.
//
// Rules (applied after lowercasing):
//  1. Segment-exact match: any "/" -delimited segment that exactly equals one
//     of the auth keywords triggers a match. This avoids false positives from
//     naive substring matching — "/blog" contains "log" but its segments are
//     ["blog"], which does not equal "login", "logout", etc.
//  2. Substring match for multi-component keywords: "oauth" and "oauth2" also
//     match when they appear as a leading/embedded component of a longer
//     segment (e.g. "/oauth2/authorize", "/oauth/callback"). They are
//     specifically listed in authSubstrings for this looser check.
//
// This function is allocation-free for the common (non-auth) case: it
// iterates the path without allocating a slice of segments.
func isAuthPath(path string) bool {
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)

	// Walk segments delimited by '/'. We avoid strings.Split to skip the
	// allocation on every proxied request.
	start := 0
	for i := 0; i <= len(lower); i++ {
		if i == len(lower) || lower[i] == '/' || lower[i] == '?' {
			seg := lower[start:i]
			if seg != "" {
				if _, ok := authKeywords[seg]; ok {
					return true
				}
				// Looser check for "oauth" / "oauth2" — they commonly appear
				// embedded (e.g. "oauth2" or "oauth2_redirect").
				if strings.HasPrefix(seg, "oauth") {
					return true
				}
			}
			if i < len(lower) && lower[i] == '?' {
				break // query string — no further segments
			}
			start = i + 1
		}
	}
	return false
}

// TagIfAuth calls st.AddFlowTags(id, ["auth"]) when the given path looks like
// an auth endpoint. The call is best-effort: any error is silently ignored so
// the capture/forwarding path is never affected. It is safe to call
// concurrently and is a no-op when path does not match.
func (c *Capturer) TagIfAuth(id int64, path string) {
	if id == 0 || !isAuthPath(path) {
		return
	}
	_, _ = c.st.AddFlowTags(id, []string{"auth"})
}
