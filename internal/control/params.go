package control

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interceptor/internal/store"
)

type minedParam struct {
	Name       string `json:"name"`
	Source     string `json:"source"` // query, form, json
	Hits       int    `json:"hits"`
	LastFlowID int64  `json:"lastFlowId"`
	SamplePath string `json:"samplePath"`
}

type paramHostGroup struct {
	Host   string       `json:"host"`
	Params []minedParam `json:"params"`
}

type paramAggKey struct {
	host, name, source string
}

func (h *flowAPI) listParams(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host := q.Get("host")
	inScope := q.Get("inScope") == "1"
	limit := atoiOr(q.Get("limit"), 400)
	if limit < 1 {
		limit = 400
	}
	if limit > 2000 {
		limit = 2000
	}

	f := store.FlowFilter{
		Host:         host,
		SortKey:      "id",
		SortDir:      -1,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan,
	}

	var flows []*store.Flow
	var err error
	if inScope {
		flows, _, err = h.queryInScopeFlowsFull(f, limit)
	} else {
		f.Limit = limit + 1
		flows, err = h.st.QueryFlowsFilter(f)
		if len(flows) > limit {
			flows = flows[:limit]
		}
	}
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	agg := map[paramAggKey]*minedParam{}
	for _, fl := range flows {
		if inScope && !h.sc.InScope(fl) {
			continue
		}
		for _, n := range queryParamNames(fl.Path) {
			h.noteParam(agg, fl, n, "query")
		}
		reqBody := h.bodyBytes(fl.ReqBodyHash)
		ct := strings.ToLower(http.Header(fl.ReqHeaders).Get("Content-Type"))
		if strings.Contains(ct, "application/x-www-form-urlencoded") {
			if vals, perr := url.ParseQuery(string(reqBody)); perr == nil {
				for k := range vals {
					h.noteParam(agg, fl, k, "form")
				}
			}
		} else if strings.Contains(ct, "application/json") || strings.HasPrefix(strings.TrimSpace(string(reqBody)), "{") {
			for k := range jsonTopKeys(reqBody) {
				h.noteParam(agg, fl, k, "json")
			}
		}
	}

	byHost := map[string][]minedParam{}
	for k, p := range agg {
		byHost[k.host] = append(byHost[k.host], *p)
	}
	out := make([]paramHostGroup, 0, len(byHost))
	for host, params := range byHost {
		sort.Slice(params, func(i, j int) bool {
			if params[i].Hits != params[j].Hits {
				return params[i].Hits > params[j].Hits
			}
			if params[i].Source != params[j].Source {
				return params[i].Source < params[j].Source
			}
			return params[i].Name < params[j].Name
		})
		out = append(out, paramHostGroup{Host: host, Params: params})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })

	writeJSON(w, http.StatusOK, map[string]any{
		"hosts": out,
		"flowsScanned": len(flows),
	})
}

func (h *flowAPI) noteParam(agg map[paramAggKey]*minedParam, fl *store.Flow, name, source string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	k := paramAggKey{host: fl.Host, name: name, source: source}
	if p, ok := agg[k]; ok {
		p.Hits++
		if fl.ID > p.LastFlowID {
			p.LastFlowID = fl.ID
			p.SamplePath = fl.Path
		}
		return
	}
	agg[k] = &minedParam{
		Name: name, Source: source, Hits: 1,
		LastFlowID: fl.ID, SamplePath: fl.Path,
	}
}

func jsonTopKeys(b []byte) map[string]struct{} {
	if len(b) == 0 || len(b) > 512*1024 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}
