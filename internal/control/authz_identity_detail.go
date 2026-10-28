package control

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/auth/jwtextract"
	"github.com/Veyal/interseptor/internal/store"
)

// The Identities workspace: one human-facing detail view per saved authz
// identity (what the credential is, where it came from, where it has been
// used, what happened to it), plus the editing, probing and refreshing
// operations that view needs.
//
// Secrets never leave through this file: header values are masked, cookie
// values are reduced to a fingerprint and the JWT is decoded without its
// signature. The raw headers stay available through GET /api/authz, which is
// already scope-gated (read-scope callers get them stripped).

const (
	maxIdentityPatchBytes int64 = 1 << 20
	maxIdentityCheckBytes int64 = 4 << 10
	// identityUsageScan bounds the credential-usage scan (see
	// store.FlowsWithCredential: request headers are one JSON column, so there
	// is no index — only the newest flows are examined).
	identityUsageScan   = 5000
	identityUsageRecent = 10
)

// errIdentityNameTaken is the 409 case of a PATCH rename.
var errIdentityNameTaken = errors.New("identity name already in use")

type identityHeaderView struct {
	Name   string `json:"name"`
	Masked string `json:"masked"`
	Length int    `json:"length"`
}

type identityCookieView struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

type identityJWTView struct {
	Alg       string         `json:"alg"`
	Sub       string         `json:"sub"`
	Roles     []string       `json:"roles"`
	Issuer    string         `json:"issuer"`
	Audience  string         `json:"audience"`
	IAT       int64          `json:"iat"`
	Exp       int64          `json:"exp"`
	ExpiresIn int64          `json:"expiresIn"`
	Expired   bool           `json:"expired"`
	Claims    map[string]any `json:"claims"`
}

type identityAuthView struct {
	Type    string               `json:"type"`
	Headers []identityHeaderView `json:"headers"`
	Cookies []identityCookieView `json:"cookies"`
	JWT     *identityJWTView     `json:"jwt"`
}

type identityUsageView struct {
	Count  int               `json:"count"`
	Recent []identityFlowRef `json:"recent"`
}

type identityFlowRef struct {
	FlowID int64  `json:"flowId"`
	TS     string `json:"ts"`
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	Status int    `json:"status"`
}

type identityTimelineView struct {
	Source *identityFlowRef `json:"source"`
	Events []identityEvent  `json:"events"`
}

// ---- auth view -------------------------------------------------------------

// maskSecret shows the first four and last two characters of a credential, or
// nothing at all when it is too short to mask meaningfully.
func maskSecret(v string) string {
	// A short secret would be mostly revealed by its ends.
	if len(v) < 16 {
		return "••••"
	}
	return v[:4] + "…" + v[len(v)-2:]
}

// identityHeaderPairs splits an identity's "Key: Value" lines.
func identityHeaderPairs(headers string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(headers, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, [2]string{http.CanonicalHeaderKey(k), strings.TrimSpace(v)})
		}
	}
	return out
}

// identityHeaderMap rebuilds a request-header map from an identity's lines so
// the shared extractors (jwtextract) can read it.
func identityHeaderMap(headers string) map[string][]string {
	out := map[string][]string{}
	for _, kv := range identityHeaderPairs(headers) {
		out[kv[0]] = []string{kv[1]}
	}
	return out
}

// identityCookiePairs splits a Cookie header value into name/value pairs.
func identityCookiePairs(cookie string) [][2]string {
	var out [][2]string
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out = append(out, [2]string{strings.TrimSpace(name), strings.TrimSpace(value)})
	}
	return out
}

// identityAuthType labels the credential scheme an identity carries.
func identityAuthType(pairs [][2]string) string {
	hasCookie, hasOther := false, false
	for _, kv := range pairs {
		switch kv[0] {
		case "Authorization":
			lower := strings.ToLower(kv[1])
			switch {
			case strings.HasPrefix(lower, "bearer "):
				return "bearer"
			case strings.HasPrefix(lower, "basic "):
				return "basic"
			}
			hasOther = true
		case "Cookie":
			hasCookie = true
		default:
			hasOther = true
		}
	}
	switch {
	case hasCookie:
		return "cookie"
	case hasOther:
		return "header"
	}
	return "none"
}

// buildIdentityAuth summarises an identity's credentials without revealing them.
func buildIdentityAuth(id identity) identityAuthView {
	pairs := identityHeaderPairs(id.Headers)
	out := identityAuthView{Type: identityAuthType(pairs), Headers: []identityHeaderView{}, Cookies: []identityCookieView{}}
	for _, kv := range pairs {
		out.Headers = append(out.Headers, identityHeaderView{Name: kv[0], Masked: maskSecret(kv[1]), Length: len(kv[1])})
		if kv[0] == "Cookie" {
			for _, c := range identityCookiePairs(kv[1]) {
				out.Cookies = append(out.Cookies, identityCookieView{Name: c[0], Fingerprint: shortFingerprint(c[1])})
			}
		}
	}
	hdrs := identityHeaderMap(id.Headers)
	if token, _ := jwtextract.Extract(jwtextract.Input{ReqHeaders: hdrs}); token != "" {
		out.JWT = decodeJWTView(token, time.Now())
	}
	return out
}

// ---- JWT ------------------------------------------------------------------

func b64urlJSON(seg string) map[string]any {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(seg, "="))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// decodeJWTView decodes a JWT's header and payload WITHOUT verifying the
// signature — the token is evidence about the target, not a credential this
// process trusts. nil when the token is not a decodable JWT.
func decodeJWTView(token string, now time.Time) *identityJWTView {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload := b64urlJSON(parts[1])
	if payload == nil {
		return nil
	}
	v := &identityJWTView{Roles: []string{}, Claims: map[string]any{}}
	if hdr := b64urlJSON(parts[0]); hdr != nil {
		v.Alg = claimString(hdr["alg"])
	}
	v.Sub = claimString(payload["sub"])
	v.Issuer = claimString(payload["iss"])
	v.Audience = claimJoin(payload["aud"])
	v.IAT = claimInt(payload["iat"])
	v.Exp = claimInt(payload["exp"])
	if v.Exp != 0 {
		v.ExpiresIn = v.Exp - now.Unix()
		v.Expired = v.ExpiresIn < 0
	}
	v.Roles = jwtRoles(payload)
	for k, raw := range payload {
		switch raw.(type) {
		case string, float64, bool, nil:
			v.Claims[k] = raw
		}
	}
	return v
}

// jwtRoleClaims are the claim names role information is commonly carried in.
var jwtRoleClaims = []string{"role", "roles", "scope", "scp", "groups", "cognito:groups"}

func jwtRoles(payload map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		for _, part := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
			if part != "" && !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	collect := func(v any) {
		switch t := v.(type) {
		case string:
			add(t)
		case []any:
			for _, e := range t {
				add(claimString(e))
			}
		}
	}
	for _, name := range jwtRoleClaims {
		collect(payload[name])
	}
	// Keycloak: realm_access.roles
	if ra, ok := payload["realm_access"].(map[string]any); ok {
		collect(ra["roles"])
	}
	return out
}

func claimString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func claimJoin(v any) string {
	if arr, ok := v.([]any); ok {
		parts := make([]string, 0, len(arr))
		for _, e := range arr {
			if s := claimString(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return claimString(v)
}

func claimInt(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

// ---- usage ----------------------------------------------------------------

// identityCredentialValues lists the credential strings that identify this
// identity in a captured request: the Authorization value and each cookie
// value it sends.
func identityCredentialValues(id identity) []string {
	var out []string
	for _, kv := range identityHeaderPairs(id.Headers) {
		switch kv[0] {
		case "Authorization":
			if kv[1] != "" {
				out = append(out, kv[1])
			}
		case "Cookie":
			for _, c := range identityCookiePairs(kv[1]) {
				if c[1] != "" {
					out = append(out, c[1])
				}
			}
		}
	}
	return out
}

func (h *authzAPI) buildIdentityUsage(id identity) identityUsageView {
	out := identityUsageView{Recent: []identityFlowRef{}}
	count, recent, err := h.st.FlowsWithCredential(identityCredentialValues(id), identityUsageScan, identityUsageRecent)
	if err != nil {
		return out
	}
	out.Count = count
	for _, u := range recent {
		out.Recent = append(out.Recent, identityFlowRef{
			FlowID: u.FlowID, TS: time.UnixMilli(u.TS).UTC().Format(time.RFC3339),
			Method: u.Method, Host: u.Host, Path: u.Path, Status: u.Status,
		})
	}
	return out
}

// ---- timeline -------------------------------------------------------------

func (h *authzAPI) buildIdentityTimeline(id identity) identityTimelineView {
	out := identityTimelineView{Events: h.identityEventsFor(id.Name)}
	if id.Source == nil || id.Source.FlowID <= 0 {
		return out
	}
	ref := &identityFlowRef{FlowID: id.Source.FlowID, Method: id.Source.Method, Host: id.Source.Host, Path: id.Source.Path}
	if f, err := h.st.GetFlow(id.Source.FlowID); err == nil {
		ref.Method, ref.Host, ref.Path, ref.Status = f.Method, f.Host, f.Path, f.Status
		ref.TS = f.TS.UTC().Format(time.RFC3339)
	}
	out.Source = ref
	return out
}

// ---- handlers -------------------------------------------------------------

// identityNameParam reads and unescapes the {name} path value.
func identityNameParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	// PathValue is already unescaped; unescaping again breaks names with '%'.
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return "", false
	}
	return name, true
}

// loadIdentity resolves one saved identity, answering 404/500 itself.
func (h *authzAPI) loadIdentity(w http.ResponseWriter, name string) (identity, bool) {
	ids, err := h.authzIdentitiesResult()
	if err != nil {
		httpInternalErr(w, err)
		return identity{}, false
	}
	id, ok := findIdentity(ids, name)
	if !ok {
		httpErr(w, http.StatusNotFound, "identity not found")
		return identity{}, false
	}
	return id, true
}

// getAuthzIdentity is GET /api/authz/identities/{name}.
func (h *authzAPI) getAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	name, ok := identityNameParam(w, r)
	if !ok {
		return
	}
	id, ok := h.loadIdentity(w, name)
	if !ok {
		return
	}
	view := map[string]any{
		"auth":     buildIdentityAuth(id),
		"usage":    h.buildIdentityUsage(id),
		"timeline": h.buildIdentityTimeline(id),
	}
	// A read-scope collaborator key sees the same masked view as GET /api/authz
	// gives it: metadata yes, raw credentials no.
	if requestScope(r) == store.ScopeRead {
		id.Headers = ""
	}
	view["identity"] = id
	writeJSON(w, http.StatusOK, view)
}

// patchAuthzIdentity is PATCH /api/authz/identities/{name}. Only the fields
// present in the body change; a rename keeps the identity's event history and
// fails with 409 when the new name is taken.
func (h *authzAPI) patchAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	name, ok := identityNameParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Name       *string          `json:"name"`
		Role       *string          `json:"role"`
		Notes      *string          `json:"notes"`
		Headers    *json.RawMessage `json:"headers"`
		Broken     *bool            `json:"broken"`
		BrokenNote *string          `json:"brokenNote"`
	}
	if !decodeLimitedJSON(w, r, maxIdentityPatchBytes, &in) {
		return
	}
	newName := name
	if in.Name != nil {
		if newName = strings.TrimSpace(*in.Name); newName == "" {
			httpErr(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
	}
	var headers string
	if in.Headers != nil {
		var err error
		if headers, err = normalizeHeaderLines(*in.Headers); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	actor := identityActor(r, "")
	var updated identity
	found := false
	wasBroken := false
	_, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		if newName != name {
			if _, taken := findIdentity(cur, newName); taken {
				return nil, errIdentityNameTaken
			}
		}
		for i := range cur {
			if cur[i].Name != name {
				continue
			}
			found = true
			wasBroken = cur[i].Broken
			cur[i].Name = newName
			if in.Role != nil {
				cur[i].Role = strings.TrimSpace(*in.Role)
			}
			if in.Notes != nil {
				cur[i].Notes = *in.Notes
			}
			if in.Headers != nil {
				if cur[i].Headers != headers {
					// New credentials invalidate the previous session check
					// (as promoteFlowToAuthz does).
					cur[i].LastCheck = nil
				}
				cur[i].Headers = headers
			}
			if in.Broken != nil {
				cur[i].Broken = *in.Broken
			}
			if in.BrokenNote != nil {
				cur[i].BrokenNote = strings.TrimSpace(*in.BrokenNote)
			}
			cur[i] = stampIdentity(cur[i], actor, time.Now())
			updated = cur[i]
			break
		}
		return cur, nil
	})
	switch {
	case errors.Is(err, errIdentityNameTaken):
		httpErr(w, http.StatusConflict, "an identity named "+newName+" already exists")
		return
	case err != nil:
		httpInternalErr(w, err)
		return
	case !found:
		httpErr(w, http.StatusNotFound, "identity not found")
		return
	}
	if newName != name {
		h.renameIdentityEvents(name, newName)
		h.appendIdentityEvent(newName, identityEventUpdated, actor, "renamed from "+name)
	}
	event := identityEventUpdated
	detail := ""
	if in.Broken != nil && *in.Broken && !wasBroken {
		event, detail = identityEventMarkedBroken, updated.BrokenNote
	}
	h.appendIdentityEvent(newName, event, actor, detail)
	writeJSON(w, http.StatusOK, map[string]any{"identity": updated})
	h.broadcastAuthzIdentities(newName)
}

// identityCheckVerdict classifies one session probe. It reuses the same
// evidence rules as POST /api/authz/check-sessions (sessionLooksInvalid).
func identityCheckVerdict(rr authzResult, hasAuth bool) (string, string) {
	switch {
	case rr.Error != "":
		return identityVerdictError, rr.Error
	case sessionLooksInvalid(rr.Status, hasAuth, rr.resHeaders):
		return identityVerdictExpired, "auth challenge on the replay"
	case accessDeniedStatus(rr.Status):
		return identityVerdictDenied, ""
	case rr.Status >= 200 && rr.Status < 400:
		return identityVerdictOK, ""
	case rr.Status == 0:
		return identityVerdictError, "no response"
	}
	return identityVerdictError, "unexpected status " + strconv.Itoa(rr.Status)
}

// checkAuthzIdentity is POST /api/authz/identities/{name}/check: replay one
// flow as this identity and store the verdict as its lastCheck.
func (h *authzAPI) checkAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	name, ok := identityNameParam(w, r)
	if !ok {
		return
	}
	var in struct {
		FlowID int64 `json:"flowId"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxIdentityCheckBytes, &in) {
		return
	}
	id, ok := h.loadIdentity(w, name)
	if !ok {
		return
	}
	flowID := in.FlowID
	if flowID == 0 && id.Source != nil {
		flowID = id.Source.FlowID
	}
	if flowID <= 0 {
		httpErr(w, http.StatusBadRequest, "flowId required — this identity has no source flow to replay")
		return
	}
	f, err := h.st.GetFlow(flowID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	rr := h.authzReplay(f, id, aiSourceFlag(r))
	verdict, note := identityCheckVerdict(rr, identityHasAuth(id))
	chk := identityCheck{
		TS: time.Now().UTC().Format(time.RFC3339), Status: rr.Status,
		Verdict: verdict, FlowID: rr.FlowID, Note: note,
	}
	actor := identityActor(r, "")
	var updated identity
	if _, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		for i := range cur {
			if cur[i].Name == name {
				cur[i].LastCheck = &chk
				updated = cur[i]
				break
			}
		}
		return cur, nil
	}); err != nil {
		httpInternalErr(w, err)
		return
	}
	if updated.Name == "" { // renamed or deleted while the replay ran
		httpErr(w, http.StatusNotFound, "identity not found")
		return
	}
	h.appendIdentityEvent(name, identityEventChecked, actor,
		fmt.Sprintf("%s on flow #%d (status %d)", verdict, flowID, rr.Status))
	writeJSON(w, http.StatusOK, map[string]any{"identity": updated, "lastCheck": chk})
	h.broadcastAuthzIdentities(name)
}

// refreshAuthzIdentityFromFlow is POST
// /api/authz/identities/{name}/refresh-from-flow/{id}: re-extract the auth
// headers from a flow into an existing identity and re-point its source.
func (h *authzAPI) refreshAuthzIdentityFromFlow(w http.ResponseWriter, r *http.Request) {
	name, ok := identityNameParam(w, r)
	if !ok {
		return
	}
	flowID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || flowID <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, ok := h.loadIdentity(w, name); !ok {
		return
	}
	if _, err := h.st.GetFlow(flowID); err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	actor := identityActor(r, "")
	ids, err := h.promoteFlowToAuthz(flowID, name, true, actor, identityEventRefreshed)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, _ := findIdentity(ids, name)
	writeJSON(w, http.StatusOK, map[string]any{"identity": updated})
	h.broadcastAuthzIdentities(name)
}
