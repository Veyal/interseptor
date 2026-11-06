package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
)

// uiSessionCookie carries a per-process random secret that the server hands
// only to the browser that loads the UI shell. Human-only gates (script trust,
// capability grants, secret reveal) require it in addition to the absence of
// AI/API-key markers, so a caller that can merely choose its own headers (for
// example a request that re-enters the control port on the AI's behalf) can no
// longer pass as an interactive UI session. The cookie is HttpOnly and
// SameSite=Strict, so page scripts and cross-site requests never see it.
const uiSessionCookie = "ick_ui"

var uiSessionSecret = newUISessionSecret()

// errUISessionStale explains a refused human-only action from a page that was
// loaded before this process started (the secret is per process).
var errUISessionStale = errors.New("this action needs the interactive Interseptor UI: reload the UI tab and try again")

func newUISessionSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("control: read random UI session secret: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// issueUISessionCookie gives the UI shell's browser the session secret. AI
// and API-key callers never receive it.
func issueUISessionCookie(w http.ResponseWriter, r *http.Request) {
	if isAISource(r) || bearerToken(r) != "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     uiSessionCookie,
		Value:    uiSessionSecret,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// hasUISessionCookie reports whether r presents this process's UI secret.
func hasUISessionCookie(r *http.Request) bool {
	c, err := r.Cookie(uiSessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(uiSessionSecret)) == 1
}
