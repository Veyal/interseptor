package control

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Veyal/interceptor/internal/harx"
	"github.com/Veyal/interceptor/internal/store"
)

// exportHAR streams the (optionally in-scope) history as a HAR 1.2 document.
func (h *Hub) exportHAR(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{
		Limit:        atoiOr(q.Get("limit"), 10000),
		ExcludeFlags: store.FlagIntruder, // attack traffic is noise in an export
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q.Get("inScope") == "1" {
		kept := flows[:0]
		for _, f := range flows {
			if h.sc.InScope(f) {
				kept = append(kept, f)
			}
		}
		flows = kept
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor.har"`)
	w.Write(harx.Build(flows, h.bodyBytes))
}

// importHAR ingests a HAR document, recording each entry as a flow (FlagImported).
func (h *Hub) importHAR(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := harx.Parse(data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "not a valid HAR: "+err.Error())
		return
	}
	n := 0
	for _, e := range entries {
		u, perr := url.Parse(e.URL)
		if perr != nil || !u.IsAbs() || u.Host == "" {
			continue
		}
		ts := e.TS
		if ts.IsZero() {
			ts = time.Now()
		}
		fl := &store.Flow{
			TS: ts, Method: e.Method, Scheme: u.Scheme, Host: u.Hostname(),
			Port: atoiOr(u.Port(), defaultPortFor(u.Scheme)), Path: u.RequestURI(),
			HTTPVersion: orVal(e.HTTPVersion, "HTTP/1.1"), Status: e.Status,
			ReqHeaders: e.ReqHeaders, ResHeaders: e.ResHeaders, Mime: e.Mime,
			DurationMs: e.DurationMs, Flags: store.FlagImported,
		}
		fl.ReqBodyHash, fl.ReqLen = h.storeBody(e.ReqBody)
		fl.ResBodyHash, fl.ResLen = h.storeBody(e.ResBody)
		if _, err := h.st.InsertFlow(fl); err == nil {
			n++
		}
	}
	if n > 0 {
		h.epsCache.invalidate() // imported flows add endpoints — drop the stale Map/endpoints aggregate
		h.broadcast(map[string]any{"type": "flow.new"}) // nudge the UI to refresh history
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (h *Hub) storeBody(b []byte) (string, int64) {
	if len(b) == 0 {
		return "", 0
	}
	bw, err := h.st.NewBodyWriter()
	if err != nil {
		return "", 0
	}
	if _, err := bw.Write(b); err != nil {
		bw.Abort()
		return "", 0
	}
	hash, n, err := bw.Finalize()
	if err != nil {
		return "", 0
	}
	return hash, n
}

func defaultPortFor(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}
