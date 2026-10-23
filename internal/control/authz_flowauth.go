package control

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/csrf"
	"github.com/Veyal/interseptor/internal/store"
)

const maxAuthzFlowPromotionRequestBytes int64 = 64 << 10

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
	if !decodeOptionalLimitedJSON(w, r, maxAuthzFlowPromotionRequestBytes, &in) {
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
	actor := identityActor(r, "")
	ids, err := h.promoteFlowToAuthz(id, name, merge, actor, identityEventUpdated)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": ids})
	h.broadcastAuthzIdentities(name)
}

// promoteFlowToAuthz writes a flow's captured auth headers into the identity
// named name and records the flow as the identity's source. updateEvent is the
// event kind logged when the identity already existed ("updated" for a
// promote, "refreshed" for an explicit refresh-from-flow).
func (h *authzAPI) promoteFlowToAuthz(flowID int64, name string, merge bool, actor, updateEvent string) ([]identity, error) {
	f, err := h.st.GetFlow(flowID)
	if err != nil {
		return nil, err
	}
	headers := extractAuthHeaders(f.ReqHeaders)
	if strings.TrimSpace(headers) == "" {
		return nil, fmt.Errorf("flow #%d has no Cookie/Authorization headers to promote", flowID)
	}
	src := &identitySource{Kind: identitySourceFlow, FlowID: f.ID, Method: f.Method, Host: f.Host, Path: f.Path}
	newID := stampIdentity(identity{Name: name, Headers: headers, Source: src}, actor, time.Now())
	event := identityEventCreated
	ids, err := h.mutateIdentities(func(ids []identity) ([]identity, error) {
		if _, existed := findIdentity(ids, newID.Name); existed {
			event = updateEvent
		}
		if merge {
			// New credentials invalidate the previous session check.
			ids = upsertIdentity(ids, newID)
			for i := range ids {
				if ids[i].Name == newID.Name {
					ids[i].LastCheck = nil
				}
			}
			return ids, nil
		}
		return append(ids, newID), nil
	})
	if err != nil {
		return nil, err
	}
	h.appendIdentityEvent(newID.Name, event, actor, "auth headers from flow #"+itoa64(f.ID))
	return ids, nil
}
