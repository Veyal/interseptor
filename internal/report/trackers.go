package report

import (
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Bug-bounty platform and issue-tracker payloads. Each is the body a caller
// posts to that system, minus the identifiers only the caller holds. See
// adapters.go for the two rules every adapter here obeys.

// HackerOneReport is the body of a HackerOne report submission.
//
// Deliberately absent:
//   - weakness_id — HackerOne's own numeric id for a CWE. Interseptor records
//     the CWE itself ("CWE-639"), and guessing the matching HackerOne id would
//     file the report under the wrong weakness. The CWE is stated in
//     vulnerability_information instead.
//   - structured_scope_id — the programme's asset id. Interseptor knows the URL,
//     not the programme's identifier for it.
//   - a CVSS vector field. HackerOne scores in CVSS v3.1; Interseptor's cvss is
//     v4.0, which is not the same string. Both the v4.0 vector and, when the
//     finding records one in cvssV3, the v3.1 vector and score are stated in
//     the narrative, each labelled with its version.
type HackerOneReport struct {
	Title                    string `json:"title"`
	VulnerabilityInformation string `json:"vulnerability_information"`
	Impact                   string `json:"impact,omitempty"`
	SeverityRating           string `json:"severity_rating,omitempty"`
}

// HackerOne shapes one finding as a HackerOne submission. titles may be nil.
func HackerOne(f store.Finding, titles map[int64]string) HackerOneReport {
	return HackerOneReport{
		Title:                    adapterTitle(f, 250),
		VulnerabilityInformation: trackerNarrative(f, titles),
		Impact:                   trackerImpact(f),
		SeverityRating:           hackerOneRating(f.Severity),
	}
}

func hackerOneRating(severity string) string {
	switch severity {
	case "Critical":
		return "critical"
	case "High":
		return "high"
	case "Medium":
		return "medium"
	case "Low":
		return "low"
	case "Info":
		return "none"
	}
	return ""
}

// BugcrowdSubmission is the body of a Bugcrowd submission.
//
// Deliberately absent:
//   - vrt_id — Bugcrowd's Vulnerability Rating Taxonomy identifier. It is
//     Bugcrowd's own taxonomy, not derivable from a CWE, and a wrong VRT id
//     changes how the submission is triaged and paid.
//   - researcher / submitted_at — the platform's, not the tool's.
type BugcrowdSubmission struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BugURL      string `json:"bug_url,omitempty"`
	ExtraInfo   string `json:"extra_info,omitempty"`
	// Severity is Bugcrowd's P1–P5 priority: 1 is P1 (critical), 5 is P5
	// (informational). 0 means the finding carried no severity to map.
	Severity int `json:"severity,omitempty"`
}

// Bugcrowd shapes one finding as a Bugcrowd submission. titles may be nil.
func Bugcrowd(f store.Finding, titles map[int64]string) BugcrowdSubmission {
	var bugURL string
	for _, target := range findingTargetURLs(f) {
		if absoluteURI(target) {
			bugURL = target
			break
		}
	}
	return BugcrowdSubmission{
		Title:       adapterTitle(f, 250),
		Description: trackerNarrative(f, titles),
		BugURL:      bugURL,
		ExtraInfo:   trackerExtraInfo(f),
		Severity:    bugcrowdPriority(f.Severity),
	}
}

func bugcrowdPriority(severity string) int {
	switch severity {
	case "Critical":
		return 1
	case "High":
		return 2
	case "Medium":
		return 3
	case "Low":
		return 4
	case "Info":
		return 5
	}
	return 0
}

// JiraIssue is the body of a Jira issue-create request.
//
// Deliberately absent:
//   - fields.project — the Jira project key is the caller's. Add
//     {"project":{"key":"ABC"}} to fields before posting.
//   - reporter, assignee, components, security level, custom fields — all
//     instance configuration Interseptor has no view of.
//
// Two assumptions, both overridable by the caller: issuetype "Bug", and the
// default Jira priority scheme (Highest…Lowest). An instance with its own
// scheme should drop or rewrite fields.priority.
//
// Description carries the engagement report's narrative converted to Jira wiki
// markup, which is what Jira's /rest/api/2/issue endpoint renders. Jira Cloud's
// v3 endpoint expects Atlassian Document Format, which Interseptor does not
// generate, so post this body to v2.
type JiraIssue struct {
	Fields JiraFields `json:"fields"`
}

type JiraFields struct {
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	IssueType   JiraNamed  `json:"issuetype"`
	Priority    *JiraNamed `json:"priority,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
}

type JiraNamed struct {
	Name string `json:"name"`
}

// Jira shapes one finding as a Jira issue-create body. titles may be nil.
func Jira(f store.Finding, titles map[int64]string) JiraIssue {
	fields := JiraFields{
		Summary:     adapterTitle(f, 250),
		Description: markdownToJiraWiki(trackerNarrative(f, titles)),
		IssueType:   JiraNamed{Name: "Bug"},
		Labels:      jiraLabels(f),
	}
	if p := jiraPriority(f.Severity); p != "" {
		fields.Priority = &JiraNamed{Name: p}
	}
	return JiraIssue{Fields: fields}
}

func jiraPriority(severity string) string {
	switch severity {
	case "Critical":
		return "Highest"
	case "High":
		return "High"
	case "Medium":
		return "Medium"
	case "Low":
		return "Low"
	case "Info":
		return "Lowest"
	}
	return ""
}

var jiraLabelUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// jiraLabels are "interseptor", the finding's own tags, and its CWE. A Jira
// label cannot contain whitespace, so anything else is folded to a hyphen.
func jiraLabels(f store.Finding) []string {
	labels := []string{"interseptor"}
	seen := map[string]bool{"interseptor": true}
	add := func(s string) {
		s = strings.Trim(jiraLabelUnsafe.ReplaceAllString(strings.TrimSpace(s), "-"), "-")
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		labels = append(labels, s)
	}
	for _, tag := range f.Tags {
		add(tag)
	}
	if m := cweID.FindStringSubmatch(f.Cwe); m != nil {
		add("cwe-" + m[1])
	}
	return labels
}

// trackerNarrative is the finding as the engagement report writes it. The
// report states severity in its section heading, which a ticket does not
// carry, so only that is added on top; status, confidence, CWE, environment,
// CVSS and the affected targets are already in the finding's own metadata and
// are not repeated.
func trackerNarrative(f store.Finding, titles map[int64]string) string {
	var b strings.Builder
	if v := strings.TrimSpace(sanitizeLine(f.Severity)); v != "" {
		b.WriteString("- **Severity:** " + v + "\n")
	}
	md := FindingMarkdown(f, titles)
	if b.Len() > 0 && strings.HasPrefix(md, "- **") {
		// Join the metadata list so it reads as one block.
		b.WriteString(md)
	} else {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(md)
	}
	return strings.TrimRight(b.String(), "\n")
}

func trackerImpact(f store.Finding) string {
	for _, s := range []string{f.Impact, f.Summary} {
		if t := strings.TrimSpace(sanitizeLine(s)); t != "" {
			return t
		}
	}
	return ""
}

// trackerExtraInfo is the remediation and retest guidance, for platforms that
// keep it apart from the narrative.
func trackerExtraInfo(f store.Finding) string {
	var parts []string
	if fix := strings.TrimSpace(sanitizeLine(f.Fix)); fix != "" {
		parts = append(parts, "Remediation: "+fix)
	}
	if retest := strings.TrimSpace(sanitizeLine(f.Retest)); retest != "" {
		parts = append(parts, "Retest: "+retest)
	}
	return strings.Join(parts, "\n")
}
