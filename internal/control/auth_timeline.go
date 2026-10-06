package control

// Auth timeline: a passive, read-only reconstruction of a login attempt's
// redirect / cookie / session / CSRF / MFA chain from already captured flows.
// It never sends requests. Observations that come straight from captured
// headers are labelled "observed"; everything that infers browser behaviour
// (cookie rejection, omission, lost sessions, MFA completion) is labelled
// "hypothesis" until reproduced. Cookie values are never returned — only short
// fingerprints; the raw values stay in the project's flow store.

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/csrf"
	"github.com/Veyal/interseptor/internal/store"
)

const (
	confidenceObserved   = "observed"
	confidenceHypothesis = "hypothesis"
)

type authTimelineEvent struct {
	Kind                string   `json:"kind"`
	Name                string   `json:"name,omitempty"`
	Detail              string   `json:"detail"`
	Confidence          string   `json:"confidence"`
	Attributes          []string `json:"attributes,omitempty"`
	Fingerprint         string   `json:"fingerprint,omitempty"`
	PreviousFingerprint string   `json:"previousFingerprint,omitempty"`
}

type authTimelineStep struct {
	FlowID   int64               `json:"flowId"`
	TS       time.Time           `json:"ts"`
	Method   string              `json:"method"`
	Scheme   string              `json:"scheme"`
	Host     string              `json:"host"`
	Path     string              `json:"path"`
	Status   int                 `json:"status"`
	Redirect string              `json:"redirect,omitempty"`
	Events   []authTimelineEvent `json:"events"`
}

type authTimelineLoss struct {
	FlowID     int64  `json:"flowId"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"`
}

type authTimeline struct {
	Steps         []authTimelineStep `json:"steps"`
	LostAt        *authTimelineLoss  `json:"lostAt,omitempty"`
	MFAState      string             `json:"mfaState"`
	MFAConfidence string             `json:"mfaConfidence"`
	Note          string             `json:"note"`
}

// jarCookie is one cookie the simulated client would hold.
type jarCookie struct {
	name, domain, path string
	hostOnly, secure   bool
	fingerprint        string
	expires            time.Time
}

func (c jarCookie) key() string { return c.name + "\x00" + c.domain + "\x00" + c.path }

type authTimelineBuilder struct {
	jar           map[string]jarCookie
	authenticated bool
	tl            authTimeline
	prev          *store.Flow
	mfaChallenged bool
}

func buildAuthTimeline(flows []*store.Flow) authTimeline {
	sorted := append([]*store.Flow(nil), flows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].TS.Equal(sorted[j].TS) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].TS.Before(sorted[j].TS)
	})
	b := &authTimelineBuilder{jar: map[string]jarCookie{}}
	b.tl = authTimeline{
		Steps: []authTimelineStep{}, MFAState: "none", MFAConfidence: confidenceHypothesis,
		Note: "Passive reconstruction from captured flows. Items marked hypothesis infer browser behaviour and must be reproduced before reporting. Cookie values are hidden; open the flow to see raw values.",
	}
	for _, f := range sorted {
		b.addFlow(f)
		b.prev = f
	}
	return b.tl
}

func (b *authTimelineBuilder) addFlow(f *store.Flow) {
	step := authTimelineStep{
		FlowID: f.ID, TS: f.TS, Method: f.Method, Scheme: strings.ToLower(f.Scheme), Host: strings.ToLower(f.Host),
		Path: safeFlowPath(f.Path), Status: f.Status, Redirect: safeRedirect(headerFirst(f.ResHeaders, "Location")),
		Events: []authTimelineEvent{},
	}
	add := func(e authTimelineEvent) { step.Events = append(step.Events, e) }
	b.originChange(f, add)
	lossReason := b.requestCookies(f, add)
	if step.Redirect != "" {
		add(authTimelineEvent{Kind: "redirect", Detail: "Redirect (" + fmt.Sprint(f.Status) + ") to " + step.Redirect, Confidence: confidenceObserved})
	}
	if reason := b.responseCookies(f, add); reason != "" && lossReason == "" {
		lossReason = reason
	}
	b.mfa(f, add)
	if lossReason == "" {
		lossReason = b.rejectionLoss(f)
	}
	if lossReason != "" && b.authenticated {
		b.authenticated = false
		add(authTimelineEvent{Kind: "auth-lost", Detail: lossReason, Confidence: confidenceHypothesis})
		if b.tl.LostAt == nil {
			b.tl.LostAt = &authTimelineLoss{FlowID: f.ID, Reason: lossReason, Confidence: confidenceHypothesis}
		}
	}
	b.tl.Steps = append(b.tl.Steps, step)
}

func (b *authTimelineBuilder) originChange(f *store.Flow, add func(authTimelineEvent)) {
	if b.prev == nil {
		return
	}
	if !strings.EqualFold(b.prev.Scheme, f.Scheme) {
		add(authTimelineEvent{Kind: "scheme-change", Detail: strings.ToLower(b.prev.Scheme) + " to " + strings.ToLower(f.Scheme), Confidence: confidenceObserved})
	}
	if !strings.EqualFold(b.prev.Host, f.Host) {
		add(authTimelineEvent{Kind: "host-change", Detail: strings.ToLower(b.prev.Host) + " to " + strings.ToLower(f.Host) + "; host-only cookies do not follow", Confidence: confidenceObserved})
	}
}

// requestCookies compares what the client sent with what the simulated jar
// says it should have sent. It returns a loss reason when an authenticated
// request failed in a way consistent with a missing/rejected session.
func (b *authTimelineBuilder) requestCookies(f *store.Flow, add func(authTimelineEvent)) string {
	sent := map[string]string{}
	for _, c := range parseRequestCookies(f.ReqHeaders) {
		sent[c.Name] = c.Fingerprint
	}
	expected := b.expectedCookies(f)
	sessionExpected, sessionSent := false, false
	for _, c := range expected {
		got, ok := sent[c.name]
		isSession := sessionCookieName(c.name)
		if isSession {
			sessionExpected = true
		}
		switch {
		case !ok:
			add(authTimelineEvent{Kind: "cookie-omitted", Name: c.name, Fingerprint: c.fingerprint, Confidence: confidenceHypothesis,
				Detail: "Cookie " + c.name + " should match this request's scope but was not sent; the client/browser may have declined it (SameSite, Secure, expiry or scope)"})
		case got != c.fingerprint:
			if isSession {
				sessionSent = true
			}
			add(authTimelineEvent{Kind: "cookie-stale", Name: c.name, Fingerprint: got, PreviousFingerprint: c.fingerprint, Confidence: confidenceHypothesis,
				Detail: "Client sent a different value for " + c.name + " than the most recent accepted Set-Cookie"})
		default:
			if isSession {
				sessionSent = true
			}
			add(authTimelineEvent{Kind: "cookie-sent", Name: c.name, Fingerprint: got, Confidence: confidenceObserved, Detail: "Cookie " + c.name + " sent"})
		}
	}
	b.csrfHeaderCheck(f, add)
	if !b.authenticated || !sessionExpected {
		return ""
	}
	return authFailureReason(f, sessionSent)
}

// csrfHeaderCheck flags a request whose CSRF header no longer matches the
// XSRF cookie it carried (typical after token rotation).
func (b *authTimelineBuilder) csrfHeaderCheck(f *store.Flow, add func(authTimelineEvent)) {
	header := headerFirst(f.ReqHeaders, "X-Xsrf-Token")
	cookie := csrf.XSRFFromCookie(headerFirst(f.ReqHeaders, "Cookie"))
	if header == "" || cookie == "" || header == cookie {
		return
	}
	add(authTimelineEvent{Kind: "csrf-mismatch", Name: "X-XSRF-TOKEN", Fingerprint: shortFingerprint(header), PreviousFingerprint: shortFingerprint(cookie),
		Confidence: confidenceHypothesis, Detail: "X-XSRF-TOKEN header differs from the XSRF-TOKEN cookie sent with the request (token rotation not applied by the client?)"})
}

func authFailureReason(f *store.Flow, sessionSent bool) string {
	rejected := f.Status == http.StatusUnauthorized || f.Status == http.StatusForbidden || looksLikeAuthChallenge(f.Status, f.ResHeaders)
	if !rejected {
		return ""
	}
	if sessionSent {
		return fmt.Sprintf("Request carrying the session cookie was answered %d / login redirect — the server may no longer accept the session", f.Status)
	}
	return fmt.Sprintf("Session cookie was not sent and the request was answered %d / login redirect — the client may have dropped it", f.Status)
}

// rejectionLoss covers authenticated Authorization-header flows (no cookie jar).
func (b *authTimelineBuilder) rejectionLoss(f *store.Flow) string {
	if !b.authenticated || headerFirst(f.ReqHeaders, "Authorization") == "" {
		return ""
	}
	if f.Status == http.StatusUnauthorized {
		return "Request with an Authorization header was answered 401"
	}
	return ""
}

func (b *authTimelineBuilder) expectedCookies(f *store.Flow) []jarCookie {
	host := strings.ToLower(f.Host)
	path := queryless(f.Path)
	var out []jarCookie
	for _, c := range b.jar {
		if !c.expires.IsZero() && !f.TS.Before(c.expires) {
			continue
		}
		if c.hostOnly && c.domain != host || !c.hostOnly && !domainMatch(host, c.domain) {
			continue
		}
		if !pathMatch(path, c.path) || c.secure && !strings.EqualFold(f.Scheme, "https") {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// responseCookies applies Set-Cookie headers to the jar and returns a loss
// reason when a session cookie was cleared.
func (b *authTimelineBuilder) responseCookies(f *store.Flow, add func(authTimelineEvent)) string {
	values := headerValues(f.ResHeaders, "Set-Cookie")
	if len(values) == 0 {
		return ""
	}
	h := make(http.Header)
	for _, v := range values {
		h.Add("Set-Cookie", v)
	}
	lost := ""
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == "" {
			continue
		}
		attrs := cookieAttributeNames(c)
		fp := shortFingerprint(c.Value)
		if why := cookieRejectionReason(f, c); why != "" {
			add(authTimelineEvent{Kind: "cookie-rejected", Name: c.Name, Attributes: attrs, Fingerprint: fp, Confidence: confidenceHypothesis,
				Detail: "Browsers are expected to reject " + c.Name + ": " + why})
			continue
		}
		jc := newJarCookie(f, c, fp)
		if cookieIsClear(f, c) {
			delete(b.jar, jc.key())
			add(authTimelineEvent{Kind: "cookie-cleared", Name: c.Name, Attributes: attrs, Confidence: confidenceObserved, Detail: "Response expired/cleared " + c.Name})
			if sessionCookieName(c.Name) && b.authenticated {
				lost = "Response cleared the session cookie " + c.Name
			}
			continue
		}
		b.storeCookie(jc, c, attrs, add)
		if sessionCookieName(c.Name) {
			b.authenticated = true
		}
	}
	return lost
}

func (b *authTimelineBuilder) storeCookie(jc jarCookie, c *http.Cookie, attrs []string, add func(authTimelineEvent)) {
	prev, replaced := b.jar[jc.key()]
	b.jar[jc.key()] = jc
	add(authTimelineEvent{Kind: "cookie-set", Name: c.Name, Attributes: attrs, Fingerprint: jc.fingerprint, Confidence: confidenceObserved,
		Detail: "Response set " + c.Name})
	if replaced && prev.fingerprint != jc.fingerprint {
		kind := "cookie-replaced"
		switch {
		case csrfCookieName(c.Name):
			kind = "csrf-rotation"
		case sessionCookieName(c.Name):
			kind = "session-rotation"
		}
		add(authTimelineEvent{Kind: kind, Name: c.Name, Fingerprint: jc.fingerprint, PreviousFingerprint: prev.fingerprint, Confidence: confidenceObserved,
			Detail: c.Name + " value changed within the same cookie scope"})
	}
	for _, other := range b.jar {
		if other.name == jc.name && other.key() != jc.key() {
			add(authTimelineEvent{Kind: "cookie-scope-duplicate", Name: c.Name, Confidence: confidenceHypothesis,
				Detail: "Cookie " + c.Name + " now exists under more than one Domain/Path scope; the client may send the stale copy"})
			break
		}
	}
}

func (b *authTimelineBuilder) mfa(f *store.Flow, add func(authTimelineEvent)) {
	if !containsAny(strings.ToLower(queryless(f.Path)), "mfa", "otp", "2fa", "two-factor", "challenge") {
		return
	}
	ok := f.Status >= 200 && f.Status < 400 && !looksLikeAuthChallenge(f.Status, f.ResHeaders)
	switch {
	case f.Method == http.MethodGet || f.Method == http.MethodHead:
		if ok {
			b.mfaChallenged = true
			b.tl.MFAState = "challenge-seen"
			add(authTimelineEvent{Kind: "mfa-challenge", Detail: "MFA challenge page/step captured", Confidence: confidenceHypothesis})
		}
	case ok:
		b.tl.MFAState = "completion-candidate"
		add(authTimelineEvent{Kind: "mfa-submit", Detail: "MFA submission was accepted (completion is a candidate only; confirm with a post-MFA authenticated request)", Confidence: confidenceHypothesis})
	default:
		b.tl.MFAState = "failed-candidate"
		add(authTimelineEvent{Kind: "mfa-submit", Detail: fmt.Sprintf("MFA submission answered %d", f.Status), Confidence: confidenceHypothesis})
	}
}

func cookieAttributeNames(c *http.Cookie) []string {
	var attrs []string
	if c.Path != "" {
		attrs = append(attrs, "Path="+c.Path)
	}
	if c.Domain != "" {
		attrs = append(attrs, "Domain="+c.Domain)
	}
	if c.Secure {
		attrs = append(attrs, "Secure")
	}
	if c.HttpOnly {
		attrs = append(attrs, "HttpOnly")
	}
	switch c.SameSite {
	case http.SameSiteStrictMode:
		attrs = append(attrs, "SameSite=Strict")
	case http.SameSiteLaxMode:
		attrs = append(attrs, "SameSite=Lax")
	case http.SameSiteNoneMode:
		attrs = append(attrs, "SameSite=None")
	}
	if c.MaxAge != 0 {
		attrs = append(attrs, fmt.Sprintf("Max-Age=%d", c.MaxAge))
	}
	if !c.Expires.IsZero() {
		attrs = append(attrs, "Expires")
	}
	return attrs
}

// cookieRejectionReason names why a browser would likely refuse a Set-Cookie.
func cookieRejectionReason(f *store.Flow, c *http.Cookie) string {
	host := strings.ToLower(f.Host)
	switch {
	case c.Secure && !strings.EqualFold(f.Scheme, "https"):
		return "Secure cookie set over " + strings.ToLower(f.Scheme)
	case c.SameSite == http.SameSiteNoneMode && !c.Secure:
		return "SameSite=None requires Secure"
	case c.Domain != "" && !domainMatch(host, strings.ToLower(strings.TrimPrefix(c.Domain, "."))):
		return "Domain=" + c.Domain + " does not match host " + host
	case strings.HasPrefix(c.Name, "__Host-") && (!c.Secure || c.Path != "/" || c.Domain != ""):
		return "__Host- prefix requires Secure, Path=/ and no Domain"
	case strings.HasPrefix(c.Name, "__Secure-") && !c.Secure:
		return "__Secure- prefix requires Secure"
	}
	return ""
}

func cookieIsClear(f *store.Flow, c *http.Cookie) bool {
	return c.MaxAge < 0 || c.Value == "" || (!c.Expires.IsZero() && !c.Expires.After(f.TS))
}

func newJarCookie(f *store.Flow, c *http.Cookie, fp string) jarCookie {
	jc := jarCookie{name: c.Name, secure: c.Secure, fingerprint: fp, path: c.Path}
	if c.Domain != "" {
		jc.domain = strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	} else {
		jc.domain, jc.hostOnly = strings.ToLower(f.Host), true
	}
	if jc.path == "" || !strings.HasPrefix(jc.path, "/") {
		jc.path = defaultCookiePath(queryless(f.Path))
	}
	switch {
	case c.MaxAge > 0:
		jc.expires = f.TS.Add(time.Duration(c.MaxAge) * time.Second)
	case !c.Expires.IsZero():
		jc.expires = c.Expires
	}
	return jc
}

func queryless(path string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return "/"
	}
	return path
}

func defaultCookiePath(reqPath string) string {
	i := strings.LastIndex(reqPath, "/")
	if i <= 0 {
		return "/"
	}
	return reqPath[:i]
}

func domainMatch(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

func csrfCookieName(name string) bool {
	return containsAny(strings.ToLower(name), "csrf", "xsrf")
}

// sessionCookieName: authentication-bearing cookies, excluding CSRF/MFA helpers.
func sessionCookieName(name string) bool {
	lower := strings.ToLower(name)
	if csrfCookieName(name) || containsAny(lower, "mfa", "otp") {
		return false
	}
	return containsAny(lower, "session", "sess", "sid", "auth", "token", "jwt")
}

// authTimelineChain loads the selected flow and the same client's following
// flows within the window, in capture order.
func authTimelineChain(st *store.Store, start *store.Flow, window time.Duration, limit int) ([]*store.Flow, error) {
	later, err := st.QueryFlowsFilter(store.FlowFilter{
		SortKey: "id", SortDir: 1, CursorID: start.ID, Limit: 500,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagAuthz,
	})
	if err != nil {
		return nil, err
	}
	chain := []*store.Flow{start}
	client := timelineClientHost(start.ClientAddr)
	for _, f := range later {
		if len(chain) >= limit || f.TS.Sub(start.TS) > window {
			break
		}
		if client != "" && timelineClientHost(f.ClientAddr) != client {
			continue
		}
		chain = append(chain, f)
	}
	return chain, nil
}

func timelineClientHost(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.Contains(addr[i:], "]") {
		return strings.Trim(addr[:i], "[]")
	}
	return strings.Trim(addr, "[]")
}
