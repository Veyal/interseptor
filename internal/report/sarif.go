package report

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// SARIF 2.1.0 export. The shape follows the OASIS specification
// (https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html); the
// mapping decisions, and the fields deliberately left out, are documented on
// each type and in docs/findings-and-reporting.md.

const (
	sarifSchema  = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/main/sarif-2.1/schema/sarif-schema-2.1.0.json"
	sarifVersion = "2.1.0"
	sarifToolURI = "https://github.com/Veyal/interseptor"
	sarifRulePre = "interseptor/finding/"
)

type SARIFLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []SARIFRun `json:"runs"`
}

type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

type SARIFDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []SARIFRule `json:"rules"`
}

// SARIFRule is a reportingDescriptor. Interseptor has no rule catalogue — a
// finding is a reviewed claim, not a detector hit — so each finding becomes its
// own rule, keyed by its stable project id.
type SARIFRule struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name,omitempty"`
	ShortDescription     *SARIFMultiMessage  `json:"shortDescription,omitempty"`
	FullDescription      *SARIFMultiMessage  `json:"fullDescription,omitempty"`
	Help                 *SARIFMultiMessage  `json:"help,omitempty"`
	DefaultConfiguration *SARIFConfiguration `json:"defaultConfiguration,omitempty"`
	Properties           map[string]any      `json:"properties,omitempty"`
}

type SARIFConfiguration struct {
	Level string `json:"level,omitempty"`
}

type SARIFMultiMessage struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown,omitempty"`
}

type SARIFMessage struct {
	Text string `json:"text"`
}

type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	RuleIndex int             `json:"ruleIndex"`
	Level     string          `json:"level"`
	Message   SARIFMessage    `json:"message"`
	Locations []SARIFLocation `json:"locations,omitempty"`
	// PartialFingerprints lets a consumer track the same finding across runs.
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

type SARIFLocation struct {
	PhysicalLocation *SARIFPhysicalLocation `json:"physicalLocation,omitempty"`
	Message          *SARIFMessage          `json:"message,omitempty"`
}

type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifactLocation `json:"artifactLocation"`
}

type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

// SARIF renders findings as a SARIF 2.1.0 log with one run. toolVersion is the
// running Interseptor version; an empty string omits driver.version rather than
// publishing a made-up one.
//
// Mapping decisions, and the ones deliberately not made:
//
//   - severity → result.level. SARIF has three usable levels against
//     Interseptor's five severities (Critical/High → error, Medium → warning,
//     Low/Info → note), so the exact severity, its CVSS vector and the
//     calculated score are carried in properties as well. Nothing is lost.
//   - status is NOT mapped to result.kind. SARIF's kind describes the outcome of
//     a check (pass/fail/review/…); Interseptor's status describes where a
//     reviewed finding sits in an engagement workflow. They are different
//     questions, so kind is omitted and status appears in properties.
//   - CWE travels as properties.cwe plus the widely consumed
//     "external/cwe/cwe-NNN" tag. A full CWE taxonomy component would need
//     per-taxon GUIDs that Interseptor does not hold.
//   - A target that is not an absolute URI is written as a location message, not
//     as artifactLocation.uri: a load balancer or a device path is not a URI and
//     saying otherwise would mislead every consumer that resolves that field.
//   - Evidence appears as properties.evidence with each artifact's role, kind,
//     source classification and proof statement, so the provenance grading that
//     decides what counts as proof survives the export. Raw captured bytes do
//     not: a SARIF log is an index of findings, not an evidence bundle.
func SARIF(findings []store.Finding, toolVersion string) SARIFLog {
	active := ReportableFindings(findings)
	titles := findingTitles(findings)
	rules := make([]SARIFRule, 0, len(active))
	results := make([]SARIFResult, 0, len(active))

	for i, f := range active {
		f.EnrichCompleteness()
		id := sarifRulePre + strconv.FormatInt(f.ID, 10)
		level := sarifLevel(f.Severity)

		rule := SARIFRule{
			ID:                   id,
			ShortDescription:     &SARIFMultiMessage{Text: adapterTitle(f, 200)},
			DefaultConfiguration: &SARIFConfiguration{Level: level},
			Properties:           sarifRuleProperties(f),
		}
		// fullDescription describes the problem: the narrative sentence as text,
		// and the report's own Markdown (evidence roles, proof statements,
		// provenance labels) as markdown. help describes the remedy, and exists
		// only when a remedy was actually written.
		if md := FindingMarkdown(f, titles); md != "" || sarifWhy(f) != "" {
			text := sarifWhy(f)
			if text == "" {
				text = adapterTitle(f, 200)
			}
			rule.FullDescription = &SARIFMultiMessage{Text: text, Markdown: md}
		}
		if help := sarifHelp(f); help != nil {
			rule.Help = help
		}
		rules = append(rules, rule)

		results = append(results, SARIFResult{
			RuleID:              id,
			RuleIndex:           i,
			Level:               level,
			Message:             SARIFMessage{Text: sarifMessage(f)},
			Locations:           sarifLocations(f),
			PartialFingerprints: map[string]string{"interseptorFindingId": strconv.FormatInt(f.ID, 10)},
			Properties:          sarifResultProperties(f),
		})
	}

	return SARIFLog{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs: []SARIFRun{{
			Tool: SARIFTool{Driver: SARIFDriver{
				Name:           "Interseptor",
				Version:        strings.TrimSpace(toolVersion),
				InformationURI: sarifToolURI,
				Rules:          rules,
			}},
			Results: results,
		}},
	}
}

func sarifLevel(severity string) string {
	switch severity {
	case "Critical", "High":
		return "error"
	case "Medium":
		return "warning"
	default: // Low, Info, and anything unrecognised
		return "note"
	}
}

func sarifMessage(f store.Finding) string {
	for _, s := range []string{f.Summary, f.Impact, f.Title} {
		if t := strings.TrimSpace(sanitizeLine(s)); t != "" {
			return t
		}
	}
	return fmt.Sprintf("Finding #%d", f.ID)
}

func sarifWhy(f store.Finding) string {
	for _, s := range []string{f.Why, f.Impact} {
		if t := strings.TrimSpace(sanitizeLine(s)); t != "" {
			return t
		}
	}
	return ""
}

// sarifHelp is the remediation guidance, or nil when the finding carries none.
// An empty "help" filled with the title would read as advice and say nothing.
func sarifHelp(f store.Finding) *SARIFMultiMessage {
	var parts []string
	if fix := strings.TrimSpace(sanitizeLine(f.Fix)); fix != "" {
		parts = append(parts, fix)
	}
	if retest := strings.TrimSpace(sanitizeLine(f.Retest)); retest != "" {
		parts = append(parts, "Retest: "+retest)
	}
	if len(parts) == 0 {
		return nil
	}
	return &SARIFMultiMessage{Text: strings.Join(parts, " ")}
}

var cweID = regexp.MustCompile(`(?i)\bCWE[-_ ]?(\d+)\b`)

func sarifRuleProperties(f store.Finding) map[string]any {
	props := map[string]any{}
	if f.Severity != "" {
		props["severity"] = f.Severity
	}
	var tags []string
	if f.Cwe != "" {
		props["cwe"] = f.Cwe
		if m := cweID.FindStringSubmatch(f.Cwe); m != nil {
			tags = append(tags, "external/cwe/cwe-"+m[1])
		}
	}
	tags = append(tags, f.Tags...)
	if len(tags) > 0 {
		props["tags"] = tags
	}
	if f.Cvss != "" {
		props["cvss"] = f.Cvss
	}
	// security-severity is the de-facto convention for a numeric score; only a
	// score Interseptor actually calculated is published.
	if f.CvssScore != nil {
		props["security-severity"] = strconv.FormatFloat(*f.CvssScore, 'f', -1, 64)
		props["cvssScore"] = *f.CvssScore
	}
	if f.CvssRating != "" {
		props["cvssRating"] = f.CvssRating
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func sarifResultProperties(f store.Finding) map[string]any {
	props := map[string]any{}
	if f.Status != "" {
		props["status"] = f.Status
	}
	if f.Severity != "" {
		props["severity"] = f.Severity
	}
	if f.Confidence != "" {
		props["confidence"] = f.Confidence
	}
	if f.Environment != "" {
		props["environment"] = f.Environment
	}
	if f.ProofReview.Execution != "" {
		props["proofExecution"] = f.ProofReview.Execution
	}
	if f.Readiness != nil {
		props["readinessStage"] = f.Readiness.Stage
		if len(f.Readiness.Gaps) > 0 {
			props["readinessGaps"] = f.Readiness.Gaps
		}
	}
	if f.Fix != "" {
		props["remediation"] = sanitizeLine(f.Fix)
	}
	if f.Retest != "" {
		props["retest"] = sanitizeLine(f.Retest)
	}
	if ev := findingEvidence(f); len(ev) > 0 {
		props["evidence"] = ev
	}
	if withdrawn := withdrawnClaimIDs(f); len(withdrawn) > 0 {
		props["withdrawnClaims"] = withdrawn
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func sarifLocations(f store.Finding) []SARIFLocation {
	var out []SARIFLocation
	for _, target := range findingTargetURLs(f) {
		if absoluteURI(target) {
			out = append(out, SARIFLocation{PhysicalLocation: &SARIFPhysicalLocation{
				ArtifactLocation: SARIFArtifactLocation{URI: target},
			}})
			continue
		}
		out = append(out, SARIFLocation{Message: &SARIFMessage{Text: sanitizeLine(target)}})
	}
	return out
}
