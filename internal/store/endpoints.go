package store

import (
	"sort"
	"strconv"
	"strings"
)

// Endpoint search scopes for the attack-surface map.
const (
	EndpointSearchPath    = "path"    // host, path, method (default)
	EndpointSearchHeaders = "headers" // req/res headers JSON
	EndpointSearchBody    = "body"    // req/res body files (bounded scan)
	EndpointSearchAll     = "all"     // path + headers + body
)

// Endpoint is a unique (host, method, path) surface aggregated from flows — the
// building block of the endpoint map. Repeated hits collapse into one row.
type Endpoint struct {
	Host       string `json:"host"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Scheme     string `json:"scheme"`
	LastStatus int    `json:"lastStatus"` // status of the most recent hit
	Statuses   []int  `json:"statuses"`   // every distinct status seen, sorted
	Hits       int    `json:"hits"`
	LastFlowID int64  `json:"lastFlowId"` // most recent flow, for click-through
}

// EndpointFilter narrows which flows are aggregated into endpoints.
type EndpointFilter struct {
	Host         string
	Search       string
	SearchScope  string // path, headers, body, all — see EndpointSearch* constants
	ExcludeFlags int64
	Tag          string // only endpoints with at least one flow carrying this tag
}

// parseStatusCSV turns GROUP_CONCAT(DISTINCT status) ("200,404") into a sorted
// de-duplicated []int.
func parseStatusCSV(s string) []int {
	if s == "" {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, p := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
