package control

// Session inspection is deliberately passive. It consumes selected captured
// flows and returns bounded observations; it never replays a request and never
// returns credential-bearing header or cookie values.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

const maxSessionInspectionFlows = 32

type sessionCookieJSON struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Attributes  []string `json:"attributes,omitempty"`
	Candidate   bool     `json:"candidate,omitempty"`
}

type sessionFlowJSON struct {
	ID               int64               `json:"id"`
	Timestamp        time.Time           `json:"timestamp"`
	Role             string              `json:"role"`
	Method           string              `json:"method"`
	Scheme           string              `json:"scheme"`
	Host             string              `json:"host"`
	Path             string              `json:"path"`
	Status           int                 `json:"status"`
	Redirect         string              `json:"redirect,omitempty"`
	RequestCookies   []sessionCookieJSON `json:"requestCookies,omitempty"`
	ResponseCookies  []sessionCookieJSON `json:"responseCookies,omitempty"`
	ResponseHeaders  string              `json:"responseHeadersFingerprint,omitempty"`
	ResponseBody     string              `json:"responseBodyFingerprint,omitempty"`
	Candidates       []string            `json:"candidates,omitempty"`
	BrowserDecision  string              `json:"browserDecision"`
	ValidationOrder  string              `json:"validationOrder"`
	ObservableEffect string              `json:"observableEffect"`
	clientAddr       string
}

type sessionTransitionJSON struct {
	FlowID     int64  `json:"flowId"`
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Message    string `json:"message"`
	Confidence string `json:"confidence"`
}

type sessionDifferentialJSON struct {
	Endpoint              string                       `json:"endpoint"`
	Roles                 []string                     `json:"roles"`
	Statuses              map[string]int               `json:"statuses"`
	ResponseFingerprints  map[string]string            `json:"responseFingerprints"`
	StatusDifferent       bool                         `json:"statusDifferent"`
	ResponseDifferent     bool                         `json:"responseDifferent"`
	ValidationOrder       string                       `json:"validationOrder"`
	ObservableSideEffects string                       `json:"observableSideEffects"`
	RoleCounts            map[string]int               `json:"roleCounts"`
	Observations          []sessionRoleObservationJSON `json:"observations"`
}

type sessionRoleObservationJSON struct {
	FlowID                     int64     `json:"flowId"`
	Timestamp                  time.Time `json:"timestamp"`
	Role                       string    `json:"role"`
	Status                     int       `json:"status"`
	ResponseHeadersFingerprint string    `json:"responseHeadersFingerprint,omitempty"`
	ResponseBodyFingerprint    string    `json:"responseBodyFingerprint,omitempty"`
}

type sessionInspectionJSON struct {
	Flows         []sessionFlowJSON         `json:"flows"`
	Transitions   []sessionTransitionJSON   `json:"transitions"`
	Differentials []sessionDifferentialJSON `json:"differentials"`
	SafetyNote    string                    `json:"safetyNote"`
}

func normalizeSessionRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "anonymous", "anon", "guest":
		return "anonymous"
	case "user", "low", "low-privilege", "member":
		return "user"
	case "admin", "administrator":
		return "admin"
	default:
		return "unassigned"
	}
}

func buildSessionInspection(flows []*store.Flow, roles []string) sessionInspectionJSON {
	roleByID := make(map[int64]string, len(flows))
	for i, f := range flows {
		role := "unassigned"
		if i < len(roles) {
			role = normalizeSessionRole(roles[i])
		}
		roleByID[f.ID] = role
	}
	sort.SliceStable(flows, func(i, j int) bool {
		if flows[i].TS.Equal(flows[j].TS) {
			return flows[i].ID < flows[j].ID
		}
		return flows[i].TS.Before(flows[j].TS)
	})

	out := sessionInspectionJSON{
		Flows:      make([]sessionFlowJSON, 0, len(flows)),
		SafetyNote: "Passive observations only. Browser cookie rejection, authentication success/loss, MFA completion, validation order, and side effects remain unknown unless directly captured.",
	}
	for _, f := range flows {
		out.Flows = append(out.Flows, inspectSessionFlow(f, roleByID[f.ID]))
	}

	previousSet := make(map[string]string)
	for _, f := range out.Flows {
		if f.Redirect != "" {
			out.Transitions = append(out.Transitions, sessionTransitionJSON{FlowID: f.ID, Kind: "redirect", Message: "Captured redirect to " + f.Redirect, Confidence: "observed"})
		}
		for _, c := range f.ResponseCookies {
			out.Transitions = append(out.Transitions, sessionTransitionJSON{FlowID: f.ID, Kind: "set-cookie", Name: c.Name, Message: "Response set candidate cookie " + c.Name + "; browser acceptance is unknown", Confidence: "candidate"})
			if key := sessionCookieContinuityKey(f, c); key != "" {
				if old, ok := previousSet[key]; ok && old != c.Fingerprint {
					out.Transitions = append(out.Transitions, sessionTransitionJSON{FlowID: f.ID, Kind: "rotation", Name: c.Name, Message: "Candidate " + c.Name + " fingerprint changed within the same observed client, host, role, and cookie scope", Confidence: "candidate"})
				}
				previousSet[key] = c.Fingerprint
			}
		}
		for _, c := range f.RequestCookies {
			if c.Candidate {
				out.Transitions = append(out.Transitions, sessionTransitionJSON{FlowID: f.ID, Kind: "transmit", Name: c.Name, Message: "Candidate cookie " + c.Name + " was transmitted", Confidence: "observed"})
			}
		}
		for _, candidate := range f.Candidates {
			out.Transitions = append(out.Transitions, sessionTransitionJSON{FlowID: f.ID, Kind: "candidate", Message: candidate, Confidence: "candidate"})
		}
	}
	out.Differentials = buildSessionDifferentials(out.Flows)
	return out
}

func inspectSessionFlow(f *store.Flow, role string) sessionFlowJSON {
	redirect := safeRedirect(headerFirst(f.ResHeaders, "Location"))
	flow := sessionFlowJSON{
		ID: f.ID, Timestamp: f.TS, Role: role, Method: f.Method, Scheme: f.Scheme,
		Host: f.Host, Path: safeFlowPath(f.Path), Status: f.Status, Redirect: redirect,
		RequestCookies: parseRequestCookies(f.ReqHeaders), ResponseCookies: parseResponseCookies(f.ResHeaders),
		ResponseHeaders: headerFingerprint(f.ResHeaders), ResponseBody: bodyFingerprint(f.ResBodyHash),
		BrowserDecision: "unknown", ValidationOrder: "unknown", ObservableEffect: "unknown",
		clientAddr: f.ClientAddr,
	}
	if redirect != "" {
		flow.Candidates = append(flow.Candidates, "redirect observed; destination authentication state is not inferred")
	}
	if f.Status == http.StatusUnauthorized || f.Status == http.StatusForbidden {
		flow.Candidates = append(flow.Candidates, "authentication or authorization failure candidate; exact cause is unknown")
	}
	if f.Status == http.StatusBadRequest || f.Status == http.StatusUnprocessableEntity {
		flow.Candidates = append(flow.Candidates, "validation failure candidate; whether authentication ran first is unknown")
	}
	lower := strings.ToLower(f.Path)
	if containsAny(lower, "login", "signin", "sign-in", "auth") {
		flow.Candidates = append(flow.Candidates, "authentication transition candidate")
	}
	if containsAny(lower, "mfa", "otp", "two-factor", "2fa", "challenge") {
		flow.Candidates = append(flow.Candidates, "MFA transition candidate; completion is unknown")
	}
	if containsAny(lower, "csrf", "xsrf", "nonce") {
		flow.Candidates = append(flow.Candidates, "CSRF/token transition candidate")
	}
	return flow
}

func buildSessionDifferentials(flows []sessionFlowJSON) []sessionDifferentialJSON {
	groups := make(map[string][]sessionFlowJSON)
	for _, f := range flows {
		key := strings.ToUpper(f.Method) + " " + strings.ToLower(f.Scheme) + "://" + strings.ToLower(f.Host) + f.Path
		groups[key] = append(groups[key], f)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rolesOrder := map[string]int{"anonymous": 0, "user": 1, "admin": 2, "unassigned": 3}
	var out []sessionDifferentialJSON
	for _, key := range keys {
		group := groups[key]
		byRole := make(map[string][]sessionFlowJSON)
		for _, f := range group {
			byRole[f.Role] = append(byRole[f.Role], f)
		}
		if len(byRole) < 2 {
			continue
		}
		roles := make([]string, 0, len(byRole))
		for role := range byRole {
			roles = append(roles, role)
		}
		sort.Slice(roles, func(i, j int) bool { return rolesOrder[roles[i]] < rolesOrder[roles[j]] })
		statuses := make(map[string]int, len(roles))
		fingerprints := make(map[string]string, len(roles))
		roleCounts := make(map[string]int, len(roles))
		observations := make([]sessionRoleObservationJSON, 0, len(group))
		for _, role := range roles {
			roleFlows := byRole[role]
			latest := roleFlows[len(roleFlows)-1]
			statuses[role] = latest.Status
			fingerprints[role] = latest.ResponseHeaders + ":" + latest.ResponseBody
			roleCounts[role] = len(roleFlows)
			for _, f := range roleFlows {
				observations = append(observations, sessionRoleObservationJSON{FlowID: f.ID, Timestamp: f.Timestamp, Role: role, Status: f.Status, ResponseHeadersFingerprint: f.ResponseHeaders, ResponseBodyFingerprint: f.ResponseBody})
			}
		}
		statusDifferent, responseDifferent := false, false
		for i := 1; i < len(roles); i++ {
			if statuses[roles[i]] != statuses[roles[0]] {
				statusDifferent = true
			}
			if fingerprints[roles[i]] != fingerprints[roles[0]] {
				responseDifferent = true
			}
		}
		out = append(out, sessionDifferentialJSON{
			Endpoint: key, Roles: roles, Statuses: statuses, ResponseFingerprints: fingerprints,
			StatusDifferent: statusDifferent, ResponseDifferent: responseDifferent,
			ValidationOrder: "unknown", ObservableSideEffects: "unknown; no requests sent",
			RoleCounts: roleCounts, Observations: observations,
		})
	}
	return out
}

func sessionCookieContinuityKey(flow sessionFlowJSON, cookie sessionCookieJSON) string {
	// Without a captured client address, two flows may come from different
	// clients. Avoid claiming session rotation across that ambiguous boundary.
	if flow.clientAddr == "" {
		return ""
	}
	return strings.Join([]string{
		normalizeSessionRole(flow.Role), sessionCookieScopeHost(flow.Host, cookie.Attributes), flow.clientAddr,
		cookie.Name, strings.Join(cookie.Attributes, "|"),
	}, "\x00")
}

func sessionCookieScopeHost(flowHost string, attributes []string) string {
	for _, attribute := range attributes {
		name, value, ok := strings.Cut(attribute, "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "domain") && strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return strings.ToLower(flowHost)
}

func parseRequestCookies(headers map[string][]string) []sessionCookieJSON {
	var out []sessionCookieJSON
	for _, raw := range headerValues(headers, "Cookie") {
		for _, part := range strings.Split(raw, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			name = strings.TrimSpace(name)
			if !ok || name == "" {
				continue
			}
			out = append(out, sessionCookieJSON{Name: name, Fingerprint: shortFingerprint(value), Candidate: candidateCookieName(name)})
		}
	}
	return out
}

func parseResponseCookies(headers map[string][]string) []sessionCookieJSON {
	values := headerValues(headers, "Set-Cookie")
	if len(values) == 0 {
		return nil
	}
	h := make(http.Header)
	for _, value := range values {
		h.Add("Set-Cookie", value)
	}
	resp := (&http.Response{Header: h}).Cookies()
	out := make([]sessionCookieJSON, 0, len(resp))
	for _, c := range resp {
		if c.Name == "" {
			continue
		}
		attrs := make([]string, 0, 8)
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
		if c.Partitioned {
			attrs = append(attrs, "Partitioned")
		}
		out = append(out, sessionCookieJSON{Name: c.Name, Fingerprint: shortFingerprint(c.Value), Attributes: attrs, Candidate: candidateCookieName(c.Name)})
	}
	return out
}

func candidateCookieName(name string) bool {
	lower := strings.ToLower(name)
	return containsAny(lower, "session", "sess", "sid", "auth", "token", "jwt", "csrf", "xsrf", "mfa", "otp")
}

func headerValues(headers map[string][]string, name string) []string {
	var out []string
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			out = append(out, values...)
		}
	}
	return out
}

func headerFirst(headers map[string][]string, name string) string {
	values := headerValues(headers, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func headerFingerprint(headers map[string][]string) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, strings.ToLower(key))
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		if key == "date" || key == "content-length" {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		for _, value := range headerValues(headers, key) {
			b.WriteString(shortFingerprint(value))
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	if b.Len() == 0 {
		return ""
	}
	return shortFingerprint(b.String())
}

func bodyFingerprint(hash string) string {
	if hash == "" {
		return ""
	}
	return shortFingerprint(hash)
}

func shortFingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:12]
}

func safeRedirect(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "redirect (location unavailable)"
	}
	if u.IsAbs() {
		return u.Scheme + "://" + u.Host + safeFlowPath(u.EscapedPath())
	}
	return safeFlowPath(u.EscapedPath())
}

func safeFlowPath(path string) string {
	if path == "" {
		return "/"
	}
	u, err := url.Parse(path)
	if err != nil {
		// Unparseable request targets may still contain credential-bearing
		// queries or fragments. Never expose the original input as a fallback.
		return "/"
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if len(u.Query()) == 0 {
		return u.Path
	}
	keys := make([]string, 0, len(u.Query()))
	for key := range u.Query() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return u.Path + "?" + strings.Join(keys, "=…&") + "=…"
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
