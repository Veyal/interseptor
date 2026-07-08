package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Browser session auth. A remote collaborator opens /login, submits an API key,
// and receives an httpOnly session cookie so subsequent same-origin fetches and
// the EventSource stream authenticate automatically (EventSource cannot set an
// Authorization header). The cookie is SameSite=Strict + Secure (over the tunnel)
// and mutations additionally require the X-Interseptor-CSRF header (see guard.go).

// loginRateLimiter throttles /api/session/auth per remote IP so the 192-bit key
// space can't be brute-forced online. A minimal fixed-window counter is enough.
type loginRateLimiter struct {
	mu     sync.Mutex
	window map[string]*rlEntry
}

type rlEntry struct {
	count int
	reset int64 // unix millis when the window resets
}

var loginRL = &loginRateLimiter{window: map[string]*rlEntry{}}

// allow reports whether another login attempt from ip is permitted (max ~10 per
// minute). It also opportunistically prunes expired windows.
func (l *loginRateLimiter) allow(ip string, nowMs int64) bool {
	const maxPerWindow = 10
	const windowMs = 60_000
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.window[ip]
	if e == nil || nowMs >= e.reset {
		l.window[ip] = &rlEntry{count: 1, reset: nowMs + windowMs}
		// Opportunistic prune to bound the map.
		for k, v := range l.window {
			if nowMs >= v.reset {
				delete(l.window, k)
			}
		}
		return true
	}
	if e.count >= maxPerWindow {
		return false
	}
	e.count++
	return true
}

// sessionLogin verifies a submitted API token and, on success, sets the session
// cookie. Returns the granted scope so the UI can adapt (read-only vs full).
func (h *Hub) sessionLogin(w http.ResponseWriter, r *http.Request) {
	if !loginRL.allow(remoteIP(r), time.Now().UnixMilli()) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts — wait a minute and retry")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	ok, scope, err := h.st.VerifyAPIKeyScope(in.Token)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpErr(w, http.StatusUnauthorized, "invalid or expired key")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    in.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   12 * 60 * 60, // 12h; the key's own expiry still applies server-side
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scope": scope})
}

// sessionLogout clears the session cookie.
func (h *Hub) sessionLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// serveLogin serves the login page (a minimal embedded HTML form).
func (h *Hub) serveLogin(w http.ResponseWriter, r *http.Request) {
	data, err := uiFS.ReadFile("ui/login.html")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "login page unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// requestIsHTTPS reports whether the browser-facing connection is HTTPS. The
// control plane itself is plain HTTP on loopback, but a tunnel (Cloudflare) or a
// reverse proxy terminates TLS and forwards X-Forwarded-Proto: https — so a Secure
// cookie is correct for remote clients and omitted for plain-HTTP localhost.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// remoteIP returns the client IP (host portion of RemoteAddr) for rate limiting.
func remoteIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		return addr[:i]
	}
	return addr
}
