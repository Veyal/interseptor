package autopwn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/verify"
)

// agentVerifierSystem is the Gate-2 system prompt. It frames the model as an
// adversary whose job is to DISPROVE the candidate: the default answer is
// "refuted"/"uncertain" and only unambiguous, reproduced evidence earns "real".
const agentVerifierSystem = `You are an adversarial vulnerability verifier inside an authorized penetration test.
A first-pass detector has flagged a CANDIDATE vulnerability. Your ONLY job is to TRY TO DISPROVE it.

Assume the candidate is a FALSE POSITIVE until the evidence is unambiguous. Consider innocent
explanations: response caching, a WAF echoing the payload, a coincidental error page, reflected
input that is safely encoded, timing noise, or a differential that a benign input also triggers.
You may use the read/replay tools (send_request, get_flow, analyze_flow, diff_flows) to probe
alternative explanations. Do NOT attempt exploitation beyond what is needed to confirm or refute.

When done, respond with ONLY a JSON object on the final line:
{"result":"real|refuted|uncertain","reasoning":"one or two sentences"}

Use "real" only when the payload demonstrably caused the vulnerable behavior and no innocent
explanation fits. Prefer "refuted" or "uncertain" otherwise.`

// agentVerifier is Gate 2: an adversarial verifier agent. It runs a SHORT bounded
// aiagent.Run whose system prompt instructs the model to disprove the candidate,
// with the read/replay tools available via the shared ToolExecutor, then parses
// the model's final answer into a verify.Verdict. A run that errors or yields an
// unparseable answer defaults to "uncertain" (which rejects), never "real".
type agentVerifier struct {
	newCaller func() (aiagent.ToolCaller, error)
	exec      aiagent.ToolExecutor
	budget    aiagent.Budget
	clock     aiagent.Clock
}

// verifierTools is the read/replay toolset Gate 2 may use to probe alternative
// explanations. Deliberately read-only + replay — no active-scan/intruder here.
var verifierTools = []aiagent.ToolSpec{
	{Name: "send_request", Description: "Re-send an HTTP request variant and record it as a flow.", Schema: objSchema},
	{Name: "get_flow", Description: "Fetch a recorded flow's request/response by id.", Schema: objSchema},
	{Name: "analyze_flow", Description: "Summarize a flow's notable request/response features.", Schema: objSchema},
	{Name: "diff_flows", Description: "Diff two recorded flows.", Schema: objSchema},
}

var objSchema = map[string]any{"type": "object"}

// Disprove runs the bounded adversarial agent and parses its verdict. On any
// failure to obtain a clear "real" it returns a non-real verdict so the gate
// rejects (fail-closed).
func (v *agentVerifier) Disprove(ctx context.Context, c verify.Candidate, evidence verify.DiffResult) verify.Verdict {
	if v.newCaller == nil {
		return verify.Verdict{Result: "uncertain", Reasoning: "no verifier model configured"}
	}
	tc, err := v.newCaller()
	if err != nil || tc == nil {
		return verify.Verdict{Result: "uncertain", Reasoning: "verifier model unavailable: " + errText(err)}
	}
	task := buildDisproveTask(c, evidence)
	res, err := aiagent.Run(ctx, tc, v.exec, agentVerifierSystem, task, verifierTools, v.verifierBudget(), v.clock)
	if err != nil {
		return verify.Verdict{Result: "uncertain", Reasoning: "verifier run error: " + err.Error()}
	}
	return parseVerdict(res.FinalText)
}

// verifierBudget returns a small default budget when none was injected, so the
// adversarial pass stays cheap.
func (v *agentVerifier) verifierBudget() aiagent.Budget {
	b := v.budget
	if b.MaxSteps <= 0 {
		b.MaxSteps = 6
	}
	if b.MaxToolCalls <= 0 {
		b.MaxToolCalls = 10
	}
	return b
}

// buildDisproveTask renders the candidate + Gate-1 evidence into the agent's task.
func buildDisproveTask(c verify.Candidate, evidence verify.DiffResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CANDIDATE to disprove:\n")
	fmt.Fprintf(&b, "- vuln class: %s\n", c.VulnClass)
	fmt.Fprintf(&b, "- severity:   %s\n", c.Severity)
	fmt.Fprintf(&b, "- target:     %s\n", c.Target)
	fmt.Fprintf(&b, "- point:      %s\n", c.Point)
	if c.Summary != "" {
		fmt.Fprintf(&b, "- summary:    %s\n", c.Summary)
	}
	fmt.Fprintf(&b, "\nGate-1 differential evidence (deterministic re-send):\n")
	fmt.Fprintf(&b, "- reproduced: %v (%d passes)\n", evidence.Reproduced, evidence.Times)
	if evidence.Detail != "" {
		fmt.Fprintf(&b, "- detail:     %s\n", evidence.Detail)
	}
	if len(evidence.Baseline) > 0 {
		fmt.Fprintf(&b, "- baseline flow ids: %v\n", evidence.Baseline)
	}
	if len(evidence.PayloadFlows) > 0 {
		fmt.Fprintf(&b, "- payload flow ids:  %v\n", evidence.PayloadFlows)
	}
	b.WriteString("\nTry to disprove this. Reply with the JSON verdict object on the final line.")
	return b.String()
}

// parseVerdict extracts the {"result","reasoning"} JSON object from the model's
// final text. If no valid object is found, the verdict is "uncertain" — the gate
// rejects on ambiguity, never files on a parse failure.
func parseVerdict(text string) verify.Verdict {
	if obj := lastJSONObject(text); obj != "" {
		var raw struct {
			Result    string `json:"result"`
			Reasoning string `json:"reasoning"`
		}
		if err := json.Unmarshal([]byte(obj), &raw); err == nil {
			result := strings.ToLower(strings.TrimSpace(raw.Result))
			switch result {
			case "real", "refuted", "uncertain":
				return verify.Verdict{Result: result, Reasoning: raw.Reasoning}
			}
		}
	}
	// Fallback: look for a bare keyword, defaulting to uncertain.
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "refuted"):
		return verify.Verdict{Result: "refuted", Reasoning: "parsed from free text"}
	case strings.Contains(low, "\"real\"") || strings.Contains(low, "result: real"):
		return verify.Verdict{Result: "real", Reasoning: "parsed from free text"}
	default:
		return verify.Verdict{Result: "uncertain", Reasoning: "no parseable verdict"}
	}
}

// lastJSONObject returns the last top-level {...} object substring in s, or "".
func lastJSONObject(s string) string {
	end := strings.LastIndexByte(s, '}')
	if end < 0 {
		return ""
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return s[i : end+1]
			}
		}
	}
	return ""
}

// humanConfirmer is Gate 4: a one-click human confirm for Critical/High
// candidates. It calls the injected AskHuman gate (control provides one wrapping
// its humaninput surface) with a two-option prompt; "confirm" files, anything
// else rejects. A gate error rejects (fail-closed).
type humanConfirmer struct {
	ask func(ctx context.Context, message string, options []string) (string, error)
}

const (
	confirmOption = "confirm"
	rejectOption  = "reject"
)

// Confirm surfaces the candidate to a human for a filing decision.
func (h *humanConfirmer) Confirm(ctx context.Context, c verify.Candidate, proof verify.Proof) verify.ConfirmResult {
	if h.ask == nil {
		return verify.ConfirmResult{Confirmed: false, Note: "no human-input gate configured"}
	}
	msg := buildConfirmMessage(c, proof)
	answer, err := h.ask(ctx, msg, []string{confirmOption, rejectOption})
	if err != nil {
		return verify.ConfirmResult{Confirmed: false, Note: "human gate error: " + err.Error()}
	}
	if strings.EqualFold(strings.TrimSpace(answer), confirmOption) {
		return verify.ConfirmResult{Confirmed: true, By: "human", Note: "confirmed via human gate"}
	}
	return verify.ConfirmResult{Confirmed: false, By: "human", Note: "declined: " + answer}
}

// buildConfirmMessage renders the candidate + proof into a short human prompt.
func buildConfirmMessage(c verify.Candidate, proof verify.Proof) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Confirm filing a %s finding: %s\n", c.Severity, c.VulnClass)
	fmt.Fprintf(&b, "Target: %s\n", c.Target)
	if c.Point != "" {
		fmt.Fprintf(&b, "Point: %s\n", c.Point)
	}
	if c.Summary != "" {
		fmt.Fprintf(&b, "%s\n", c.Summary)
	}
	fmt.Fprintf(&b, "Machine gates passed (differential x%d", proof.ReproCount)
	if proof.AgentVerdict.Result != "" {
		fmt.Fprintf(&b, ", agent=%s", proof.AgentVerdict.Result)
	}
	if proof.OOBToken != "" {
		fmt.Fprintf(&b, ", OOB callback")
	}
	fmt.Fprintf(&b, "). Confidence %d/100.\n", proof.Confidence)
	b.WriteString("File this as a verified finding?")
	return b.String()
}

func errText(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}
