package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// Authorization (access-control) testing: replay captured request(s) under each
// saved identity (role) and diff the responses. If a lower-privileged identity
// still gets a successful, ~same-size response as the baseline, that's a strong
// signal of broken access control (IDOR / privilege escalation — OWASP #1).

// identity is a named set of auth headers (a user/role/anonymous).
type identity struct {
	Name       string `json:"name"`
	Headers    string `json:"headers"` // "Key: Value" lines (Cookie / Authorization / …)
	Broken     bool   `json:"broken,omitempty"`
	BrokenNote string `json:"brokenNote,omitempty"` // e.g. "locked after rate-limit test"
}

type authzResult struct {
	Name           string `json:"name"`
	Status         int    `json:"status"`
	Length         int64  `json:"length"`
	Mime           string `json:"mime"`
	Error          string `json:"error"`
	FlowID         int64  `json:"flowId"`
	BodyHash       string `json:"bodyHash,omitempty"`
	Same           bool   `json:"sameAsBaseline"`
	SessionInvalid bool   `json:"sessionInvalid,omitempty"`
	Broken         bool   `json:"broken,omitempty"`
}

type authzRunOut struct {
	FlowID         int64         `json:"flowId,omitempty"`
	Method         string        `json:"method,omitempty"`
	Host           string        `json:"host,omitempty"`
	Path           string        `json:"path,omitempty"`
	BaselineStatus int           `json:"baselineStatus"`
	Results        []authzResult `json:"results"`
}

func (h *Hub) authzIdentities() []identity {
	raw, _, _ := h.st.GetSetting("authz.identities")
	var ids []identity
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	return ids
}

func (h *Hub) getAuthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"identities": h.authzIdentities()})
}

func (h *Hub) setAuthz(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Identities []identity `json:"identities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	b, _ := json.Marshal(in.Identities)
	if err := h.st.SetSetting("authz.identities", string(b)); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": in.Identities})
}

// authzFlowAuth returns Cookie/Authorization from a flow's request plus optional
// expiry hints parsed from the captured response Set-Cookie headers.
func (h *Hub) authzFlowAuth(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	f, err := h.st.GetFlow(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requestAuth":  extractAuthHeaders(f.ReqHeaders),
		"cookieHints":  cookieExpiryHints(f.ResHeaders),
	})
}

// authzCheckSessions replays one flow under each identity and reports whether
// each session still looks valid (401/403 when auth headers are set = invalid).
func (h *Hub) authzCheckSessions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID int64 `json:"flowId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 {
		httpErr(w, http.StatusBadRequest, "flowId required — pick a session probe (e.g. GET /api/me)")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	ids := h.authzIdentities()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no identities configured")
		return
	}
	type check struct {
		Name           string `json:"name"`
		Status         int    `json:"status"`
		Error          string `json:"error"`
		SessionInvalid bool   `json:"sessionInvalid"`
		HasAuth        bool   `json:"hasAuth"`
		Broken         bool   `json:"broken,omitempty"`
	}
	var out []check
	for _, id := range ids {
		if id.Broken {
			out = append(out, check{Name: id.Name, Broken: true, Error: "skipped — broken account"})
			continue
		}
		hasAuth := identityHasAuth(id)
		rr := h.authzReplay(f, id)
		out = append(out, check{
			Name:           id.Name,
			Status:         rr.Status,
			Error:          rr.Error,
			SessionInvalid: sessionLooksInvalid(rr.Status, hasAuth),
			HasAuth:        hasAuth,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"flowId": in.FlowID, "checks": out})
}

// authzRun replays flow(s) under each identity and reports per-identity diffs.
func (h *Hub) authzRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID      int64 `json:"flowId"`
		InScope     bool  `json:"inScope"`
		MaxFlows    int   `json:"maxFlows"`
		SkipStatic  *bool `json:"skipStatic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 && !in.InScope {
		httpErr(w, http.StatusBadRequest, "specify flowId or inScope:true")
		return
	}
	if in.InScope && !h.sc.HasIncludes() {
		httpErr(w, http.StatusBadRequest, "define a target-scope include rule before an “all in-scope” authz run — with no scope it would replay every captured endpoint")
		return
	}
	ids := h.authzIdentities()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no identities configured — add at least one (name + auth headers) first")
		return
	}

	var flows []*store.Flow
	skipStatic := in.InScope
	if in.SkipStatic != nil {
		skipStatic = *in.SkipStatic
	}
	if in.FlowID > 0 {
		f, err := h.st.GetFlow(in.FlowID)
		if err != nil {
			httpErr(w, http.StatusNotFound, "flow not found")
			return
		}
		flows = []*store.Flow{f}
	} else {
		limit := in.MaxFlows
		if limit <= 0 {
			limit = 30
		}
		if limit > 100 {
			limit = 100
		}
		raw, _ := h.st.QueryFlowsFilter(store.FlowFilter{
			Limit:        limit * 5, // over-fetch; authzTargets dedupes + static filter
			ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan,
		})
		flows = h.authzTargets(raw, skipStatic)
		if len(flows) > limit {
			flows = flows[:limit]
		}
	}
	if len(flows) == 0 {
		httpErr(w, http.StatusBadRequest, "no in-scope endpoints to test")
		return
	}

	var runs []authzRunOut
	flagged := 0
	for _, f := range flows {
		ro := h.authzRunOne(f, ids)
		runs = append(runs, ro)
		for i, rr := range ro.Results {
			if i > 0 && rr.Same {
				flagged++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs": runs,
		"summary": map[string]any{
			"endpoints": len(runs),
			"flagged":   flagged,
		},
	})
}

func (h *Hub) authzRunOne(f *store.Flow, ids []identity) authzRunOut {
	ro := authzRunOut{
		FlowID: f.ID, Method: f.Method, Host: f.Host, Path: f.Path,
	}
	var baseLen int64
	var baseHash, baseMime string
	haveBase := false
	for _, id := range ids {
		if id.Broken {
			note := id.BrokenNote
			if note == "" {
				note = "skipped — account marked broken"
			} else {
				note = "skipped — " + note
			}
			ro.Results = append(ro.Results, authzResult{Name: id.Name, Error: note, Broken: true})
			continue
		}
		hasAuth := identityHasAuth(id)
		rr := h.authzReplay(f, id)
		rr.Name = id.Name
		rr.SessionInvalid = sessionLooksInvalid(rr.Status, hasAuth)
		if !haveBase {
			ro.BaselineStatus, baseLen, baseHash, baseMime, haveBase = rr.Status, rr.Length, rr.BodyHash, rr.Mime, true
		} else {
			rr.Same = authzSameAccess(ro.BaselineStatus, baseLen, baseHash, baseMime, rr)
		}
		ro.Results = append(ro.Results, rr)
	}
	return ro
}

func (h *Hub) authzReplay(f *store.Flow, id identity) authzResult {
	url := flowURLStr(f)
	body := h.bodyBytes(f.ReqBodyHash)
	hdrs := applyIdentityHeaders(f.ReqHeaders, id)
	flow, _ := h.snd.Send(sender.Request{Method: f.Method, URL: url, Headers: hdrs, Body: body, Flags: store.FlagAuthz, NoSession: true})
	rr := authzResult{Name: id.Name}
	if flow != nil {
		rr.Status, rr.Length, rr.Mime, rr.Error, rr.FlowID = flow.Status, flow.ResLen, flow.Mime, flow.Error, flow.ID
		if flow.ResBodyHash != "" {
			resBody := h.bodyBytes(flow.ResBodyHash)
			rr.BodyHash = bodySHA256(resBody)
		}
	}
	return rr
}

// authzTargets keeps in-scope flows deduped by method+host+path.
func (h *Hub) authzTargets(flows []*store.Flow, skipStatic bool) []*store.Flow {
	seen := map[string]bool{}
	var out []*store.Flow
	for _, f := range flows {
		if !h.sc.InScope(f) || h.isOwnListener(f) {
			continue
		}
		if skipStatic && authzSkipStatic(f) {
			continue
		}
		key := f.Method + " " + f.Host + f.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

func applyIdentityHeaders(base map[string][]string, id identity) map[string][]string {
	out := cloneHeaders(base)
	if strings.TrimSpace(id.Headers) == "" {
		stripAuthHeaders(out)
		return out
	}
	for _, line := range strings.Split(id.Headers, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out[http.CanonicalHeaderKey(k)] = []string{strings.TrimSpace(v)}
		}
	}
	return out
}

func identityHasAuth(id identity) bool {
	if strings.TrimSpace(id.Headers) == "" {
		return false
	}
	for _, line := range strings.Split(id.Headers, "\n") {
		line = strings.TrimRight(line, "\r")
		k, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if isAuthHeaderKey(k) {
			return true
		}
	}
	return false
}

// sessionLooksInvalid is true when a replay with auth headers got 401/403.
func sessionLooksInvalid(status int, hasAuth bool) bool {
	return hasAuth && (status == http.StatusUnauthorized || status == http.StatusForbidden)
}

// cookieExpiryHints parses Set-Cookie attributes from a captured response.
func cookieExpiryHints(resHdr map[string][]string) []string {
	if resHdr == nil {
		return nil
	}
	var hints []string
	for _, raw := range resHdr["Set-Cookie"] {
		name := cookieName(raw)
		if name == "" {
			continue
		}
		if exp := cookieAttr(raw, "expires"); exp != "" {
			if t, err := http.ParseTime(exp); err == nil && t.Before(time.Now()) {
				hints = append(hints, name+": expired at capture ("+exp+")")
				continue
			}
			hints = append(hints, name+": Expires "+exp)
			continue
		}
		if age := cookieAttr(raw, "max-age"); age != "" {
			hints = append(hints, name+": Max-Age "+age)
		}
	}
	return hints
}

func cookieName(setCookie string) string {
	nameVal, _, _ := strings.Cut(setCookie, ";")
	nameVal = strings.TrimSpace(nameVal)
	if nameVal == "" {
		return ""
	}
	name, _, ok := strings.Cut(nameVal, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

func cookieAttr(setCookie, key string) string {
	key = strings.ToLower(key)
	for _, part := range strings.Split(setCookie, ";") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// extractBearerToken returns the raw JWT from an Authorization: Bearer header.
func extractBearerToken(hdrs map[string][]string) string {
	for _, v := range hdrs["Authorization"] {
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			return strings.TrimSpace(v[7:])
		}
	}
	return ""
}

// authzCrossHostReplay extracts a JWT from one flow and replays the reference
// endpoint's path to every unique in-scope host seen in history. This detects
// cross-environment JWT confusion (e.g. a host-A Bearer token accepted on host-B
// because both environments share the same JWT secret).
func (h *Hub) authzCrossHostReplay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID    int64  `json:"flowId"`    // reference endpoint (path to replay)
		JWTFlowID int64  `json:"jwtFlowId"` // source of JWT; defaults to flowId
		JWT       string `json:"jwt"`        // raw JWT (alternative to jwtFlowId)
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 {
		httpErr(w, http.StatusBadRequest, "flowId required")
		return
	}

	ref, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}

	jwt := strings.TrimSpace(in.JWT)
	if jwt == "" {
		srcID := in.JWTFlowID
		if srcID == 0 {
			srcID = in.FlowID
		}
		var srcFlow *store.Flow
		if srcID == in.FlowID {
			srcFlow = ref
		} else {
			srcFlow, err = h.st.GetFlow(srcID)
			if err != nil {
				httpErr(w, http.StatusNotFound, "jwtFlowId flow not found")
				return
			}
		}
		jwt = extractBearerToken(srcFlow.ReqHeaders)
	}
	if jwt == "" {
		httpErr(w, http.StatusBadRequest, "no Bearer token found — provide jwt directly or pick a flow that has an Authorization: Bearer header")
		return
	}

	type hostKey struct {
		host, scheme string
		port         int
	}
	allFlows, _ := h.st.QueryFlowsFilter(store.FlowFilter{
		Limit:        5000,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan | store.FlagAuthz,
	})
	seen := map[hostKey]bool{}
	var targets []hostKey
	for _, f := range allFlows {
		if !h.sc.InScope(f) || h.isOwnListener(f) {
			continue
		}
		k := hostKey{f.Host, f.Scheme, f.Port}
		if !seen[k] {
			seen[k] = true
			targets = append(targets, k)
		}
	}
	if len(targets) == 0 {
		targets = []hostKey{{ref.Host, ref.Scheme, ref.Port}}
	}

	path := ref.Path
	if path == "" {
		path = "/"
	}

	type hostResult struct {
		Host     string `json:"host"`
		Scheme   string `json:"scheme"`
		Port     int    `json:"port"`
		URL      string `json:"url"`
		Status   int    `json:"status"`
		Length   int64  `json:"length"`
		Accepted bool   `json:"accepted"`
		FlowID   int64  `json:"flowId,omitempty"`
		Error    string `json:"error,omitempty"`
	}

	var results []hostResult
	for _, t := range targets {
		hostport := t.host
		def := (t.scheme == "https" && t.port == 443) || (t.scheme == "http" && t.port == 80)
		if t.port != 0 && !def {
			hostport += ":" + strconv.Itoa(t.port)
		}
		targetURL := t.scheme + "://" + hostport + path

		hdrs := cloneHeaders(ref.ReqHeaders)
		hdrs["Authorization"] = []string{"Bearer " + jwt}

		flow, _ := h.snd.Send(sender.Request{
			Method:    ref.Method,
			URL:       targetURL,
			Headers:   hdrs,
			Body:      h.bodyBytes(ref.ReqBodyHash),
			Flags:     store.FlagAuthz,
			NoSession: true,
		})

		hr := hostResult{Host: t.host, Scheme: t.scheme, Port: t.port, URL: targetURL}
		if flow != nil {
			hr.Status = flow.Status
			hr.Length = flow.ResLen
			hr.FlowID = flow.ID
			hr.Error = flow.Error
			hr.Accepted = flow.Status >= 200 && flow.Status < 300
		}
		results = append(results, hr)
	}

	jwtPreview := jwt
	if len(jwtPreview) > 50 {
		jwtPreview = jwtPreview[:50] + "…"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"flowId":  in.FlowID,
		"method":  ref.Method,
		"path":    path,
		"jwt":     jwtPreview,
		"results": results,
	})
}

// flowURLStr reconstructs the absolute URL of a captured flow.
func flowURLStr(f *store.Flow) string {
	hostport := f.Host
	def := (f.Scheme == "https" && f.Port == 443) || (f.Scheme == "http" && f.Port == 80)
	if f.Port != 0 && !def {
		hostport += ":" + strconv.Itoa(f.Port)
	}
	path := f.Path
	if path == "" {
		path = "/"
	}
	return f.Scheme + "://" + hostport + path
}

func cloneHeaders(h map[string][]string) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
