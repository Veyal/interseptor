package control

import (
	"net/url"
	"strings"
)

// Saved proxy passwords are write-only. GET /api/settings (and every MCP tool
// that echoes it to an agent) reports only whether one is saved; a client
// re-saving the masked upstream URL asks the server to keep the stored password.

// redactUpstreamPassword drops the password from a stored upstream proxy URL and
// reports whether there was one. An unparseable value is returned unchanged so
// the UI can still say it is invalid; it cannot carry a parsed password.
func redactUpstreamPassword(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, false
	}
	if _, ok := u.User.Password(); !ok {
		return raw, false
	}
	u.User = url.User(u.User.Username())
	return u.String(), true
}

// keepUpstreamPassword grafts the stored password onto next when next names a
// user without a password of its own and points at the same proxy host as the
// stored URL. Renaming the user, or changing the scheme or port, keeps the
// password (the Settings field promises "leave blank to keep"); a different
// host never inherits it. Anything else is returned as sent.
func keepUpstreamPassword(next, stored string) string {
	n, err := url.Parse(next)
	if err != nil || n.User == nil || n.User.Username() == "" {
		return next
	}
	if _, ok := n.User.Password(); ok {
		return next
	}
	s, err := url.Parse(stored)
	if err != nil || s.User == nil || !strings.EqualFold(s.Hostname(), n.Hostname()) {
		return next
	}
	pw, ok := s.User.Password()
	if !ok {
		return next
	}
	n.User = url.UserPassword(n.User.Username(), pw)
	return n.String()
}
