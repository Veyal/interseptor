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
// continuations. Content-Length is dropped (curl computes it), and the headers
// curl would add by itself are suppressed unless the request carried them, so
// the command sends the captured request and nothing else.
func Build(method, url string, headers http.Header, body []byte) string {
	var parts []string
	// -g: curl otherwise expands [] and {} in the URL as glob ranges, so a
	// captured "ids[]=1" is rejected and "{a,b}" becomes two requests.
	parts = append(parts, "curl --path-as-is -k -g")

	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = http.MethodGet
	}
	// curl sends --data-raw as POST unless told otherwise, so a GET with a
	// body needs the method spelled out too.
	switch {
	case m == http.MethodHead && len(body) == 0:
		// "-X HEAD" only renames the method: curl would still wait for the
		// body the response headers announce. -I is curl's real HEAD.
		parts = append(parts, "-I")
	case m != http.MethodGet || len(body) > 0:
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
	// curl adds these on its own (Content-Type only with a body). A captured
	// request that did not carry one is replayed without it: "Name:" is curl's
	// spelling for "do not send Name".
	for _, name := range []string{"Accept", "Content-Type", "User-Agent"} {
		if name == "Content-Type" && len(body) == 0 {
			continue
		}
		if !hasHeader(headers, name) {
			parts = append(parts, "-H '"+name+":'")
		}
	}
	for _, k := range keys {
		for _, v := range headers[k] {
			// curl reads "Name:" (empty value) as "do not send Name" — it even
			// suppresses the header curl would add itself. "Name;" is curl's
			// spelling for a header sent with an empty value, so an empty value
			// replayed in a capture has to use it.
			if v == "" {
				parts = append(parts, "-H '"+shellEscape(k+";")+"'")
				continue
			}
			parts = append(parts, "-H '"+shellEscape(k+": "+v)+"'")
		}
	}

	if len(body) > 0 {
		parts = append(parts, "--data-raw '"+shellEscape(string(body))+"'")
	}

	return strings.Join(parts, " \\\n  ")
}

// hasHeader reports whether headers carries name under any capitalisation
// (captured headers keep the case they were sent with).
func hasHeader(headers http.Header, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// shellEscape makes s safe inside single quotes by closing the quote, emitting
// an escaped quote, and reopening: ' → '\”.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
