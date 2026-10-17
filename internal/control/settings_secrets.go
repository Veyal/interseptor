package control

import (
	"net/http"
	"net/url"

	"github.com/Veyal/interseptor/internal/store"
)

// settingsSecretsVisible reports whether GET /api/settings may return stored
// credentials (the proxy basic-auth password and an upstream proxy's
// password). Only the interactive UI session of a full-access operator sees
// them; the AI channel, API-key callers and read-only sessions get them
// masked, so they never land in agent transcripts or provider logs.
func settingsSecretsVisible(r *http.Request) bool {
	return !isAISource(r) && bearerToken(r) == "" && requestScope(r) == store.ScopeFull && hasUISessionCookie(r)
}

// redactProxyURL masks the password in a proxy URL's userinfo, keeping the
// scheme, user and address so a reader can still see where traffic goes.
func redactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, ok := u.User.Password(); !ok {
		return raw
	}
	return u.Redacted()
}
