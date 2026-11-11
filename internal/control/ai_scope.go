package control

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// AI guardrails for request-generating tools.
//
// The MCP bridge stamps every control call with X-Interseptor-Source: ai.
// Collections already force scopePolicy=block for that channel; the direct
// send paths (Repeater, replay, Intruder, WebSocket send, finding retest and
// authz replays) did not, so an autonomous or prompt-injected agent could aim
// attack traffic at any host. These helpers give every such path the same
// rule: an AI-originated send must target a URL the project scope admits
// (an empty scope admits everything, exactly as for captured flows), and the
// AI channel may not widen an allow-list the operator has set.

// aiMaxIntruderThreads bounds Intruder concurrency for AI-started attacks; the
// operator can still run wider attacks from the UI.
const aiMaxIntruderThreads = 10

// aiScopeFix is the remediation every out-of-scope refusal carries.
const aiScopeFix = "ask the operator (request_human_input) to add the host to scope in the UI; the AI channel cannot send outside scope"

// aiInvalidTargetPrefix starts the refusal for a target that cannot be
// scope-checked at all; writeAIScopeRefusal reports it as code invalid_target.
const aiInvalidTargetPrefix = "invalid_target: "

// aiScopeRefusal returns a refusal message when an AI-originated send (flags
// carries store.FlagAI) targets rawURL outside the project scope, or "" when
// the send may proceed. pathHint overrides the URL path (Intruder targets
// carry only scheme://host[:port]; the path lives in the template).
func (h *Hub) aiScopeRefusal(flags int64, rawURL, pathHint string) string {
	if flags&store.FlagAI == 0 {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		// Fail closed: a target whose host cannot be read cannot be shown to be
		// in scope. Allowing it left the decision to whatever the send path made
		// of the same string later.
		return aiInvalidTargetPrefix + "the target URL must be absolute with a host (scheme://host[:port]/path) so it can be checked against the project scope"
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme { // WebSocket targets are scoped like their HTTP origin
	case "ws":
		scheme = "http"
	case "wss":
		scheme = "https"
	}
	path := u.Path
	if pathHint != "" {
		path = pathHint
	}
	if path == "" {
		path = "/"
	}
	port := atoiOr(u.Port(), defaultPortFor(scheme))
	if h.sc.URLInScope(scheme, u.Hostname(), port, path) {
		return ""
	}
	return fmt.Sprintf("out_of_scope: %s://%s%s is outside the project scope", scheme, u.Host, path)
}

// writeAIScopeRefusal answers a refused AI send with a structured 403 the MCP
// bridge relays verbatim.
func writeAIScopeRefusal(w http.ResponseWriter, msg, rawURL string) {
	host := ""
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		host = u.Hostname()
	}
	if strings.HasPrefix(msg, aiInvalidTargetPrefix) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": msg,
			"code":  "invalid_target",
			"host":  host,
			"fix":   "send an absolute http(s) or ws(s) URL with a host",
		})
		return
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": msg,
		"code":  "out_of_scope",
		"host":  host,
		"fix":   aiScopeFix,
	})
}

// templatePath extracts the request-target path from a raw Intruder template
// so path-pinned scope rules apply to the attack, ignoring §…§ markers.
func templatePath(template string) string {
	line, _, _ := strings.Cut(strings.TrimLeft(template, "\r\n"), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	target := strings.ReplaceAll(fields[1], "§", "")
	if u, err := url.Parse(target); err == nil && u.IsAbs() {
		target = u.RequestURI()
	}
	path, _, _ := strings.Cut(target, "?")
	return path
}

// aiScopeWideningRefusal reports whether an AI-originated scope change would
// widen an allow-list the operator set. With no include rule yet, the agent
// may establish the first one (scope_from_url on a fresh project); after that
// it may only add exclude rules or repeat an identical include. Updating or
// deleting rules can always widen scope, so the AI channel may not do either.
func (h *Hub) aiScopeWideningRefusal(r *http.Request, in *store.ScopeRule) string {
	if !isAISource(r) {
		return ""
	}
	if in == nil {
		return "the AI channel cannot update or delete scope rules; " + aiScopeFix
	}
	if !in.Enabled || !strings.EqualFold(in.Action, "include") || !h.sc.HasIncludes() {
		return ""
	}
	return "the AI channel cannot widen an existing scope allow-list; " + aiScopeFix
}

// identicalScopeRule returns an existing rule equal to in, so a repeated
// scope_from_url is idempotent instead of stacking duplicate rules.
func (h *Hub) identicalScopeRule(in store.ScopeRule) (*store.ScopeRule, error) {
	rules, err := h.st.ListScopeRules()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		r := rules[i]
		if r.Enabled == in.Enabled && strings.EqualFold(r.Action, in.Action) && strings.EqualFold(r.Host, in.Host) &&
			r.Path == in.Path && strings.EqualFold(r.Scheme, in.Scheme) && r.Port == in.Port {
			return &r, nil
		}
	}
	return nil, nil
}
