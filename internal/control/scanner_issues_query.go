package control

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Passive scanning repeats itself: one missing header on one host is one issue
// per URL, so a short capture yields hundreds of issues that are a handful of
// (check, host) pairs. This file filters, groups and pages the stored issues
// for callers that read them as text (MCP agents, scripts), without changing
// what is stored or what the UI's plain GET returns.

const (
	issueGroupExamples = 3    // example flows / targets kept per group
	issueQueryMaxLimit = 1000 // largest page a caller may ask for
)

type issueQuery struct {
	severities map[string]bool // lower-case; empty = any
	host       string          // lower-case exact host; "" = any
	title      string          // lower-case substring; "" = any
	group      bool            // fold by (title, host)
	limit      int             // 0 = no limit
	offset     int
}

// issueGroup is one (check title, host) pair and how often it fired.
type issueGroup struct {
	Title          string   `json:"title"`
	Severity       string   `json:"severity"`
	Host           string   `json:"host"`
	Count          int      `json:"count"`
	ExampleFlowIDs []int64  `json:"exampleFlowIds"`
	ExampleTargets []string `json:"exampleTargets"`
	Detail         string   `json:"detail,omitempty"`
	Fix            string   `json:"fix,omitempty"`
}

// issueQueryActive reports whether any filter, grouping or paging parameter is
// present; without one the endpoint keeps its original {issues:[...]} shape.
func issueQueryActive(q url.Values) bool {
	for _, k := range []string{"severity", "host", "title", "group", "limit", "offset"} {
		if q.Has(k) {
			return true
		}
	}
	return false
}

func parseIssueQuery(q url.Values) (issueQuery, error) {
	opts := issueQuery{
		host:  strings.ToLower(strings.TrimSpace(q.Get("host"))),
		title: strings.ToLower(strings.TrimSpace(q.Get("title"))),
	}
	for _, s := range strings.Split(q.Get("severity"), ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			if opts.severities == nil {
				opts.severities = map[string]bool{}
			}
			opts.severities[s] = true
		}
	}
	switch g := strings.ToLower(strings.TrimSpace(q.Get("group"))); g {
	case "", "0", "false", "none":
	case "host", "1", "true":
		opts.group = true
	default:
		return opts, fmt.Errorf("group must be host or none, got %q", g)
	}
	var err error
	if opts.limit, err = issueQueryInt(q, "limit"); err != nil {
		return opts, err
	}
	if opts.limit > issueQueryMaxLimit {
		opts.limit = issueQueryMaxLimit
	}
	if opts.offset, err = issueQueryInt(q, "offset"); err != nil {
		return opts, err
	}
	return opts, nil
}

func issueQueryInt(q url.Values, key string) (int, error) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return n, nil
}

// issueHost extracts the host from an issue target. Built-in checks write
// "METHOD host/path"; a custom check may write a URL or a bare host.
func issueHost(target string) string {
	t := strings.TrimSpace(target)
	if i := strings.IndexByte(t, ' '); i >= 0 && !strings.Contains(t[:i], "/") {
		t = strings.TrimSpace(t[i+1:])
	}
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil && u.Host != "" {
			return strings.ToLower(u.Hostname())
		}
	}
	if i := strings.IndexAny(t, "/?# "); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(t)
}

// issueSeverityRank orders severities most severe first; unknown ones last.
func issueSeverityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	case "info":
		return 4
	}
	return 5
}

func (o issueQuery) keep(is store.Issue) bool {
	if len(o.severities) > 0 && !o.severities[strings.ToLower(is.Severity)] {
		return false
	}
	if o.host != "" && issueHost(is.Target) != o.host {
		return false
	}
	if o.title != "" && !strings.Contains(strings.ToLower(is.Title), o.title) {
		return false
	}
	return true
}

// groupIssuesByHost folds issues by (title, host). A group takes its most
// severe member's severity, and lists the first few flows and targets.
func groupIssuesByHost(issues []store.Issue) []issueGroup {
	index := map[[2]string]int{}
	var groups []issueGroup
	for _, is := range issues {
		host := issueHost(is.Target)
		key := [2]string{is.Title, host}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, issueGroup{Title: is.Title, Severity: is.Severity, Host: host, Detail: is.Detail, Fix: is.Fix,
				ExampleFlowIDs: []int64{}, ExampleTargets: []string{}})
		}
		g := &groups[i]
		g.Count++
		if issueSeverityRank(is.Severity) < issueSeverityRank(g.Severity) {
			g.Severity = is.Severity
		}
		if is.FlowID > 0 && len(g.ExampleFlowIDs) < issueGroupExamples {
			g.ExampleFlowIDs = append(g.ExampleFlowIDs, is.FlowID)
		}
		if is.Target != "" && len(g.ExampleTargets) < issueGroupExamples {
			g.ExampleTargets = append(g.ExampleTargets, is.Target)
		}
	}
	sort.SliceStable(groups, func(a, b int) bool {
		ga, gb := groups[a], groups[b]
		if ra, rb := issueSeverityRank(ga.Severity), issueSeverityRank(gb.Severity); ra != rb {
			return ra < rb
		}
		if ga.Count != gb.Count {
			return ga.Count > gb.Count
		}
		if ga.Title != gb.Title {
			return ga.Title < gb.Title
		}
		return ga.Host < gb.Host
	})
	return groups
}

// queryIssues applies a parsed query. The response always says how many
// issues matched and, when a page stops short, the offset of the next one.
func queryIssues(all []store.Issue, o issueQuery) map[string]any {
	matched := make([]store.Issue, 0, len(all))
	for _, is := range all {
		if o.keep(is) {
			matched = append(matched, is)
		}
	}
	out := map[string]any{"total": len(matched)}
	var rows int
	if o.group {
		groups := groupIssuesByHost(matched)
		rows = len(groups)
		groups = pageSlice(groups, o.offset, o.limit)
		out["groups"] = groups
		out["groupCount"] = rows
	} else {
		rows = len(matched)
		out["issues"] = pageSlice(matched, o.offset, o.limit)
	}
	if o.limit > 0 && o.offset+o.limit < rows {
		out["nextOffset"] = o.offset + o.limit
	}
	return out
}

func pageSlice[T any](rows []T, offset, limit int) []T {
	if offset >= len(rows) {
		return []T{}
	}
	rows = rows[offset:]
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows
}

// issueRunSummary is the compact result of a scan: counts only, so a caller
// that wants the rows asks for them (grouped) instead of receiving all of them.
func issueRunSummary(all []store.Issue) map[string]any {
	bySeverity := map[string]int{}
	for _, is := range all {
		bySeverity[is.Severity]++
	}
	return map[string]any{
		"total":      len(all),
		"groupCount": len(groupIssuesByHost(all)),
		"bySeverity": bySeverity,
	}
}
