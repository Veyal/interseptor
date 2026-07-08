// Package curlgen renders a captured request as a runnable curl command, so a
// tester (or the AI) can reproduce and iterate on it in a terminal. It favors
// security-testing fidelity: the exact path is preserved (--path-as-is) and TLS
// verification is skipped (-k), matching how Interseptor itself talks to targets.
package curlgen

import (
	"net/http"
	"sort"
	"strings"
)

// Build renders a curl command for method/url with the given headers and body.
// Values are single-quoted and shell-escaped; long commands wrap with backslash
// continuations. Content-Length is dropped (curl computes it).
func Build(method, url string, headers http.Header, body []byte) string {
	var parts []string
	parts = append(parts, "curl --path-as-is -k")

	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = http.MethodGet
	}
	if m != http.MethodGet {
		parts = append(parts, "-X '"+shellEscape(m)+"'")
	}

	parts = append(parts, "'"+shellEscape(url)+"'")

	// Stable header order for reproducible output.
	keys := make([]string, 0, len(headers))
	for k := range headers {
		if http.CanonicalHeaderKey(k) == "Content-Length" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range headers[k] {
			parts = append(parts, "-H '"+shellEscape(k+": "+v)+"'")
		}
	}

	if len(body) > 0 {
		parts = append(parts, "--data-raw '"+shellEscape(string(body))+"'")
	}

	return strings.Join(parts, " \\\n  ")
}

// shellEscape makes s safe inside single quotes by closing the quote, emitting
// an escaped quote, and reopening: ' → '\”.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
