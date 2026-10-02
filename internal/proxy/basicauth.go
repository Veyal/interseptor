package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
)

// BasicAuth is an optional proxy-listener credential. Disabled (the default)
// forwards every request. When enabled, the listener requires
// Proxy-Authorization: Basic with the configured username and password — not
// an API key. History and the control API stay on their own authorization.
type BasicAuth struct {
	mu      sync.RWMutex
	enabled bool
	user    string
	pass    string
}

// Set replaces the live credential. An enabled config with an empty username
// or password fails closed: every request is challenged.
func (a *BasicAuth) Set(enabled bool, user, pass string) {
	a.mu.Lock()
	a.enabled, a.user, a.pass = enabled, user, pass
	a.mu.Unlock()
}

// Handler returns a listener wrapper. The returned handler observes later Set
// calls, so Settings can change the credential without rebinding the listener.
func (a *BasicAuth) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		enabled, user, pass := a.enabled, a.user, a.pass
		a.mu.RUnlock()
		if !enabled {
			next.ServeHTTP(w, r)
			return
		}
		gotUser, gotPass, ok := proxyBasicCredentials(r)
		if !ok || user == "" || pass == "" || !proxyCredentialsMatch(user, pass, gotUser, gotPass) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="interseptor"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		r.Header.Del("Proxy-Authorization")
		next.ServeHTTP(w, r)
	})
}

func proxyBasicCredentials(r *http.Request) (string, string, bool) {
	value := r.Header.Get("Proxy-Authorization")
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", "", false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	return user, pass, ok
}

func proxyCredentialsMatch(user, pass, gotUser, gotPass string) bool {
	want := sha256.Sum256([]byte(user + "\x00" + pass))
	got := sha256.Sum256([]byte(gotUser + "\x00" + gotPass))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
