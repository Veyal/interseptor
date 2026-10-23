package control

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

const maxHARImportBytes = 64 << 20

// exportHAR streams the (optionally in-scope) history as a HAR 1.2 document.
func (h *projectAPI) exportHAR(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{
		Limit:        atoiOr(q.Get("limit"), 10000),
		ExcludeFlags: store.FlagIntruder, // attack traffic is noise in an export
	})
	if err != nil {
		httpInternalErr(w, err)
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
	w.Header().Set("Content-Disposition", `attachment; filename="interseptor.har"`)
	w.Write(harx.Build(flows, h.bodyBytes))
}

// importHAR ingests a HAR document, recording each entry as a flow (FlagImported).
func (h *projectAPI) importHAR(w http.ResponseWriter, r *http.Request) {
	data, ok := readLimitedBody(w, r, maxHARImportBytes)
	if !ok {
		return
	}
	entries, err := harx.Parse(data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "not a valid HAR: "+err.Error())
		return
	}
	n := 0
	for _, e := range entries {
		fl, ok := harEntryFlow(e)
		if !ok {
			continue
		}
		if err := h.insertImportedFlow(fl, e.ReqBody, e.ResBody); err != nil {
			httpInternalErr(w, err)
			return
		}
		n++
	}
	if n > 0 {
		h.epsCache.invalidate()                         // imported flows add endpoints — drop the stale Map/endpoints aggregate
		h.broadcast(map[string]any{"type": "flow.new"}) // nudge the UI to refresh history
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

// harEntryFlow maps one HAR entry to an imported flow. Like the Burp import it
// accepts only absolute http/https URLs and defaults an empty method to GET;
// ok is false for an entry that must be skipped.
func harEntryFlow(e harx.Entry) (*store.Flow, bool) {
	u, err := url.Parse(e.URL)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	ts := e.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	return &store.Flow{
		TS: ts, Method: orVal(strings.TrimSpace(e.Method), "GET"), Scheme: u.Scheme, Host: u.Hostname(),
		Port: atoiOr(u.Port(), defaultPortFor(u.Scheme)), Path: u.RequestURI(),
		HTTPVersion: orVal(e.HTTPVersion, "HTTP/1.1"), Status: e.Status,
		ReqHeaders: e.ReqHeaders, ResHeaders: e.ResHeaders, Mime: e.Mime,
		DurationMs: e.DurationMs, Flags: store.FlagImported,
	}, true
}

func (h *projectAPI) insertImportedFlow(flow *store.Flow, reqBody, resBody []byte) error {
	reqWriter, reqHash, reqLen, err := stageImportedBody(h.st, reqBody)
	if err != nil {
		return fmt.Errorf("store imported request body: %w", err)
	}
	flow.ReqBodyHash, flow.ReqLen = reqHash, reqLen
	_, resHash, resLen, err := stageImportedBody(h.st, resBody)
	if err != nil {
		if reqWriter != nil {
			reqWriter.Abort()
		}
		return fmt.Errorf("store imported response body: %w", err)
	}
	flow.ResBodyHash, flow.ResLen = resHash, resLen
	if _, err := h.st.InsertFlow(flow); err != nil {
		return fmt.Errorf("insert imported flow: %w", err)
	}
	return nil
}

// insertImportedFlowOnce is insertImportedFlow that skips a flow whose content
// signature (store.FlowImportSignature: request identity, timestamp and status,
// the fields that survive a HAR round trip) is already in seen, so re-importing the same export adds
// nothing. It reports whether the flow was inserted and records new ones.
func (h *projectAPI) insertImportedFlowOnce(flow *store.Flow, reqBody, resBody []byte, seen map[string]int64) (bool, error) {
	reqWriter, reqHash, reqLen, err := stageImportedBody(h.st, reqBody)
	if err != nil {
		return false, fmt.Errorf("store imported request body: %w", err)
	}
	flow.ReqBodyHash, flow.ReqLen = reqHash, reqLen
	resWriter, resHash, resLen, err := stageImportedBody(h.st, resBody)
	if err != nil {
		if reqWriter != nil {
			reqWriter.Abort()
		}
		return false, fmt.Errorf("store imported response body: %w", err)
	}
	flow.ResBodyHash, flow.ResLen = resHash, resLen
	sig := store.FlowImportSignature(*flow)
	if _, dup := seen[sig]; dup {
		// The bodies are content-addressed and already referenced by the
		// existing flow; Abort only releases their pending-publication claim.
		for _, bw := range []*store.BodyWriter{reqWriter, resWriter} {
			if bw != nil {
				bw.Abort()
			}
		}
		return false, nil
	}
	id, err := h.st.InsertFlow(flow)
	if err != nil {
		return false, fmt.Errorf("insert imported flow: %w", err)
	}
	seen[sig] = id
	return true, nil
}

func (h *projectAPI) storeBody(b []byte) (string, int64) {
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
