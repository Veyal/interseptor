package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// Authorization (access-control) testing: replay one captured request under each
// saved identity (role) and diff the responses. If a lower-privileged identity
// still gets a successful, ~same-size response as the baseline, that's a strong
// signal of broken access control (IDOR / privilege escalation — OWASP #1).

// identity is a named set of auth headers (a user/role/anonymous).
type identity struct {
	Name    string `json:"name"`
	Headers string `json:"headers"` // "Key: Value" lines (Cookie / Authorization / …)
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

// authzRun replays a flow's request under each identity and reports the per-identity
// status/length plus a "same access as baseline" flag (the first identity is the
// baseline — typically your most-privileged role).
func (h *Hub) authzRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID int64 `json:"flowId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	ids := h.authzIdentities()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no identities configured — add at least one (name + auth headers) first")
		return
	}
	url := flowURLStr(f)
	body := h.bodyBytes(f.ReqBodyHash)

	type result struct {
		Name   string `json:"name"`
		Status int    `json:"status"`
		Length int64  `json:"length"`
		Mime   string `json:"mime"`
		Error  string `json:"error"`
		FlowID int64  `json:"flowId"`
		Same   bool   `json:"sameAsBaseline"`
	}
	var out []result
	var baseLen int64
	var baseStatus int
	haveBase := false
	for _, id := range ids {
		hdrs := cloneHeaders(f.ReqHeaders)
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
				hdrs[http.CanonicalHeaderKey(k)] = []string{strings.TrimSpace(v)}
			}
		}
		flow, _ := h.snd.Send(sender.Request{Method: f.Method, URL: url, Headers: hdrs, Body: body, Flags: store.FlagAuthz, NoSession: true})
		rr := result{Name: id.Name}
		if flow != nil {
			rr.Status, rr.Length, rr.Mime, rr.Error, rr.FlowID = flow.Status, flow.ResLen, flow.Mime, flow.Error, flow.ID
		}
		if !haveBase {
			baseStatus, baseLen, haveBase = rr.Status, rr.Length, true
		} else if rr.Status > 0 && rr.Status < 400 && abs64(rr.Length-baseLen) <= max64(64, baseLen/20) {
			rr.Same = true // succeeded with a ~same-size body as the privileged baseline
		}
		out = append(out, rr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"baselineStatus": baseStatus, "results": out})
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
