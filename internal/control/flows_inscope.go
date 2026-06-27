package control

import (
	"net/http"

	"github.com/Veyal/interceptor/internal/store"
)

// queryInScopeFlows pages through captured traffic until want in-scope rows are
// found (or history is exhausted). Post-filtering a single SQL page misses
// in-scope flows when recent traffic is mostly out-of-scope noise.
func (h *Hub) queryInScopeFlows(f store.FlowFilter, want int) ([]*store.Flow, bool, error) {
	if want < 1 {
		want = 1
	}
	batch := want * 10
	if batch < 300 {
		batch = 300
	}
	if batch > 2000 {
		batch = 2000
	}
	ff := f
	ff.Limit = 0
	var matched []*store.Flow
	truncated := false
	for page := 0; page < 25 && len(matched) < want; page++ {
		ff.Limit = batch + 1
		rows, err := h.st.QueryFlowsListFilter(ff)
		if err != nil {
			return nil, false, err
		}
		pageTrunc := len(rows) > batch
		if pageTrunc {
			rows = rows[:batch]
			truncated = true
		}
		for _, fl := range rows {
			if h.sc.InScope(fl) {
				matched = append(matched, fl)
				if len(matched) >= want {
					break
				}
			}
		}
		if len(matched) >= want || !pageTrunc {
			break
		}
		last := rows[len(rows)-1]
		ff.CursorID = last.ID
		ff.CursorVal = store.FlowSortValue(last, ff.SortKey)
		ff.BeforeID = 0
	}
	return matched, truncated, nil
}

// queryInScopeFlowsFull is like queryInScopeFlows but loads full flow rows (headers
// for param mining).
func (h *Hub) queryInScopeFlowsFull(f store.FlowFilter, want int) ([]*store.Flow, bool, error) {
	if want < 1 {
		want = 1
	}
	batch := want * 10
	if batch < 300 {
		batch = 300
	}
	if batch > 2000 {
		batch = 2000
	}
	ff := f
	ff.Limit = 0
	var matched []*store.Flow
	truncated := false
	for page := 0; page < 25 && len(matched) < want; page++ {
		ff.Limit = batch + 1
		rows, err := h.st.QueryFlowsFilter(ff)
		if err != nil {
			return nil, false, err
		}
		pageTrunc := len(rows) > batch
		if pageTrunc {
			rows = rows[:batch]
			truncated = true
		}
		for _, fl := range rows {
			if h.sc.InScope(fl) {
				matched = append(matched, fl)
				if len(matched) >= want {
					break
				}
			}
		}
		if len(matched) >= want || !pageTrunc {
			break
		}
		last := rows[len(rows)-1]
		ff.CursorID = last.ID
		ff.CursorVal = store.FlowSortValue(last, ff.SortKey)
		ff.BeforeID = 0
	}
	return matched, truncated, nil
}

func (h *Hub) hasInScopeTraffic() bool {
	matched, _, err := h.queryInScopeFlows(store.FlowFilter{
		SortKey:      "id",
		SortDir:      -1,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan,
	}, 1)
	return err == nil && len(matched) > 0
}

func (h *Hub) trafficInScope(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"inScope": h.hasInScopeTraffic()})
}
