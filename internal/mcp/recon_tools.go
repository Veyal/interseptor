package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// intruderState backs the intruder_state tool: it always returns the run
// summary, and windows/filters the (possibly huge) result set through the
// disk-backed /api/intruder/results endpoint instead of returning every row.
func (s *Server) intruderState(a map[string]any) (string, error) {
	stateRaw, err := s.apiGet("/api/intruder/state")
	if err != nil {
		return "", err
	}
	var st map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stateRaw), &st); err != nil {
		return boundJSON(stateRaw, 200), nil // unexpected shape: fall back to the old cap
	}
	offset := argInt(a, "offset", 0)
	limit := argInt(a, "limit", 200)
	onlyAnom := argBool(a, "onlyAnomalies", false)
	onlyFlag := argBool(a, "onlyFlagged", false)

	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	// When filtering, scan a wide window so anomalies past the page boundary are
	// not missed; otherwise fetch exactly the page asked for.
	scan := limit
	if onlyAnom || onlyFlag {
		scan = 5000
	}
	q.Set("limit", strconv.Itoa(scan))
	resRaw, err := s.apiGet("/api/intruder/results?" + q.Encode())
	if err != nil {
		return "", err
	}
	var rr struct {
		Results []json.RawMessage `json:"results"`
		Total   int               `json:"total"`
		Offset  int               `json:"offset"`
	}
	if err := json.Unmarshal([]byte(resRaw), &rr); err != nil {
		return "", fmt.Errorf("decode intruder results: %w", err)
	}
	kept := rr.Results[:0:0]
	consumed := 0
	for _, r := range rr.Results {
		consumed++
		if onlyAnom && !resultBool(r, "anomaly") {
			continue
		}
		if onlyFlag && !resultBool(r, "flagged") && !resultBool(r, "matched") {
			continue
		}
		kept = append(kept, r)
		if len(kept) >= limit {
			break
		}
	}
	if kept == nil {
		kept = []json.RawMessage{}
	}
	delete(st, "results")
	out := map[string]any{"results": kept, "total": rr.Total, "offset": rr.Offset, "shown": len(kept)}
	// nextOffset is the first row not yet examined. A filtered call scans one
	// window and may stop early at limit, so it needs the pointer too: without
	// it the reply looked complete while later anomalies were never read.
	if next := rr.Offset + consumed; next < rr.Total {
		out["nextOffset"] = next
	}
	for k, v := range st {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func resultBool(raw json.RawMessage, field string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	var b bool
	_ = json.Unmarshal(m[field], &b)
	return b
}

func (s *Server) registerReconTools() {
	s.add("list_endpoints",
		"The deduplicated attack-surface map: one row per (method, host, path) with the statuses seen, hit count and the latest flow id — the fastest way to see what a target exposes. Forced-browse 403/404-only noise is hidden by default.",
		obj(map[string]any{
			"host":        p("string", "host substring"),
			"search":      p("string", "match path (or widen with searchScope)"),
			"searchScope": pe("default path", "path", "headers", "body", "all"),
			"tag":         p("string", "only endpoints with a flow carrying this tag"),
			"showNoise":   p("boolean", "include endpoints that only ever returned 403/404 (default false)"),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			for _, k := range []string{"host", "search", "searchScope", "tag"} {
				if v := argStr(a, k); v != "" {
					q.Set(k, v)
				}
			}
			if argBool(a, "showNoise", false) {
				q.Set("hideNoise", "0")
			}
			return s.apiGet("/api/endpoints?" + q.Encode())
		})

	s.add("list_params",
		"Mined request parameters per host (name, where it appears — query/form/json — hit count and a sample path with the latest flow id): the injection-point inventory for fuzzing.",
		obj(map[string]any{
			"host":    p("string", "host substring"),
			"inScope": p("boolean", "only in-scope flows"),
			"limit":   pr("max flows scanned (default 400)", 1, 2000),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			if v := argStr(a, "host"); v != "" {
				q.Set("host", v)
			}
			if argBool(a, "inScope", false) {
				q.Set("inScope", "1")
			}
			if v := argInt(a, "limit", 0); v > 0 {
				q.Set("limit", strconv.Itoa(v))
			}
			return s.apiGet("/api/params?" + q.Encode())
		})
}
