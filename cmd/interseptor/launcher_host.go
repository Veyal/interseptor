package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// launcherHostGuard defeats DNS rebinding against the launcher: a request
// whose Host is not loopback (or the explicitly configured bind host) is
// refused before any route runs — GET / embeds the launcher token — and a
// mutating request with an Origin naming a different host is refused too.
func (lh *launcherServer) launcherHostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lh.allowedLauncherHost(r.Host) {
			launcherErr(w, http.StatusForbidden, "launcher only answers loopback Host headers")
			return
		}
		if launcherMutating(r.Method) && !sameHostOrigin(r.Header.Get("Origin"), r.Host) {
			launcherErr(w, http.StatusForbidden, "cross-origin launcher request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedLauncherHost accepts loopback hosts and the configured bind host.
func (lh *launcherServer) allowedLauncherHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return lh.bindHost != "" && strings.EqualFold(host, lh.bindHost)
}

func launcherMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameHostOrigin reports whether an Origin header is absent or names exactly
// the request's Host (scheme-agnostic host:port comparison).
func sameHostOrigin(origin, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// launcherBindHost returns the host part of a non-loopback, non-wildcard bind
// address so requests addressed to it are accepted; "" otherwise.
func launcherBindHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return host
}
