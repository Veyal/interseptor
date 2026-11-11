package control

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/Veyal/interseptor/internal/store"
)

// inScopeScanPages bounds one interactive in-scope scan: a History page reads at
// most this many store batches (25 x up to 2000 rows) before it answers. The
// scope predicate runs in Go, so the bound is what keeps one request from reading
// a whole multi-million-row project. A var so tests can lower it.
var inScopeScanPages = 25

// inScopeScan says how an in-scope scan ended.
type inScopeScan struct {
	// More: in-scope rows may exist past the returned ones (the caller got the
	// rows it asked for and history goes on, or the scan stopped early).
	More bool
	// Stopped: the page budget ran out before `want` rows were found. CursorID and
	// CursorVal name the last row the scan read; a caller resumes from there
	// (FlowFilter.CursorID/CursorVal) instead of from the last matched row, which
	// can be tens of thousands of rows newer — or missing when nothing matched.
	Stopped   bool
	CursorID  int64
	CursorVal string
}

// scanInScope pages through captured traffic until want in-scope rows are found,
// history is exhausted, or maxPages batches were read (maxPages <= 0: no bound).
// Post-filtering a single SQL page misses in-scope flows when recent traffic is
// mostly out-of-scope noise.
func (h *Hub) scanInScope(f store.FlowFilter, want, maxPages int, query func(store.FlowFilter) ([]*store.Flow, error)) ([]*store.Flow, inScopeScan, error) {
	if want < 1 {
		want = 1
	}
	batch := want * 10
	if batch < 300 {
		batch = 300
	}
	if batch > 2000 || maxPages <= 0 {
		batch = 2000 // an unbounded scan reads in the largest batches: fewer queries
	}
	ff := f
	var matched []*store.Flow
	for page := 1; ; page++ {
		ff.Limit = batch + 1
		rows, err := query(ff)
		if err != nil {
			return nil, inScopeScan{}, err
		}
		pageTrunc := len(rows) > batch
		if pageTrunc {
			rows = rows[:batch]
		}
		for i, fl := range rows {
			if !h.sc.InScope(fl) {
				continue
			}
			matched = append(matched, fl)
			if len(matched) >= want {
				return matched, inScopeScan{More: pageTrunc || i < len(rows)-1}, nil
			}
		}
		if !pageTrunc {
			return matched, inScopeScan{}, nil
		}
		last := rows[len(rows)-1]
		ff.CursorID = last.ID
		ff.CursorVal = store.FlowSortValue(last, ff.SortKey)
		ff.BeforeID = 0
		if maxPages > 0 && page >= maxPages {
			return matched, inScopeScan{More: true, Stopped: true, CursorID: ff.CursorID, CursorVal: ff.CursorVal}, nil
		}
	}
}

// queryInScopeFlows is one bounded, resumable in-scope scan over slim list rows
// (a History page). When it reports Stopped the caller must surface the cursor.
func (h *Hub) queryInScopeFlows(f store.FlowFilter, want int) ([]*store.Flow, inScopeScan, error) {
	return h.scanInScope(f, want, inScopeScanPages, h.st.QueryFlowsListFilter)
}

// queryInScopeFlowsFull is like queryInScopeFlows but loads full flow rows (headers
// for param mining).
func (h *Hub) queryInScopeFlowsFull(f store.FlowFilter, want int) ([]*store.Flow, inScopeScan, error) {
	return h.scanInScope(f, want, inScopeScanPages, h.st.QueryFlowsFilter)
}

// queryAllInScopeFlowsFull scans to the end of history for up to want in-scope
// full rows. For explicit exports, where a silent stop after the scan budget
// would hand back an incomplete file.
func (h *Hub) queryAllInScopeFlowsFull(f store.FlowFilter, want int) ([]*store.Flow, inScopeScan, error) {
	return h.scanInScope(f, want, 0, h.st.QueryFlowsFilter)
}

// inScopeMemo remembers the last hasInScopeTraffic answer so the full-history
// scan is paid once per scope, not on every /api/readiness call: either the id
// of a flow that was in scope, or the highest id below which nothing was.
type inScopeMemo struct {
	mu        sync.Mutex
	rules     string // fingerprint of the scope rules the memo was computed under
	bulkGen   uint64 // endpointsCache.bulkGeneration the memo was computed under
	matchID   int64  // a flow that was in scope (0: none known)
	scannedTo int64  // every flow with id <= scannedTo was out of scope
}

// hasInScopeTraffic reports whether any captured proxy flow is in scope. It reads
// to the end of history: a bounded scan answered "no" whenever the in-scope
// flows were older than the newest few thousand out-of-scope rows. Later calls
// under the same scope rules only look at flows captured since.
func (h *Hub) hasInScopeTraffic() bool {
	fp := ""
	if rules, err := h.st.ListScopeRules(); err == nil {
		if b, err := json.Marshal(rules); err == nil {
			fp = string(b)
		}
	}
	m := &h.inScopeMemo
	m.mu.Lock()
	defer m.mu.Unlock()
	// An import, merge, delete or prune undo can add or remove flows below
	// scannedTo (a restored prune keeps its original ids), so it resets the memo
	// like a scope change does; live capture only appends.
	if gen := h.epsCache.bulkGeneration(); fp == "" || m.rules != fp || m.bulkGen != gen {
		m.rules, m.bulkGen, m.matchID, m.scannedTo = fp, gen, 0, 0
	}
	if m.matchID > 0 {
		if fl, err := h.st.GetFlow(m.matchID); err == nil && fl.Flags&(store.FlagRepeater|store.FlagIntruder) == 0 && h.sc.InScope(fl) {
			return true
		}
		m.matchID, m.scannedTo = 0, 0 // the remembered flow is gone: rescan
	}
	f := store.FlowFilter{
		SortKey:      "id",
		SortDir:      -1,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder,
	}
	if m.scannedTo > 0 {
		f.ScanBoundID = m.scannedTo + 1 // only flows newer than the last scan
	}
	newest := int64(0)
	matched, _, err := h.scanInScope(f, 1, 0, func(ff store.FlowFilter) ([]*store.Flow, error) {
		rows, err := h.st.QueryFlowsListFilter(ff)
		if err == nil && newest == 0 && len(rows) > 0 {
			newest = rows[0].ID
		}
		return rows, err
	})
	if err != nil {
		return false
	}
	if len(matched) > 0 {
		m.matchID = matched[0].ID
		return true
	}
	if fp != "" && newest > m.scannedTo {
		m.scannedTo = newest
	}
	return false
}

func (h *flowAPI) trafficInScope(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"inScope": h.hasInScopeTraffic()})
}
