package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func trackerFixture() store.Finding {
	f := sarifFixture()[0]
	f.Blocks = append(f.Blocks, store.FindingBlock{
		Type: "flow", FlowID: 7, Role: "action", Source: "captured_flow",
		Method: "GET", Host: "example.com", Path: "/invoices/8", Status: 200,
		ReqRaw: "GET /invoices/8 HTTP/1.1\r\nHost: example.com\r\nCookie: sid=0123456789abcdef\r\n\r\n",
		ResRaw: "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"invoice\":\"8\"}",
	})
	return f
}

// Every adapter renders its narrative through the report's own finding
// renderer, so the proof annotations and the provenance labels that decide what
// counts as evidence travel with the ticket.
func TestTrackerNarrativesKeepReportProvenance(t *testing.T) {
	f := trackerFixture()
	bodies := map[string]string{
		"hackerone": HackerOne(f, nil).VulnerabilityInformation,
		"bugcrowd":  Bugcrowd(f, nil).Description,
		"jira":      Jira(f, nil).Fields.Description,
	}
	for name, body := range bodies {
		for _, want := range []string{
			"role=action",
			"source=browser_screenshot",
			"source=flow_preview (generated HTTP preview; not browser proof)",
			"Reads another tenant's invoice.",
			"**Impact:** Cross-tenant read of invoice records.",
			"GET /invoices/8 HTTP/1.1",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: narrative missing %q", name, want)
			}
		}
		// The classification facts are restated so the ticket stands alone.
		for _, want := range []string{"- **Severity:** High", "- **CWE:** CWE-639", "calculated v4.0 score"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: header missing %q", name, want)
			}
		}
		if !strings.Contains(body, "https://example.com/invoices/9") {
			t.Errorf("%s: second affected target lost", name)
		}
	}
}

func TestTrackerNarrativesHonourMasking(t *testing.T) {
	masked := MaskSensitiveHeaders([]store.Finding{trackerFixture()})[0]
	bodies := []string{
		HackerOne(masked, nil).VulnerabilityInformation,
		Bugcrowd(masked, nil).Description,
		Jira(masked, nil).Fields.Description,
	}
	for _, body := range bodies {
		if strings.Contains(body, "0123456789abcdef") {
			t.Errorf("a masked finding leaked its cookie into a tracker payload")
		}
		if !strings.Contains(body, "[redacted") {
			t.Error("masked tracker payload has no placeholder")
		}
	}
}

func TestHackerOneMapping(t *testing.T) {
	got := HackerOne(trackerFixture(), nil)
	if got.Title != "IDOR on /invoices/{id}" {
		t.Errorf("title = %q", got.Title)
	}
	if got.SeverityRating != "high" {
		t.Errorf("severity_rating = %q", got.SeverityRating)
	}
	if got.Impact != "Cross-tenant read of invoice records." {
		t.Errorf("impact = %q", got.Impact)
	}
	// A HackerOne weakness id and scope id cannot be derived, so they must not
	// appear at all — a guessed id files the report against the wrong thing.
	raw, _ := json.Marshal(got)
	for _, absent := range []string{"weakness_id", "structured_scope_id", "cvss"} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("invented field %q: %s", absent, raw)
		}
	}
	// The CWE is still stated, in the narrative.
	if !strings.Contains(got.VulnerabilityInformation, "CWE-639") {
		t.Error("the CWE should still be stated in the narrative")
	}
	for sev, want := range map[string]string{
		"Critical": "critical", "High": "high", "Medium": "medium",
		"Low": "low", "Info": "none", "": "",
	} {
		f := store.Finding{ID: 1, Title: "x", Severity: sev}
		if got := HackerOne(f, nil).SeverityRating; got != want {
			t.Errorf("severity %q -> %q, want %q", sev, got, want)
		}
	}
}

func TestBugcrowdMapping(t *testing.T) {
	got := Bugcrowd(trackerFixture(), nil)
	if got.Severity != 2 {
		t.Errorf("High should be P2, got %d", got.Severity)
	}
	if got.BugURL != "https://example.com/invoices/8" {
		t.Errorf("bug_url = %q", got.BugURL)
	}
	if !strings.Contains(got.ExtraInfo, "Remediation: Check tenant ownership") ||
		!strings.Contains(got.ExtraInfo, "Retest: Repeat the request") {
		t.Errorf("extra_info = %q", got.ExtraInfo)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "vrt_id") {
		t.Errorf("invented a VRT id: %s", raw)
	}
	for sev, want := range map[string]int{"Critical": 1, "High": 2, "Medium": 3, "Low": 4, "Info": 5, "": 0} {
		f := store.Finding{ID: 1, Title: "x", Severity: sev}
		if got := Bugcrowd(f, nil).Severity; got != want {
			t.Errorf("severity %q -> %d, want %d", sev, got, want)
		}
	}
	// A target that is not a URL must not be published as one.
	noURL := Bugcrowd(store.Finding{ID: 2, Title: "x", Target: "the staging load balancer"}, nil)
	if noURL.BugURL != "" {
		t.Errorf("bug_url = %q for a non-URL target", noURL.BugURL)
	}
}

func TestJiraMapping(t *testing.T) {
	got := Jira(trackerFixture(), nil)
	if got.Fields.Summary != "IDOR on /invoices/{id}" {
		t.Errorf("summary = %q", got.Fields.Summary)
	}
	if got.Fields.IssueType.Name != "Bug" {
		t.Errorf("issuetype = %q", got.Fields.IssueType.Name)
	}
	if got.Fields.Priority == nil || got.Fields.Priority.Name != "High" {
		t.Errorf("priority = %+v", got.Fields.Priority)
	}
	want := map[string]bool{"interseptor": true, "api": true, "cwe-639": true}
	for _, l := range got.Fields.Labels {
		delete(want, l)
		if strings.ContainsAny(l, " \t") {
			t.Errorf("label %q contains whitespace; Jira will reject it", l)
		}
	}
	if len(want) > 0 {
		t.Errorf("labels missing %v, got %v", want, got.Fields.Labels)
	}
	// The project key belongs to the caller's Jira, not to us.
	raw, _ := json.Marshal(got)
	for _, absent := range []string{`"project"`, `"reporter"`, `"assignee"`} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("invented field %s: %s", absent, raw)
		}
	}
}

func TestJiraLabelsFoldUnsafeCharacters(t *testing.T) {
	f := store.Finding{ID: 1, Title: "x", Tags: []string{"out of scope", "api/v2", ""}}
	for _, l := range Jira(f, nil).Fields.Labels {
		if strings.ContainsAny(l, " /") || l == "" {
			t.Errorf("unsafe label %q", l)
		}
	}
	if !contains(Jira(f, nil).Fields.Labels, "out-of-scope") {
		t.Errorf("labels = %v", Jira(f, nil).Fields.Labels)
	}
}

func TestTrackerAdaptersAreDeterministic(t *testing.T) {
	f := trackerFixture()
	for _, pair := range [][2]any{
		{HackerOne(f, nil), HackerOne(f, nil)},
		{Bugcrowd(f, nil), Bugcrowd(f, nil)},
		{Jira(f, nil), Jira(f, nil)},
	} {
		a, _ := json.Marshal(pair[0])
		b, _ := json.Marshal(pair[1])
		if string(a) != string(b) {
			t.Fatalf("adapter output is not deterministic:\n%s\n%s", a, b)
		}
	}
}

func TestTrackerAdaptersHandleABareFinding(t *testing.T) {
	f := store.Finding{ID: 9, Title: "Bare claim"}
	h := HackerOne(f, nil)
	if h.Title != "Bare claim" || h.Impact != "" || h.SeverityRating != "" {
		t.Errorf("hackerone invented content: %+v", h)
	}
	if h.VulnerabilityInformation == "" {
		t.Error("the narrative should still report the readiness gaps")
	}
	b := Bugcrowd(f, nil)
	if b.BugURL != "" || b.ExtraInfo != "" || b.Severity != 0 {
		t.Errorf("bugcrowd invented content: %+v", b)
	}
	j := Jira(f, nil)
	if j.Fields.Priority != nil {
		t.Errorf("jira invented a priority: %+v", j.Fields.Priority)
	}
}

func TestReportableFindingsExcludesDismissedAndOrdersBySeverity(t *testing.T) {
	in := []store.Finding{
		{ID: 1, Severity: "Low", Title: "b"},
		{ID: 2, Severity: "Critical", Title: "a", Status: "false_positive"},
		{ID: 3, Severity: "High", Title: "c"},
		{ID: 4, Severity: "High", Title: "a"},
	}
	out := ReportableFindings(in)
	var ids []int64
	for _, f := range out {
		ids = append(ids, f.ID)
	}
	if len(ids) != 3 || ids[0] != 4 || ids[1] != 3 || ids[2] != 1 {
		t.Fatalf("order/exclusion wrong: %v", ids)
	}
}

func TestFindingMarkdownDropsOnlyTheHeading(t *testing.T) {
	f := trackerFixture()
	md := FindingMarkdown(f, nil)
	if strings.HasPrefix(md, "###") {
		t.Fatalf("the report's numbered heading should be dropped:\n%s", md[:80])
	}
	if !strings.Contains(md, "**Status:** verified") {
		t.Errorf("metadata lost:\n%s", md[:300])
	}
	if strings.Contains(md, "IDOR on /invoices/{id}\n- **Status") {
		t.Error("the title line was kept as prose")
	}
}
