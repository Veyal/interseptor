package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/searchscript"
	"github.com/Veyal/interseptor/internal/store"
)

const (
	flowSearchesSettingKey      = "flow.searches"
	maxFlowSearchBodyBytes      = 64 << 10
	maxFlowScriptCandidates     = 64
	maxFlowScriptTotalBodyBytes = 8 << 20
	maxFlowSearches             = 100
	maxFlowSearchNameBytes      = 128
	maxFlowAnywhereCandidates   = 8000
	maxFlowSearchRequestBytes   = 128 << 10
)

type savedFlowSearch struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Script string `json:"script,omitempty"`
}

type flowSearchInput struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Script string `json:"script"`
	FlowID int64  `json:"flowId,omitempty"`
}

func normalizeFlowSearchScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "body", "id":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "anywhere"
	}
}

func (h *flowAPI) listFlowSearches(w http.ResponseWriter, _ *http.Request) {
	searches, err := h.loadFlowSearches()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	out := make([]savedFlowSearch, len(searches))
	for i, search := range searches {
		out[i] = savedFlowSearch{Name: search.Name, Scope: search.Scope}
	}
	writeJSON(w, http.StatusOK, map[string]any{"searches": out})
}

func (h *flowAPI) getFlowSearchSource(w http.ResponseWriter, r *http.Request) {
	search, ok := h.findFlowSearch(w, r.PathValue("name"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, search)
}

func (h *flowAPI) createFlowSearch(w http.ResponseWriter, r *http.Request) {
	h.saveFlowSearch(w, r, http.StatusCreated, "")
}

func (h *flowAPI) updateFlowSearch(w http.ResponseWriter, r *http.Request) {
	h.saveFlowSearch(w, r, http.StatusOK, r.PathValue("name"))
}

func (h *flowAPI) deleteFlowSearch(w http.ResponseWriter, r *http.Request) {
	searches, err := h.loadFlowSearches()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	name := r.PathValue("name")
	kept := searches[:0]
	for _, search := range searches {
		if search.Name != name {
			kept = append(kept, search)
		}
	}
	if len(kept) == len(searches) {
		httpErr(w, http.StatusNotFound, "flow search not found")
		return
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if err := h.st.SetSetting(flowSearchesSettingKey, string(encoded)); err != nil {
		httpInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *flowAPI) testFlowSearch(w http.ResponseWriter, r *http.Request) {
	input, script, ok := decodeFlowSearch(w, r)
	if !ok {
		return
	}
	if input.FlowID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "name": input.Name, "scope": input.Scope, "compiled": script != nil})
		return
	}
	flow, err := h.st.GetFlow(input.FlowID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	budget := int64(maxFlowScriptTotalBodyBytes)
	matched, err := script.MatchContext(r.Context(), searchScriptFlow(h, flow, &budget))
	if err != nil {
		httpErr(w, http.StatusBadRequest, "search script execution failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "name": input.Name, "scope": input.Scope, "compiled": true, "flowId": flow.ID, "matched": matched})
}

func (h *flowAPI) saveFlowSearch(w http.ResponseWriter, r *http.Request, status int, pathName string) {
	input, _, ok := decodeFlowSearch(w, r)
	if !ok {
		return
	}
	if pathName != "" {
		input.Name = pathName
	}
	if input.Name == "" || len(input.Name) > maxFlowSearchNameBytes || strings.ContainsAny(input.Name, "/\\") {
		httpErr(w, http.StatusBadRequest, "invalid flow search name")
		return
	}
	searches, err := h.loadFlowSearches()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	saved := savedFlowSearch{Name: input.Name, Scope: input.Scope, Script: input.Script}
	replaced := false
	for i := range searches {
		if searches[i].Name == saved.Name {
			searches[i] = saved
			replaced = true
			break
		}
	}
	if !replaced {
		if len(searches) >= maxFlowSearches {
			httpErr(w, http.StatusBadRequest, "saved flow search limit reached")
			return
		}
		searches = append(searches, saved)
	}
	encoded, err := json.Marshal(searches)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if err := h.st.SetSetting(flowSearchesSettingKey, string(encoded)); err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"name": saved.Name, "scope": saved.Scope})
}

func decodeFlowSearch(w http.ResponseWriter, r *http.Request) (flowSearchInput, *searchscript.Script, bool) {
	var input flowSearchInput
	if !decodeLimitedJSONDisallowUnknownFields(w, r, maxFlowSearchRequestBytes, &input) {
		return flowSearchInput{}, nil, false
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Scope = normalizeFlowSearchScope(input.Scope)
	if input.Name == "" || strings.ContainsAny(input.Name, "/\\") {
		httpErr(w, http.StatusBadRequest, "invalid flow search name")
		return flowSearchInput{}, nil, false
	}
	script, err := searchscript.Compile(input.Script)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return flowSearchInput{}, nil, false
	}
	return input, script, true
}

func (h *flowAPI) loadFlowSearches() ([]savedFlowSearch, error) {
	raw, found, err := h.st.GetSetting(flowSearchesSettingKey)
	if err != nil || !found {
		return nil, err
	}
	var searches []savedFlowSearch
	if err := json.Unmarshal([]byte(raw), &searches); err != nil {
		return nil, fmt.Errorf("decode saved flow searches: %w", err)
	}
	if len(searches) > maxFlowSearches {
		return nil, fmt.Errorf("saved flow search limit exceeded")
	}
	return searches, nil
}

func (h *flowAPI) findFlowSearch(w http.ResponseWriter, name string) (savedFlowSearch, bool) {
	searches, err := h.loadFlowSearches()
	if err != nil {
		httpInternalErr(w, err)
		return savedFlowSearch{}, false
	}
	for _, search := range searches {
		if search.Name == name {
			return search, true
		}
	}
	httpErr(w, http.StatusNotFound, "flow search not found")
	return savedFlowSearch{}, false
}

func (h *flowAPI) applyFlowSearch(r *http.Request, filter *store.FlowFilter) (string, bool, error) {
	filter.SearchScope = normalizeFlowSearchScope(filter.SearchScope)
	if filter.Search == "" && strings.TrimSpace(r.URL.Query().Get("savedSearch")) == "" {
		return "", false, nil
	}
	if savedName := strings.TrimSpace(r.URL.Query().Get("savedSearch")); savedName != "" {
		searches, err := h.loadFlowSearches()
		if err != nil {
			return "", false, err
		}
		for _, saved := range searches {
			if saved.Name == savedName {
				note, err := h.matchFlowScript(r, filter, saved.Script)
				return note, len(filter.FlowIDs) == 0, err
			}
		}
		return "", false, fmt.Errorf("saved flow search not found")
	}
	if filter.SearchScope != "anywhere" {
		return "", false, nil
	}
	ids, note, err := h.st.FlowIDsAnywhereSearch(*filter, maxFlowAnywhereCandidates)
	filter.Search = ""
	filter.FlowIDs = ids
	return note, len(ids) == 0, err
}

func (h *flowAPI) matchFlowScript(r *http.Request, filter *store.FlowFilter, source string) (string, error) {
	script, err := searchscript.Compile(source)
	if err != nil {
		return "", err
	}
	candidates := *filter
	candidates.Search = ""
	candidates.SearchScope = ""
	candidates.Limit = maxFlowScriptCandidates
	flows, err := h.st.QueryFlowsFilter(candidates)
	if err != nil {
		return "", err
	}
	ids := make([]int64, 0, len(flows))
	bodyBudget := int64(maxFlowScriptTotalBodyBytes)
	for _, flow := range flows {
		if err := r.Context().Err(); err != nil {
			return "", err
		}
		matched, err := script.MatchContext(r.Context(), searchScriptFlow(h, flow, &bodyBudget))
		if err != nil {
			return "", err
		}
		if matched {
			ids = append(ids, flow.ID)
		}
	}
	filter.Search = ""
	filter.FlowIDs = ids
	if len(flows) == maxFlowScriptCandidates {
		return fmt.Sprintf("Saved search scanned the latest %d filtered flows.", maxFlowScriptCandidates), nil
	}
	return "", nil
}

func searchScriptFlow(h *flowAPI, flow *store.Flow, bodyBudget *int64) searchscript.Flow {
	return searchscript.Flow{
		Method: flow.Method, Scheme: flow.Scheme, Host: flow.Host, Port: flow.Port, Path: flow.Path,
		Status: flow.Status, Mime: flow.Mime, ReqHeaders: flow.ReqHeaders, ResHeaders: flow.ResHeaders,
		ReqBody: h.boundedFlowBody(flow.ReqBodyHash, bodyBudget), ResBody: h.boundedFlowBody(flow.ResBodyHash, bodyBudget),
	}
}

func (h *flowAPI) boundedFlowBody(hash string, remaining *int64) string {
	if hash == "" || remaining == nil || *remaining <= 0 {
		return ""
	}
	limit := min(int64(maxFlowSearchBodyBytes), *remaining)
	body, err := h.st.OpenBody(hash)
	if err != nil {
		return ""
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return ""
	}
	*remaining -= int64(len(data))
	return string(data)
}
