package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interceptor/internal/activescan/csrf"
	"github.com/Veyal/interceptor/internal/store"
)

// flowAuthPayload is the structured auth material MCP agents consume.
func flowAuthPayload(f *store.Flow) map[string]any {
	suggested := extractAuthHeaders(f.ReqHeaders)
	cookie := headerValue(f.ReqHeaders, "Cookie")
	authz := headerValue(f.ReqHeaders, "Authorization")
	xsrf := headerValue(f.ReqHeaders, "X-Xsrf-Token")
	if xsrf == "" {
		xsrf = headerValue(f.ReqHeaders, "X-XSRF-TOKEN")
	}
	if xsrf == "" {
		xsrf = csrf.XSRFFromCookie(cookie)
	}
	out := map[string]any{
		"flowId":           f.ID,
		"cookie":           cookie,
		"authorization":    authz,
		"xsrfHeader":       xsrf,
		"suggestedHeaders": suggested,
		"cookieHints":      cookieExpiryHints(f.ResHeaders),
	}
	return out
}

func headerValue(hdrs map[string][]string, key string) string {
	if hdrs == nil {
		return ""
	}
	if vs := hdrs[key]; len(vs) > 0 {
		return strings.TrimSpace(vs[0])
	}
	return ""
}

// authzPromoteFromFlow adds/updates an authz identity from a captured flow.
func (h *authzAPI) authzPromoteFromFlow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Name  string `json:"name"`
		Merge *bool  `json:"merge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	merge := true
	if in.Merge != nil {
		merge = *in.Merge
	}
	ids, err := h.promoteFlowToAuthz(id, name, merge)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": ids})
}

func (h *authzAPI) promoteFlowToAuthz(flowID int64, name string, merge bool) ([]identity, error) {
	f, err := h.st.GetFlow(flowID)
	if err != nil {
		return nil, err
	}
	headers := extractAuthHeaders(f.ReqHeaders)
	if strings.TrimSpace(headers) == "" {
		return nil, fmt.Errorf("flow #%d has no Cookie/Authorization headers to promote", flowID)
	}
	newID := identity{Name: name, Headers: headers}
	ids := h.authzIdentities()
	if merge {
		updated := false
		for i, id := range ids {
			if id.Name == name {
				ids[i] = newID
				updated = true
				break
			}
		}
		if !updated {
			ids = append(ids, newID)
		}
	} else {
		ids = append(ids, newID)
	}
	b, _ := json.Marshal(ids)
	if err := h.st.SetSetting("authz.identities", string(b)); err != nil {
		return nil, err
	}
	return ids, nil
}
