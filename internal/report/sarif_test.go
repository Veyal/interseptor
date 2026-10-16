package report

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const testToolVersion = "2.13.1"

func sarifFixture() []store.Finding {
	score := 8.1
	return []store.Finding{
		{
			ID: 1, Severity: "High", Status: "verified", Title: "IDOR on /invoices/{id}",
			Summary: "A reader reaches another tenant's invoice.",
			Impact:  "Cross-tenant read of invoice records.",
			Why:     "The object reference is not checked against the session tenant.",
			Fix:     "Check tenant ownership before returning an invoice.",
			Retest:  "Repeat the request as the other tenant; expect 403.",
			Cwe:     "CWE-639", Cvss: "CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N",
			CvssScore: &score, CvssRating: "High", Confidence: "firm",
			Environment: "production", Tags: []string{"api"},
			Target: "https://example.com/invoices/8",
			Targets: store.FindingTargets{
				{URL: "https://example.com/invoices/8", Methods: []string{"GET"}, Relation: "affected"},
				{URL: "https://example.com/invoices/9", Methods: []string{"GET"}, Relation: "affected"},
			},
			ProofReview: store.FindingProofReview{Execution: "demonstrated"},
			Blocks: []store.FindingBlock{
				{Type: "flow", FlowID: 7, Role: "action", Proof: "Reads another tenant's invoice.", Source: "captured_flow"},
				{Type: "image", Hash: "abc123", Role: "result", Proof: "Browser shows the other tenant's invoice.", Source: "browser_screenshot"},
				{Type: "image", Hash: "def456", Role: "control", Proof: "Generated preview of the baseline response.", Source: "flow_preview", SourceFlowID: 6},
			},
		},
		{ID: 2, Severity: "Low", Status: "open", Title: "Missing cache header on /health", Target: "the staging load balancer"},
		{ID: 3, Severity: "Critical", Status: "false_positive", Title: "Dismissed claim", Target: "https://example.com/x"},
	}
}

// decodeSARIF renders and decodes, so every assertion below reads the JSON a
// consumer would actually receive.
func decodeSARIF(t *testing.T, findings []store.Finding) map[string]any {
	t.Helper()
	raw, err := json.Marshal(SARIF(findings, testToolVersion))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestSARIFSatisfiesSchema210Structure checks the structural constraints the
// SARIF 2.1.0 schema imposes: required properties on sarifLog, run, tool,
// toolComponent, reportingDescriptor, result, message, location and
// artifactLocation, plus the closed enums for result.level and result.kind.
// It is a hand-written check against the published specification rather than
// JSON-Schema validation: no schema document is vendored and no JSON-Schema
// module is in go.mod, so the constraints are spelled out here instead.
func TestSARIFSatisfiesSchema210Structure(t *testing.T) {
	log := decodeSARIF(t, sarifFixture())

	requireString(t, log, "version", "2.1.0")
	if _, ok := log["$schema"].(string); !ok {
		t.Error("$schema should name the 2.1.0 schema")
	}
	runs, ok := log["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs must be a one-element array, got %T", log["runs"])
	}
	run := runs[0].(map[string]any)

	tool, ok := run["tool"].(map[string]any)
	if !ok {
		t.Fatal("run.tool is required")
	}
	driver, ok := tool["driver"].(map[string]any)
	if !ok {
		t.Fatal("tool.driver is required")
	}
	if name, _ := driver["name"].(string); name == "" {
		t.Error("toolComponent.name is required")
	}

	ruleIDs := map[string]int{}
	rules, _ := driver["rules"].([]any)
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("rules[%d] is not an object", i)
		}
		id, _ := rule["id"].(string)
		if id == "" {
			t.Errorf("rules[%d]: reportingDescriptor.id is required", i)
		}
		if _, dup := ruleIDs[id]; dup {
			t.Errorf("rules[%d]: duplicate rule id %q", i, id)
		}
		ruleIDs[id] = i
		for _, key := range []string{"shortDescription", "fullDescription", "help"} {
			if v, present := rule[key]; present {
				requireMultiformatMessage(t, fmt.Sprintf("rules[%d].%s", i, key), v)
			}
		}
		if cfg, present := rule["defaultConfiguration"].(map[string]any); present {
			if lvl, has := cfg["level"]; has {
				requireLevel(t, fmt.Sprintf("rules[%d].defaultConfiguration.level", i), lvl)
			}
		}
	}

	results, ok := run["results"].([]any)
	if !ok {
		t.Fatal("run.results must be an array")
	}
	for i, raw := range results {
		res, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("results[%d] is not an object", i)
		}
		where := fmt.Sprintf("results[%d]", i)
		requireMessage(t, where+".message", res["message"])
		if lvl, has := res["level"]; has {
			requireLevel(t, where+".level", lvl)
		}
		if kind, has := res["kind"]; has {
			requireKind(t, where+".kind", kind)
		}
		id, _ := res["ruleId"].(string)
		if id == "" {
			t.Errorf("%s: ruleId missing", where)
		}
		idx, hasIdx := res["ruleIndex"].(float64)
		if !hasIdx {
			t.Errorf("%s: ruleIndex missing", where)
		} else if want, ok := ruleIDs[id]; !ok || int(idx) != want {
			t.Errorf("%s: ruleIndex %v does not point at rule %q", where, idx, id)
		}
		for j, locRaw := range asArray(res["locations"]) {
			loc, ok := locRaw.(map[string]any)
			if !ok {
				t.Fatalf("%s.locations[%d] is not an object", where, j)
			}
			if phys, has := loc["physicalLocation"].(map[string]any); has {
				art, has := phys["artifactLocation"].(map[string]any)
				if !has {
					t.Errorf("%s.locations[%d]: physicalLocation without artifactLocation", where, j)
					continue
				}
				uri, _ := art["uri"].(string)
				if uri == "" {
					t.Errorf("%s.locations[%d]: artifactLocation needs a uri or an index", where, j)
				}
			} else if _, has := loc["message"]; !has {
				t.Errorf("%s.locations[%d]: a location with no physicalLocation must describe itself", where, j)
			}
		}
		for k, v := range asMap(res["partialFingerprints"]) {
			if _, ok := v.(string); !ok {
				t.Errorf("%s.partialFingerprints[%q] must be a string, got %T", where, k, v)
			}
		}
	}
}

func requireString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	if got, _ := m[key].(string); got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func requireMessage(t *testing.T, where string, v any) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s is required", where)
		return
	}
	text, _ := m["text"].(string)
	id, _ := m["id"].(string)
	if text == "" && id == "" {
		t.Errorf("%s needs text or id", where)
	}
}

func requireMultiformatMessage(t *testing.T, where string, v any) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s is not an object", where)
		return
	}
	if text, _ := m["text"].(string); text == "" {
		t.Errorf("%s.text is required", where)
	}
}

var sarifLevels = map[string]bool{"none": true, "note": true, "warning": true, "error": true}
var sarifKinds = map[string]bool{
	"notApplicable": true, "pass": true, "fail": true,
	"review": true, "open": true, "informational": true,
}

func requireLevel(t *testing.T, where string, v any) {
	t.Helper()
	s, _ := v.(string)
	if !sarifLevels[s] {
		t.Errorf("%s = %q is not a SARIF level", where, s)
	}
}

func requireKind(t *testing.T, where string, v any) {
	t.Helper()
	s, _ := v.(string)
	if !sarifKinds[s] {
		t.Errorf("%s = %q is not a SARIF kind", where, s)
	}
}

func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func TestSARIFExcludesFalsePositivesAndMapsSeverity(t *testing.T) {
	log := decodeSARIF(t, sarifFixture())
	results := asArray(asMap(asArray(log["runs"])[0])["results"])
	if len(results) != 2 {
		t.Fatalf("want 2 results (the false positive is excluded), got %d", len(results))
	}
	byRule := map[string]map[string]any{}
	for _, r := range results {
		res := asMap(r)
		byRule[res["ruleId"].(string)] = res
	}
	if lvl := byRule["interseptor/finding/1"]["level"]; lvl != "error" {
		t.Errorf("High should map to error, got %v", lvl)
	}
	if lvl := byRule["interseptor/finding/2"]["level"]; lvl != "note" {
		t.Errorf("Low should map to note, got %v", lvl)
	}
	// SARIF has three usable levels and Interseptor has five severities, so the
	// exact severity has to survive in properties.
	if sev := asMap(byRule["interseptor/finding/1"]["properties"])["severity"]; sev != "High" {
		t.Errorf("exact severity lost: %v", sev)
	}
	for _, res := range byRule {
		if _, has := res["kind"]; has {
			t.Error("kind must be omitted: Interseptor's workflow status is not a SARIF check outcome")
		}
	}
}

func TestSARIFKeepsEvidenceProvenance(t *testing.T) {
	log := decodeSARIF(t, sarifFixture())
	results := asArray(asMap(asArray(log["runs"])[0])["results"])
	var first map[string]any
	for _, r := range results {
		if asMap(r)["ruleId"] == "interseptor/finding/1" {
			first = asMap(r)
		}
	}
	if first == nil {
		t.Fatal("finding 1 missing")
	}
	props := asMap(first["properties"])
	evidence := asArray(props["evidence"])
	if len(evidence) != 3 {
		t.Fatalf("want 3 evidence entries, got %d", len(evidence))
	}
	sources := map[string]string{}
	for _, e := range evidence {
		entry := asMap(e)
		role, _ := entry["role"].(string)
		src, _ := entry["source"].(string)
		sources[role] = src
		if proof, _ := entry["proof"].(string); proof == "" {
			t.Errorf("evidence %v lost its proof statement", entry)
		}
	}
	if sources["result"] != "browser_screenshot" || sources["control"] != "flow_preview" {
		t.Fatalf("provenance grading flattened: %v", sources)
	}
	if props["proofExecution"] != "demonstrated" {
		t.Errorf("impact verification lost: %v", props["proofExecution"])
	}
}

func TestSARIFLocationsAndCWE(t *testing.T) {
	log := decodeSARIF(t, sarifFixture())
	run := asMap(asArray(log["runs"])[0])
	results := asArray(run["results"])
	for _, r := range results {
		res := asMap(r)
		locs := asArray(res["locations"])
		switch res["ruleId"] {
		case "interseptor/finding/1":
			if len(locs) != 2 {
				t.Fatalf("both affected targets should be locations, got %d", len(locs))
			}
			uri := asMap(asMap(asMap(locs[0])["physicalLocation"])["artifactLocation"])["uri"]
			if uri != "https://example.com/invoices/8" {
				t.Errorf("target uri = %v", uri)
			}
		case "interseptor/finding/2":
			// "the staging load balancer" is not a URI; claiming it is one in
			// artifactLocation.uri would be a lie about the field's type.
			if len(locs) != 1 {
				t.Fatalf("want one described location, got %d", len(locs))
			}
			if _, has := asMap(locs[0])["physicalLocation"]; has {
				t.Error("a non-URI target must not become an artifactLocation")
			}
			if txt := asMap(asMap(locs[0])["message"])["text"]; txt != "the staging load balancer" {
				t.Errorf("location description = %v", txt)
			}
		}
	}
	rules := asArray(asMap(asMap(run["tool"])["driver"])["rules"])
	var withCWE map[string]any
	for _, raw := range rules {
		if asMap(raw)["id"] == "interseptor/finding/1" {
			withCWE = asMap(raw)
		}
	}
	props := asMap(withCWE["properties"])
	if props["cwe"] != "CWE-639" {
		t.Errorf("cwe = %v", props["cwe"])
	}
	var tags []string
	for _, tag := range asArray(props["tags"]) {
		tags = append(tags, tag.(string))
	}
	if !contains(tags, "external/cwe/cwe-639") {
		t.Errorf("cwe tag missing: %v", tags)
	}
	// The published score is the one Interseptor calculates from the vector, not
	// whatever a caller happened to store alongside it.
	calc := sarifFixture()[0]
	calc.EnrichCompleteness()
	want := strconv.FormatFloat(*calc.CvssScore, 'f', -1, 64)
	if props["security-severity"] != want {
		t.Errorf("security-severity = %v, want the calculated CVSS score %s", props["security-severity"], want)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestSARIFOmitsWhatItDoesNotKnow(t *testing.T) {
	log := decodeSARIF(t, []store.Finding{{ID: 9, Title: "Bare claim"}})
	run := asMap(asArray(log["runs"])[0])
	rules := asArray(asMap(asMap(run["tool"])["driver"])["rules"])
	rule := asMap(rules[0])
	// No remediation was written, so there is no advice to give.
	if _, has := rule["help"]; has {
		t.Error("help should be omitted when no remediation was written")
	}
	props := asMap(rule["properties"])
	for _, absent := range []string{"cwe", "security-severity"} {
		if _, has := props[absent]; has {
			t.Errorf("properties.%s invented a value", absent)
		}
	}
	res := asMap(asArray(run["results"])[0])
	if _, has := res["locations"]; has {
		t.Error("a finding with no target must not carry a location")
	}
}

func TestSARIFEmptyProjectIsStillValid(t *testing.T) {
	log := decodeSARIF(t, nil)
	run := asMap(asArray(log["runs"])[0])
	if _, ok := run["tool"]; !ok {
		t.Fatal("tool is required even with no results")
	}
	results := asArray(run["results"])
	if len(results) != 0 {
		t.Fatalf("want no results, got %d", len(results))
	}
	raw, _ := json.Marshal(SARIF(nil, testToolVersion))
	if !strings.Contains(string(raw), `"results":[]`) {
		t.Errorf("results must serialise as an empty array, not null: %s", raw)
	}
}

func TestSARIFIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(SARIF(sarifFixture(), testToolVersion))
	b, _ := json.Marshal(SARIF(sarifFixture(), testToolVersion))
	if string(a) != string(b) {
		t.Fatal("SARIF output is not deterministic")
	}
}

func TestSARIFNamesTheToolAndVersion(t *testing.T) {
	log := decodeSARIF(t, sarifFixture())
	driver := asMap(asMap(asMap(asArray(log["runs"])[0])["tool"])["driver"])
	if driver["name"] != "Interseptor" {
		t.Errorf("driver.name = %v", driver["name"])
	}
	if driver["version"] != testToolVersion {
		t.Errorf("driver.version = %v, want %q", driver["version"], testToolVersion)
	}
	if uri, _ := driver["informationUri"].(string); !strings.HasPrefix(uri, "https://") {
		t.Errorf("driver.informationUri = %v", driver["informationUri"])
	}
	// An unknown version must not be invented as a fake one.
	raw, _ := json.Marshal(SARIF(nil, ""))
	if strings.Contains(string(raw), `"version"`) && strings.Contains(string(raw), `"version":""`) {
		t.Errorf("empty version should be omitted: %s", raw)
	}
}
