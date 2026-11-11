package control

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/playbooks"
	"github.com/Veyal/interseptor/internal/store"
)

// Playbooks are the markdown test guides an AI harness reads and executes. The
// control plane serves them because the project directory lives here: bundled
// ones are compiled in, project ones sit under <project>/playbooks.

// listPlaybooks serves GET /api/playbooks.
//
//	?host=api.example&path=/orders/7&param=id
//
// The filters are old-style "what applies here" queries; without them the whole
// catalogue is returned.
func (h *playbooksAPI) listPlaybooks(w http.ResponseWriter, r *http.Request) {
	all, err := playbooks.Load(h.ProjectDir)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	obs := playbooks.Observed{
		Host:   r.URL.Query().Get("host"),
		Path:   r.URL.Query().Get("path"),
		Method: r.URL.Query().Get("method"),
		Mime:   r.URL.Query().Get("mime"),
	}
	obs.Params = splitCSVQuery(r.URL.Query().Get("param"))
	summaries := make([]playbooks.Summary, 0, len(all))
	matched := make([]playbooks.Summary, 0)
	anyFilter := obs.Host != "" || obs.Path != "" || obs.Method != "" || obs.Mime != "" || len(obs.Params) > 0
	for _, p := range all {
		s := p.Summarize()
		summaries = append(summaries, s)
		if anyFilter && p.Match(obs) != "" {
			matched = append(matched, s)
		}
	}
	out := map[string]any{
		"playbooks": summaries,
		"count":     len(summaries),
		"bundled":   countSource(summaries, playbooks.SourceBundled),
		"project":   countSource(summaries, playbooks.SourceProject),
		// Untrusted project playbooks are called out so a harness can treat their
		// text as content rather than as authority.
		"untrusted": countUntrusted(summaries),
	}
	if anyFilter {
		out["matched"] = matched
	}
	writeJSON(w, http.StatusOK, out)
}

// getPlaybook serves GET /api/playbooks/{id}: the full markdown plus metadata.
func (h *playbooksAPI) getPlaybook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := playbooks.Get(h.ProjectDir, id)
	if !ok {
		httpErr(w, http.StatusNotFound, "playbook not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playbook": p.Summarize(),
		"markdown": p.Rendered(),
		"body":     p.Body,
	})
}

// matchPlaybooks serves GET /api/playbooks/match?flowId=N: which playbooks apply
// to a captured flow, with the signal that matched. This is the nudge an agent
// polling traffic needs, and the reason get_flow can say "3 playbooks apply".
func (h *playbooksAPI) matchPlaybooks(w http.ResponseWriter, r *http.Request) {
	id := int64(0)
	if raw := r.URL.Query().Get("flowId"); raw != "" {
		if n, err := parseIntParam(raw); err == nil {
			id = int64(n)
		}
	}
	if id == 0 {
		httpErr(w, http.StatusBadRequest, "flowId is required")
		return
	}
	f, err := h.st.GetFlow(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	obs := h.observedForFlow(f)
	all, err := playbooks.Load(h.ProjectDir)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type hit struct {
		playbooks.Summary
		MatchedOn string `json:"matchedOn"`
	}
	var hits []hit
	for _, p := range all {
		if reason := p.Match(obs); reason != "" {
			hits = append(hits, hit{Summary: p.Summarize(), MatchedOn: reason})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"flowId": id, "observed": obs, "playbooks": hits, "count": len(hits)})
}

// observedForFlow extracts the matching signals from a captured flow: query and
// form parameter names, JSON body keys, header names and the response body.
func (h *Hub) observedForFlow(f *store.Flow) playbooks.Observed {
	obs := playbooks.Observed{
		Method:  f.Method,
		Host:    strings.ToLower(f.Host),
		Path:    f.Path,
		Mime:    f.Mime,
		Headers: map[string]string{},
	}
	if u, err := url.Parse("http://" + f.Host + f.Path); err == nil {
		obs.Path = u.Path
		for name := range u.Query() {
			obs.Params = append(obs.Params, name)
		}
	}
	for name, values := range f.ReqHeaders {
		if len(values) > 0 {
			obs.Headers[name] = values[0]
		}
	}
	// Bounded reads: a match needs the head of the response and the request's
	// parameter names, never a whole upload or download in memory. This runs
	// for the newest flow on every readiness refresh.
	_, res, _, _ := h.bodyForDisplayLimit(f.ResBodyHash, f.ResHeaders, playbookResponseScan)
	obs.Body = string(res)
	_, req, cut, _ := h.bodyForDisplayLimit(f.ReqBodyHash, f.ReqHeaders, playbookRequestScan)
	if cut {
		obs.Params = append(obs.Params, paramNamesFromTruncatedBody(string(req))...)
	} else {
		obs.Params = append(obs.Params, paramNamesFromBody(string(req))...)
	}
	obs.Params = dedupeStrings(obs.Params)
	return obs
}

const (
	// playbookResponseScan is how much of the response a body signal sees.
	playbookResponseScan = 64 * 1024
	// playbookRequestScan is how much of the request is read for its parameter
	// names; names past it are not reported.
	playbookRequestScan = 256 * 1024
)

// paramNamesFromTruncatedBody is paramNamesFromBody for a body cut at the read
// limit: it reports the names that are complete in the prefix. For JSON those
// are the top-level keys read before the cut; for a form, every pair before
// the last "&" (the final segment may be half a name).
func paramNamesFromTruncatedBody(body string) []string {
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		dec := json.NewDecoder(strings.NewReader(body))
		var names []string
		if _, err := dec.Token(); err == nil {
			for dec.More() {
				key, err := dec.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					break
				}
				names = append(names, name)
				var skip json.RawMessage
				if dec.Decode(&skip) != nil {
					break
				}
			}
		}
		if len(names) > 0 {
			return names
		}
	}
	i := strings.LastIndexByte(body, '&')
	if i < 0 {
		return nil
	}
	return paramNamesFromBody(body[:i])
}

// paramNamesFromBody lists form and JSON top-level keys, which is where a
// tampering or IDOR target hides in a POST.
func paramNamesFromBody(body string) []string {
	if body == "" {
		return nil
	}
	var names []string
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(body), &obj) == nil {
			for k := range obj {
				names = append(names, k)
			}
			return names
		}
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil
	}
	for k := range values {
		names = append(names, k)
	}
	return names
}

// listPlaybookCoverage serves GET /api/playbooks/coverage.
func (h *playbooksAPI) listPlaybookCoverage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := h.st.ListPlaybookCoverage(store.PlaybookCoverageFilter{
		PlaybookID: q.Get("playbookId"),
		Host:       q.Get("host"),
		Endpoint:   q.Get("endpoint"),
		Param:      q.Get("param"),
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum, err := h.st.CoverageSummary()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coverage": orEmptyCoverage(rows), "summary": sum})
}

// recordPlaybookCoverage serves POST /api/playbooks/coverage.
func (h *playbooksAPI) recordPlaybookCoverage(w http.ResponseWriter, r *http.Request) {
	var in store.PlaybookCoverage
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	rec, err := h.st.RecordPlaybookCoverage(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// orEmptyCoverage keeps the JSON array shape stable for an empty ledger.
func orEmptyCoverage(v []store.PlaybookCoverage) []store.PlaybookCoverage {
	if v == nil {
		return []store.PlaybookCoverage{}
	}
	return v
}

func countSource(list []playbooks.Summary, source string) int {
	n := 0
	for _, s := range list {
		if s.Source == source {
			n++
		}
	}
	return n
}

func countUntrusted(list []playbooks.Summary) int {
	n := 0
	for _, s := range list {
		if !s.Trusted {
			n++
		}
	}
	return n
}

// splitCSVQuery splits a comma-separated query value.
func splitCSVQuery(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// dedupeStrings keeps the first occurrence order and drops empties.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
