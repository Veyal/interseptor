package autopwn

import (
	"context"
	"strings"

	"github.com/Veyal/interseptor/internal/aiagent"
)

// reconSampleLimit bounds the endpoint sample injected into the planning prompt.
const reconSampleLimit = 120

// reconDigest builds a deterministic, compact digest of the captured in-scope
// history by driving the read-only recon tools DIRECTLY over the tool bus, rather
// than relying on the planning model to call tools itself.
//
// This is the fix for autopilot runs that produced empty plans: many
// OpenAI-compatible endpoints (and some models) do not honor function-calling —
// they return prose instead of `tool_calls` — so the planning agent never called
// list_flows and hallucinated "no in-scope history", yielding a zero-step plan.
// By gathering scope + host + endpoint context deterministically and injecting it
// into the planning task, the model can produce a real plan from prompt context
// alone. Tool-capable models still get the tools too, to dig deeper.
func (e *Engine) reconDigest(ctx context.Context) string {
	exec := e.executor()
	call := func(name string, args map[string]any) string {
		if ctx.Err() != nil {
			return "(cancelled)"
		}
		out, err := exec.Exec(ctx, aiagent.ToolCall{Name: name, Args: args})
		if err != nil {
			return "(" + name + " unavailable: " + err.Error() + ")"
		}
		return strings.TrimSpace(out)
	}

	var b strings.Builder
	b.WriteString("RECON CONTEXT — deterministic digest of the captured in-scope HTTP history.\n")
	b.WriteString("Base your plan on this context. The read-only recon tools are also available if you can call them.\n\n")
	b.WriteString("## Scope rules\n")
	b.WriteString(truncateDigest(call("list_scope", map[string]any{}), 1500))
	b.WriteString("\n\n## Hosts by traffic\n")
	b.WriteString(truncateDigest(call("host_stats", map[string]any{}), 2500))
	b.WriteString("\n\n## Endpoint sample (id · method · host · path · status)\n")
	b.WriteString(truncateDigest(call("list_flows", map[string]any{"limit": reconSampleLimit}), 7000))
	b.WriteString("\n")
	return b.String()
}

// truncateDigest bounds one digest section so a large history cannot blow the
// planning prompt's token budget.
func truncateDigest(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	if len(s) > max {
		return s[:max] + "\n…(truncated)"
	}
	return s
}
