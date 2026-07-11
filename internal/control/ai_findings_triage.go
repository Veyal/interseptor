package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/mcp"
)

// Findings triage: a lighter alternative to autopwn. The operator asks the
// in-app AI to review captured in-scope history and file evidence-backed
// findings (create_finding + add_finding_poc) without launching active attacks.

const findingsTriageSystem = `You are Interseptor's findings triage assistant for an authorized pentest.

Your job: review captured in-scope HTTP history and passive issues, then file ONLY evidence-backed findings.

Rules:
- Do NOT run active attacks (no active_scan, Intruder, authz fuzz, or send_request probes). Triage only.
- Dedupe against existing findings (titles/targets already listed). Skip duplicates.
- File via create_finding then add_finding_poc (text → flow → text notebook). Prefer attaching real flows over pasting raw HTTP into detail.
- Use the required finding format (## Summary impact-first, ## Evidence, ## Impact; ## Needs Verification when unproven).
- Mark needs_verification when evidence is suggestive but not proven; include verificationInstructions.
- When done, reply with a short summary only:
  filed: <n> — titles
  skipped: <n> — reasons (dup / no evidence / …)
  needs_verification: <n> — titles
`

var findingsTriageToolNames = []string{
	"list_scope", "host_stats", "list_flows", "get_flow", "list_issues",
	"list_findings", "create_finding", "add_finding_poc", "update_finding",
}

const findingsTriageSampleLimit = 80

// aiFindingsTriage runs a budgeted agent that triages history and files findings.
// Body: {steer?: string}. Streams SSE: status / tool / text / done / error.
func (h *aiAPI) aiFindingsTriage(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	var in struct {
		Steer string `json:"steer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}

	tc, err := h.autopwnToolCaller()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Ensure the in-process tool bus exists (same lazy path as autopwn).
	_ = h.autopwn()
	toolsSrv := h.autopwnTools
	if toolsSrv == nil {
		httpErr(w, http.StatusInternalServerError, "tool bus unavailable")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emit := func(event string, payload any) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	emit("status", map[string]any{"message": "Building triage context…"})
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()

	digest := findingsTriageDigest(ctx, toolsSrv)
	task := "Triage in-scope history and file evidence-backed findings.\n\n" + digest
	if strings.TrimSpace(in.Steer) != "" {
		task += "\n\nOperator steer: " + strings.TrimSpace(in.Steer)
	}

	specs := toolSpecsFromMCP(toolsSrv, findingsTriageToolNames)
	exec := &triageToolExec{srv: toolsSrv, emit: emit}
	budget := aiagent.Budget{MaxSteps: 16, MaxToolCalls: 40, MaxWallMs: 3 * 60 * 1000}

	emit("status", map[string]any{"message": "Triaging…"})
	res, runErr := aiagent.Run(ctx, tc, exec, findingsTriageSystem, task, specs, budget, aiagent.RealClock{})
	if runErr != nil && res.FinalText == "" {
		emit("error", map[string]any{"message": runErr.Error()})
		return
	}
	text := res.FinalText
	if text == "" {
		text = "(no summary — check Activity / Findings for any filings)"
	}
	if runErr != nil {
		text += "\n\n_(stopped: " + runErr.Error() + ")_"
	}
	emit("text", map[string]any{"text": text})
	emit("done", map[string]any{
		"stoppedBy": res.StoppedBy,
		"steps":     res.Steps,
		"toolCalls": res.ToolCalls,
	})
}

// findingsTriageDigest builds a deterministic context block so triage does not
// depend on the model choosing to call recon tools first.
func findingsTriageDigest(ctx context.Context, srv *mcp.Server) string {
	call := func(name string, args map[string]any) string {
		if ctx.Err() != nil {
			return "(cancelled)"
		}
		out, err := srv.Call(name, args)
		if err != nil {
			return "(" + name + " unavailable: " + err.Error() + ")"
		}
		return strings.TrimSpace(out)
	}
	trunc := func(s string, max int) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return "(none)"
		}
		if len(s) > max {
			return s[:max] + "\n…(truncated)"
		}
		return s
	}
	var b strings.Builder
	b.WriteString("## Existing findings (dedupe against these)\n")
	b.WriteString(trunc(call("list_findings", map[string]any{}), 3000))
	b.WriteString("\n\n## Scope rules\n")
	b.WriteString(trunc(call("list_scope", map[string]any{}), 1500))
	b.WriteString("\n\n## Hosts by traffic\n")
	b.WriteString(trunc(call("host_stats", map[string]any{}), 2500))
	b.WriteString("\n\n## Passive scan issues\n")
	b.WriteString(trunc(call("list_issues", map[string]any{}), 2500))
	b.WriteString("\n\n## In-scope flow sample\n")
	b.WriteString(trunc(call("list_flows", map[string]any{"limit": findingsTriageSampleLimit}), 7000))
	b.WriteString("\n")
	return b.String()
}

func toolSpecsFromMCP(srv *mcp.Server, names []string) []aiagent.ToolSpec {
	if srv == nil {
		return nil
	}
	out := make([]aiagent.ToolSpec, 0, len(names))
	for _, name := range names {
		desc, schema, ok := srv.ToolMeta(name)
		if !ok {
			continue
		}
		out = append(out, aiagent.ToolSpec{Name: name, Description: desc, Schema: schema})
	}
	return out
}

type triageToolExec struct {
	srv  *mcp.Server
	emit func(event string, payload any)
}

func (e *triageToolExec) Exec(ctx context.Context, call aiagent.ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.emit != nil {
		e.emit("tool", map[string]any{"name": call.Name, "args": call.Args})
	}
	return e.srv.Call(call.Name, call.Args)
}
