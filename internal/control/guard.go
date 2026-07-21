package control

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// securityGuard protects the control plane. It has two trust modes:
//
//   - Loopback trust (unchanged, the local default): a request that arrives on a
//     loopback connection with a loopback Host and no API key is allowed, exactly
//     as before. The embedded UI, curl, and the in-process MCP tool bus all reach
//     us this way. DNS-rebinding is defeated by the loopback-Host requirement and
//     classic CSRF by the loopback-Origin check.
//   - Key trust (remote access): a request carrying a VALID API key is authorized
//     regardless of Host/Origin/connection — this is what lets an AI agent on a VPS
//     or a collaborator's browser reach Interseptor over a Cloudflare tunnel. A
//     read-only key may only read (GET/HEAD + SSE); a full key may also mutate. The
//     cookie path (browser login) additionally requires an anti-CSRF header and a
//     same-origin Origin on mutations, since a cookie is an ambient credential.
//
// A non-loopback request WITHOUT a valid key is closed (401, or a redirect to the
// login page for a browser navigation) — so accidentally exposing the port never
// leaks the captured pentest data.
//
// maxRequestBody bounds every control request body as a DoS backstop (see below).
var maxRequestBody int64 = 128 << 20

// sessionCookie is the name of the httpOnly cookie holding a browser session's
// API token (set by the login endpoint, cleared by logout).
const sessionCookie = "ick_session"

func (h *Hub) securityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The OOB interaction catcher is deliberately public: blind callbacks from a
		// target arrive with a foreign Host/Origin and no auth. It only records
		// request metadata (no control actions), so it bypasses all checks.
		if strings.HasPrefix(r.URL.Path, "/oob/") {
			next.ServeHTTP(w, r)
			return
		}
		// Pre-auth surface: the login page and the session endpoints must be
		// reachable before a remote browser has a session. They mint/clear the
		// cookie themselves and are rate-limited; they perform no privileged action.
		if r.URL.Path == "/login" || r.URL.Path == "/api/session/auth" || r.URL.Path == "/api/session/logout" {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			next.ServeHTTP(w, r)
			return
		}

		tok, via := authToken(r)
		keyOK, scope := false, ""
		if tok != "" && h.st != nil {
			keyOK, scope, _ = h.st.VerifyAPIKeyScope(tok)
		}

		loopbackConn := true
		if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && !isLoopbackAddr(la) {
			loopbackConn = false
		}
		loopbackHost := isLoopbackHost(r.Host)

		switch {
		case keyOK:
			// Authenticated remote (or local) client. Bypass the loopback Host/Origin
			// checks; enforce scope, MCP tier, and CSRF for the ambient-cookie path.
			if isMutatingMethod(r.Method) {
				if scope == store.ScopeRead {
					httpErr(w, http.StatusForbidden, "this key is read-only — it may view but not modify")
					return
				}
				if via == authViaCookie {
					// Accept both spellings: the rename Interceptor→Interseptor left
					// some clients briefly sending the old header name.
					if !csrfHeaderOK(r) {
						httpErr(w, http.StatusForbidden, "missing X-Interseptor-CSRF header")
						return
					}
					if o := r.Header.Get("Origin"); o != "" && !sameHostOrigin(o, r.Host) {
						httpErr(w, http.StatusForbidden, "cross-origin request rejected")
						return
					}
				}
			}
			// The MCP tool surface requires a FULL-scope key: a read-only watcher
			// gets no tool access (they observe via the UI + SSE). This also sidesteps
			// MCP's loopback re-entrancy — MCP tool calls re-enter as unauthenticated
			// loopback POSTs, so scope can't be re-checked there.
			if r.URL.Path == "/mcp" && scope != store.ScopeFull {
				w.Header().Set("WWW-Authenticate", `Bearer realm="interseptor"`)
				httpErr(w, http.StatusForbidden, "the MCP endpoint requires a full-access key")
				return
			}
			// A URL query token (?token=) is only honored for the SSE stream, whose
			// EventSource client cannot set an Authorization header; everywhere else a
			// query token is refused so tokens don't leak through general URL logging.
			if via == authViaQuery && r.URL.Path != "/api/events" {
				httpErr(w, http.StatusForbidden, "a URL token is only valid for the event stream")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			next.ServeHTTP(w, r)
			return

		case loopbackConn && loopbackHost:
			// Unauthenticated loopback: the legacy local-trust path, byte-for-byte
			// unchanged so local development keeps working with no login.
			if o := r.Header.Get("Origin"); o != "" && !isLoopbackOrigin(o) {
				httpErr(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			if r.URL.Path == "/mcp" && !h.mcpAuthorized(r) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="interseptor"`)
				httpErr(w, http.StatusUnauthorized, "the MCP endpoint requires Authorization: Bearer <api key> — create one in the API tab, or remove all keys to disable auth")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			next.ServeHTTP(w, r)
			return

		case h.st != nil && h.st.AllowlistMatch(clientIP(r)):
			// Machine-global IP/CIDR allowlist (Settings → API → Allowlist). Full
			// UI/REST trust without a key. /mcp still requires a full API key.
			if r.URL.Path == "/mcp" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="interseptor"`)
				httpErr(w, http.StatusUnauthorized, "the MCP endpoint requires Authorization: Bearer <api key> — IP allowlist does not cover /mcp")
				return
			}
			if isMutatingMethod(r.Method) {
				if o := r.Header.Get("Origin"); o != "" && !sameHostOrigin(o, r.Host) {
					httpErr(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			next.ServeHTTP(w, r)
			return

		default:
			// Non-loopback connection (or spoofed Host) without a valid key: closed.
			// A browser navigation is redirected to the login page; anything else
			// (fetch/curl/MCP) gets a 401 so the client can present a key.
			w.Header().Set("WWW-Authenticate", `Bearer realm="interseptor"`)
			if tok == "" && wantsHTML(r) {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			httpErr(w, http.StatusUnauthorized, "remote access to Interseptor requires an API key — open /login or send Authorization: Bearer <key>")
			return
		}
	})
}

// auth token sources, in precedence order.
const (
	authViaBearer = "bearer"
	authViaCookie = "cookie"
	authViaQuery  = "query"
)

// authToken extracts the presented API token and how it arrived. Precedence:
// Authorization: Bearer header (curl / MCP / pull-push), then the session cookie
// (browser login), then a ?token= query param (only meaningful for SSE).
func authToken(r *http.Request) (token, via string) {
	if t := bearerToken(r); t != "" {
		return t, authViaBearer
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value, authViaCookie
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t, authViaQuery
	}
	return "", ""
}

// csrfHeaderOK reports whether the request carries the anti-CSRF marker used by
// cookie-authenticated browser sessions. Accepts both Interseptor (current) and
// Interceptor (pre-rename) spellings so a mismatched UI/binary pair still works.
func csrfHeaderOK(r *http.Request) bool {
	return r.Header.Get("X-Interseptor-CSRF") == "1" || r.Header.Get("X-Interceptor-CSRF") == "1"
}

// isMutatingMethod reports whether an HTTP method can change server state.
func isMutatingMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// wantsHTML reports whether a request looks like a browser navigation (so an
// unauthenticated one should be redirected to the login page rather than 401'd).
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// sameHostOrigin reports whether an Origin header's host matches the request Host
// (scheme/port aside for the host comparison — the tunnel terminates TLS upstream
// so the proxied Host and Origin host agree).
func sameHostOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	oh := u.Hostname()
	rh := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		rh = h
	}
	return strings.EqualFold(oh, rh)
}

// mcpAuthorized reports whether a request to /mcp may proceed. Auth is opt-in:
// with no API keys the endpoint is open (loopback trust); once any key exists a
// valid bearer token is required. On a store error it falls back to the last-known
// key state (mcpKeysSeen) — so a keyless install stays open through a transient DB
// hiccup, but once keys are known to exist the endpoint fails CLOSED rather than
// briefly dropping auth.
func (h *Hub) mcpAuthorized(r *http.Request) bool {
	has, err := h.st.HasAPIKeys()
	if err != nil {
		has = h.mcpKeysSeen.Load()
	} else {
		h.mcpKeysSeen.Store(has)
	}
	if !has {
		return true
	}
	tok := bearerToken(r)
	if tok == "" {
		return false
	}
	ok, _ := h.st.VerifyAPIKey(tok)
	return ok
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) string {
	const pfx = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) >= len(pfx) && strings.EqualFold(v[:len(pfx)], pfx) {
		return strings.TrimSpace(v[len(pfx):])
	}
	return ""
}

// isLoopbackHost reports whether a Host header (host or host:port) names the
// loopback interface: 127.0.0.0/8, ::1, or "localhost". An empty host (e.g. a
// bare ":8080", meaning all interfaces) is not loopback.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackAddr reports whether a net.Addr (the connection's server-side local
// address) names the loopback interface. Unlike the Host header this is set by
// the kernel from the accepting socket, so a remote client cannot spoof it.
func isLoopbackAddr(a net.Addr) bool {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		host = a.String()
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackOrigin reports whether an Origin header value points at loopback.
// A malformed or opaque origin (e.g. "null") is treated as non-loopback.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHost(u.Host)
}
