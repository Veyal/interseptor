package autopwn

import (
	"context"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/verify"
)

// planSystem frames the planning agent: read-only recon over captured in-scope
// history producing a prioritized, structured attack plan. It is told to emit a
// strict JSON plan object as its final answer.
const planSystem = `You are the reconnaissance and planning stage of an authorized, autonomous penetration test.
Read the captured in-scope HTTP history using the READ-ONLY tools provided (list_flows, get_flow,
analyze_flow, list_scope, host_stats, list_tags, diff_flows). Cluster requests by host, endpoint,
and parameter. Produce a PRIORITIZED attack plan: for each interesting endpoint/param, decide which
vulnerability classes to test and which active tool to drive.

Do NOT send any attack traffic — you are planning only. When done, respond with ONLY a JSON object:
{"summary":"...","steps":[
  {"target":"https://host/path","flowId":123,"point":"param q",
   "vulnClasses":["sqli-boolean","xss-reflected"],
   "tool":"active_scan","args":{"flowId":123,"arm":true},"priority":9}
]}
Order steps by priority (higher first). Prefer active_scan for injection classes, start_intruder for
enumeration, and authz_run for access-control.`

// planTools is the read-only recon toolset the planning agent may use.
var planTools = []aiagent.ToolSpec{
	{Name: "list_flows", Description: "List captured flows (filter by tag/host/scope).", Schema: objSchema},
	{Name: "get_flow", Description: "Fetch a captured flow's request/response by id.", Schema: objSchema},
	{Name: "analyze_flow", Description: "Summarize a flow's notable request/response features.", Schema: objSchema},
	{Name: "list_scope", Description: "List the current target-scope rules.", Schema: objSchema},
	{Name: "host_stats", Description: "Per-host traffic statistics over captured history.", Schema: objSchema},
	{Name: "list_tags", Description: "List flow tags in use.", Schema: objSchema},
	{Name: "diff_flows", Description: "Diff two captured flows.", Schema: objSchema},
}

// buildPlanTask renders the planning task, weaving in a deterministic recon
// digest (so planning does not depend on the model calling tools) and the
// operator's target hint.
func buildPlanTask(hint, digest string) string {
	var b strings.Builder
	if strings.TrimSpace(digest) != "" {
		b.WriteString(digest)
		b.WriteString("\n")
	}
	b.WriteString("Using the recon context above, produce a prioritized attack plan ")
	b.WriteString("(targets x vuln classes x tools). Reply with ONLY the strict JSON plan object.")
	if strings.TrimSpace(hint) != "" {
		fmt.Fprintf(&b, "\nOperator focus hint: %s", strings.TrimSpace(hint))
	}
	return b.String()
}

// defaultCollectCandidates is the production candidate collector: it reads the
// scanner's confirmed issues (via list_issues over the tool bus) and maps each to
// a Candidate. Agent-surfaced observations are folded in by control's richer
// collector later; the default keeps a clean, deterministic active-scan path.
//
// NOTE: the default collector is intentionally conservative — it produces the
// class/severity/target skeleton from issues. The Diff spec (Gate-1 requests) is
// left minimal; control's follow-up wiring enriches candidates with concrete
// baseline/payload requests derived from the seeding flow. Tests inject their own
// CollectCandidates to script the full candidate set.
func defaultCollectCandidates(ctx context.Context, e *Engine, plan Plan) []Candidate {
	if e.d.Tools == nil {
		return nil
	}
	out, err := e.executor().Exec(ctx, aiagent.ToolCall{Name: "list_issues", Args: map[string]any{}})
	if err != nil {
		return nil
	}
	issues := parseIssues(out)
	cands := make([]Candidate, 0, len(issues))
	for _, is := range issues {
		cands = append(cands, Candidate{
			VulnClass: classOfIssue(is.Title),
			Severity:  verify.ParseSeverity(is.Severity),
			Target:    is.Target,
			Point:     is.Title,
			Summary:   is.Detail,
			StepFlow:  is.FlowID,
		})
	}
	return cands
}
