package postman

import (
	"fmt"
	"sort"
)

// Level classifies how faithfully a feature was imported.
type Level string

const (
	Converted      Level = "converted"
	Degraded       Level = "degraded"
	PreservedInert Level = "preserved-inert"
	Unsupported    Level = "unsupported"
	Blocked        Level = "blocked"
	NeedsReview    Level = "needs-review"
)

// Entry is one finding in the import report. It never contains a secret value.
type Entry struct {
	Level      Level  `json:"level"`
	Path       string `json:"path,omitempty"`
	Item       string `json:"item,omitempty"` // item uid, for jump-to in the UI
	Feature    string `json:"feature"`
	Message    string `json:"message"`
	Line       int    `json:"line,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ScriptInfo is one row of the scripts panel.
type ScriptInfo struct {
	Path       string   `json:"path"`
	Item       string   `json:"item,omitempty"`
	Listen     string   `json:"listen"`
	SourceHash string   `json:"sourceHash"`
	Lines      int      `json:"lines"`
	APIs       []string `json:"apis,omitempty"`
	Modules    []string `json:"modules,omitempty"`
	Hosts      []string `json:"hosts,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	Status     string   `json:"status"` // supported | partial | unsupported
	Quarantine bool     `json:"quarantined"`
}

// ScriptSummary is the headline script count.
type ScriptSummary struct {
	Total       int `json:"total"`
	Supported   int `json:"supported"`
	Partial     int `json:"partial"`
	Unsupported int `json:"unsupported"`
}

// Stats counts imported structure.
type Stats struct {
	Folders              int `json:"folders"`
	Requests             int `json:"requests"`
	Examples             int `json:"examples"`
	Variables            int `json:"variables"`
	SecretVariables      int `json:"secretVariables"`
	DisabledRows         int `json:"disabledRows"`
	NeedsAsset           int `json:"needsAsset"`
	EmbeddedCredentials  int `json:"embeddedCredentials"`
	EnvironmentVariables int `json:"environmentVariables"`
}

// Report is the honest account of an import: what converted cleanly, what was
// only preserved, and what cannot work. Stored in ix_import_log and shown
// before commit.
type Report struct {
	Format     string        `json:"format"`
	Entries    []Entry       `json:"entries"`
	Counts     map[Level]int `json:"counts"`
	Scripts    ScriptSummary `json:"scripts"`
	ScriptList []ScriptInfo  `json:"scriptList,omitempty"`
	Stats      Stats         `json:"stats"`
	Headline   string        `json:"headline"`
}

func (r *Report) add(e Entry) {
	r.Entries = append(r.Entries, e)
}

// finish computes counts and the headline; call once after the walk.
func (r *Report) finish() {
	r.Counts = map[Level]int{}
	for _, e := range r.Entries {
		r.Counts[e.Level]++
	}
	s := r.Scripts
	r.Headline = fmt.Sprintf("%d folders, %d requests; %d scripts: %d fully supported, %d partial, %d using unsupported APIs (all quarantined until trusted)",
		r.Stats.Folders, r.Stats.Requests, s.Total, s.Supported, s.Partial, s.Unsupported)
	sort.SliceStable(r.Entries, func(i, j int) bool { return levelRank(r.Entries[i].Level) < levelRank(r.Entries[j].Level) })
}

func levelRank(l Level) int {
	switch l {
	case Unsupported:
		return 0
	case Blocked:
		return 1
	case NeedsReview:
		return 2
	case Degraded:
		return 3
	case PreservedInert:
		return 4
	}
	return 5
}

// Has reports whether an entry with the given level and feature exists.
func (r *Report) Has(l Level, feature string) bool {
	for _, e := range r.Entries {
		if e.Level == l && e.Feature == feature {
			return true
		}
	}
	return false
}

// SkippedItem is one request the store did not import because an identical
// one already exists.
type SkippedItem struct {
	Item   string
	Path   string
	Method string
	Reason string
}

// AddSkippedDuplicates records skipped requests in the report so a skip is
// never silent: one entry per request (bounded by the store) plus a summary.
func (r *Report) AddSkippedDuplicates(items []SkippedItem) {
	if len(items) == 0 {
		return
	}
	for _, k := range items {
		r.add(Entry{Level: NeedsReview, Path: k.Path, Item: k.Item, Feature: "item-skipped-duplicate",
			Message:    "not imported: " + k.Reason,
			Suggestion: "Rename or delete the existing request first if you want both"})
	}
	r.add(Entry{Level: NeedsReview, Feature: "items-skipped",
		Message:    fmt.Sprintf("%d request(s) were not imported because identical ones already exist in this collection", len(items)),
		Suggestion: "Re-importing a file never duplicates requests; edit or delete the existing ones to replace them"})
	headline := r.Headline
	r.finish()
	if headline != "" {
		r.Headline = headline + fmt.Sprintf("; %d skipped as duplicates", len(items))
	}
}
