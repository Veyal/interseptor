package autopwn

import (
	"encoding/json"
	"strings"

	"github.com/Veyal/interseptor/internal/verify"
)

// Plan is the structured attack plan the planning-phase agent produces: a
// prioritized list of steps, each pairing a target endpoint/param with the vuln
// classes to test and the tools to drive. It is persisted as pentest_run.plan.
type Plan struct {
	Summary string     `json:"summary,omitempty"`
	Steps   []PlanStep `json:"steps"`
}

// PlanStep is one unit of the plan: probe one target with a set of vuln classes
// using a set of tools. FlowID (optional) ties the step to a captured flow so the
// executor can pass it to active_scan / start_intruder.
type PlanStep struct {
	Target      string         `json:"target"`                // url/endpoint
	FlowID      int64          `json:"flowId,omitempty"`      // captured flow to target, if known
	Point       string         `json:"point,omitempty"`       // injection point (param/header)
	VulnClasses []string       `json:"vulnClasses,omitempty"` // e.g. ["sqli-boolean","xss-reflected"]
	Tools       []string       `json:"tools,omitempty"`       // e.g. ["active_scan","start_intruder"]
	Tool        string         `json:"tool,omitempty"`        // primary tool (executor picks this)
	Args        map[string]any `json:"args,omitempty"`        // explicit args for the primary tool call
	Priority    int            `json:"priority,omitempty"`    // higher = run first
}

// parsePlan extracts a Plan from the planning agent's final answer. It accepts
// either a bare JSON Plan object or JSON embedded in prose (last {...} block). A
// plan that cannot be parsed yields an empty plan (the run still completes with
// zero candidates rather than erroring on a chatty model).
func parsePlan(finalText string) Plan {
	// Try the whole text first (agent may reply with pure JSON).
	if p, ok := tryPlanJSON(finalText); ok {
		return p
	}
	if obj := lastJSONObject(finalText); obj != "" {
		if p, ok := tryPlanJSON(obj); ok {
			return p
		}
	}
	return Plan{Summary: strings.TrimSpace(finalText)}
}

func tryPlanJSON(s string) (Plan, bool) {
	var p Plan
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &p); err == nil && len(p.Steps) > 0 {
		return p, true
	}
	return Plan{}, false
}

// Candidate is one thing the execution phase surfaces for verification. It pairs
// the class/severity/target with everything Gate 1 (Diff) and Gate 3 (OOB) need.
// Candidates come from active-scan issues and agent-surfaced observations; only a
// candidate that passes every applicable gate becomes a filed Finding.
type Candidate struct {
	VulnClass string          // e.g. "sqli-boolean","ssrf-blind"
	Severity  verify.Severity // drives Gate 4
	Target    string
	Point     string
	Blind     bool
	Summary   string
	Diff      verify.DiffSpec // Gate 1 requests
	// OOBProbe, when Blind, is the request to inject the minted token URL into.
	// The engine mints the token, rewrites the placeholder to the callback URL,
	// and builds the verify.OOBSpec at verification time.
	OOBProbe       verify.Request
	OOBPlaceholder string // substring in OOBProbe URL/body replaced by the callback URL
	// StepFlow is the captured flow that seeded this candidate (for context).
	StepFlow int64
}

// toVerifyCandidate builds the verify.Candidate for the gates. When Blind and an
// OOB spec was minted, oob is attached; otherwise it is nil.
func (c Candidate) toVerifyCandidate(oob *verify.OOBSpec) verify.Candidate {
	return verify.Candidate{
		VulnClass: c.VulnClass,
		Severity:  c.Severity,
		Target:    c.Target,
		Point:     c.Point,
		Blind:     c.Blind,
		Diff:      c.Diff,
		OOB:       oob,
		Summary:   c.Summary,
	}
}
