package control

import "net/http"

// controlCSP is the defence-in-depth policy for every control-plane response:
// the UI may not be framed by another site (clickjacking against the keyless
// loopback UI), and plugins and <base> rewrites are disabled. Script and
// style sources are not restricted yet because the shell still carries inline
// bootstrap scripts.
const controlCSP = "frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

// securityHeaders sets hardening headers before any handler runs, so they also
// cover guard refusals, the login page and the OOB catcher. Handlers may
// tighten them (see sandboxDownload).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", controlCSP)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sandboxDownload is for responses that carry target-controlled bytes with
// the target's Content-Type (e.g. a captured text/html or SVG body): opened
// directly, they render in a sandbox with no script and an opaque origin, so
// they cannot act on the control API.
func sandboxDownload(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
}
