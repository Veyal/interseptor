package control

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Scanner issue views for agents.
//
// GET /api/scanner/issues returns every issue, which for one noisy host was
// 131 issues and 74 KB that were really 2 distinct (title, host) problems.
// With ?view= the endpoint filters (severity, host, title, flowId), groups by
// (title, host) or summarizes, and pages with offset/limit. Without ?view the
// reply is unchanged, so the UI is unaffected.

const (
	issueViewDefaultLimit = 50
	issueViewMaxLimit     = 500
	issueExampleFlows     = 3
	issueEvidenceRunes    = 500
	issueSummaryTop       = 10
)

type issueGroup struct {
	Severity string  `json:"severity"`
	Title    string  `json:"title"`
	Host     string  `json:"host"`
	Count    int     `json:"count"`
	FlowIDs  []int64 `json:"exampleFlowIds"`
	Target   string  `json:"exampleTarget"`
	Detail   string  `json:"detail,omitempty"`
	Fix      string  `json:"fix,omitempty"`
}

func issueHost(target string) string {
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	host, _, _ := strings.Cut(strings.TrimSpace(target), "/")
	return strings.ToLower(host)
}

// writeIssueView answers GET /api/scanner/issues?view=list|grouped|summary.
func writeIssueView(w http.ResponseWriter, q url.Values, issues []store.Issue) {
	view := strings.ToLower(q.Get("view"))
	if view != "list" && view != "grouped" && view != "summary" {
		httpErr(w, http.StatusBadRequest, "view must be list, grouped or summary")
		return
	}
	offset, err := sliceInt(q.Get("offset"), 0, 0, 1<<31-1)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "offset must be a non-negative integer")
		return
	}
	limit, err := sliceInt(q.Get("limit"), issueViewDefaultLimit, 1, issueViewMaxLimit)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(issueViewMaxLimit))
		return
	}
	filtered := filterIssues(issues, q)
	switch view {
	case "summary":
		groups := groupIssues(filtered)
		by := map[string]int{}
		for _, is := range filtered {
			by[strings.ToLower(is.Severity)]++
		}
		top := make([]map[string]any, 0, issueSummaryTop)
		for _, g := range groups[:min(len(groups), issueSummaryTop)] {
			top = append(top, map[string]any{"severity": g.Severity, "title": g.Title, "host": g.Host, "count": g.Count})
		}
		writeJSON(w, http.StatusOK, map[string]any{"total": len(filtered), "bySeverity": by, "groups": len(groups), "top": top})
	case "grouped":
		groups := groupIssues(filtered)
		page := pageOf(groups, offset, limit)
		writeJSON(w, http.StatusOK, pagedReply("groups", page, len(groups), offset, limit, map[string]any{"totalIssues": len(filtered)}))
	default:
		page := pageOf(filtered, offset, limit)
		for i := range page {
			page[i].Evidence = truncRunesCtl(page[i].Evidence, issueEvidenceRunes)
		}
		writeJSON(w, http.StatusOK, pagedReply("issues", page, len(filtered), offset, limit, nil))
	}
}

func filterIssues(issues []store.Issue, q url.Values) []store.Issue {
	sevs := map[string]bool{}
	for _, s := range strings.Split(q.Get("severity"), ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			sevs[s] = true
		}
	}
	host := strings.ToLower(strings.TrimSpace(q.Get("host")))
	title := strings.ToLower(strings.TrimSpace(q.Get("title")))
	flowID, _ := strconv.ParseInt(q.Get("flowId"), 10, 64)
	out := make([]store.Issue, 0, len(issues))
	for _, is := range issues {
		if len(sevs) > 0 && !sevs[strings.ToLower(is.Severity)] {
			continue
		}
		if host != "" && !strings.Contains(issueHost(is.Target), host) {
			continue
		}
		if title != "" && !strings.Contains(strings.ToLower(is.Title), title) {
			continue
		}
		if flowID > 0 && is.FlowID != flowID {
			continue
		}
		out = append(out, is)
	}
	return out
}

// groupIssues collapses issues by (title, host), most severe and most
// frequent first.
func groupIssues(issues []store.Issue) []issueGroup {
	idx := map[[2]string]int{}
	var groups []issueGroup
	for _, is := range issues {
		key := [2]string{is.Title, issueHost(is.Target)}
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, issueGroup{Severity: is.Severity, Title: is.Title, Host: key[1], Target: is.Target,
				Detail: is.Detail, Fix: is.Fix, FlowIDs: []int64{}})
		}
		g := &groups[i]
		g.Count++
		if severityRank(is.Severity) < severityRank(g.Severity) {
			g.Severity = is.Severity
		}
		if len(g.FlowIDs) < issueExampleFlows && is.FlowID != 0 {
			g.FlowIDs = append(g.FlowIDs, is.FlowID)
		}
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if ra, rb := severityRank(groups[a].Severity), severityRank(groups[b].Severity); ra != rb {
			return ra < rb
		}
		if groups[a].Count != groups[b].Count {
			return groups[a].Count > groups[b].Count
		}
		return groups[a].Title < groups[b].Title
	})
	if groups == nil {
		groups = []issueGroup{}
	}
	return groups
}

func pageOf[T any](all []T, offset, limit int) []T {
	if offset >= len(all) {
		return []T{}
	}
	return append([]T(nil), all[offset:min(len(all), offset+limit)]...)
}

func pagedReply[T any](key string, page []T, total, offset, limit int, extra map[string]any) map[string]any {
	out := map[string]any{key: page, "total": total, "offset": offset, "limit": limit}
	if next := offset + len(page); next < total {
		out["nextOffset"] = next
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func truncRunesCtl(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
