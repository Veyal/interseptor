package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
)

// runDeadline bounds one whole run request regardless of item count.
var runDeadline = 10 * time.Minute

type stepRequest struct {
	ItemUID     string            `json:"itemUid"`
	EnvUID      string            `json:"envUid"`
	Local       map[string]string `json:"local"`
	NoScripts   bool              `json:"noScripts"`
	Persist     string            `json:"persist"` // keep | discard
	ScopePolicy string            `json:"scopePolicy"`
	Identity    string            `json:"identity"`
}

func sourceOf(r *http.Request) collexec.Source {
	if isAISource(r) {
		return collexec.SourceMCP
	}
	return collexec.SourceUI
}

// scopePolicyOverride validates a caller-supplied policy. The AI channel may
// never choose one: it always gets the collection's (default block).
func scopePolicyOverride(r *http.Request, p string) (string, bool) {
	if p == "" {
		return "", true
	}
	if isAISource(r) {
		return "", false
	}
	switch p {
	case store.ScopePolicyBlock, store.ScopePolicyWarn, store.ScopePolicyOff:
		return p, true
	}
	return "", false
}

type stepOut struct {
	*collexec.StepResult
	Skipped []string `json:"variableWritesSkipped,omitempty"`
}

// runOne executes one request item through the shared pipeline and applies
// variable changes per the persist policy. ov (may be nil) carries script
// writes between the steps of a run.
func (c *collectionsAPI) runOne(ctx context.Context, chain collexec.Chain, rq stepRequest, src collexec.Source, ai bool, policy string, ov *varOverlay, runUID string, iter, count int) (*stepOut, error) {
	layers, local, err := c.layersFor(chain, rq.EnvUID)
	if err != nil {
		return nil, err
	}
	if ov != nil {
		layers = ov.apply(layers)
	}
	if len(rq.Local) > 0 {
		if local == nil {
			local = map[string]string{}
		}
		for k, v := range rq.Local {
			local[k] = v
		}
	}
	var pin string
	if rq.EnvUID != "" {
		if e, err := c.h.st.GetEnvironment(rq.EnvUID); err == nil {
			pin = e.BaseTargetPin
		}
	}
	env := &stepEnv{c: c, coll: chain.Collection, source: src, ai: ai, envUID: rq.EnvUID, layers: layers, iter: iter, count: count}
	p := c.pipeline(env)
	res, err := p.Step(ctx, collexec.StepInput{
		Chain: chain, Layers: layers, Local: local, Source: src, AI: ai, ScopePolicy: policy,
		NoScripts: rq.NoScripts, RunID: runUID, Iteration: iter, EnvUID: rq.EnvUID, EnvPin: pin, Identity: rq.Identity,
	})
	if err != nil {
		return nil, err
	}
	out := &stepOut{StepResult: res}
	if ov != nil {
		ov.record(res.VarChanges)
	}
	if rq.Persist == "keep" {
		out.Skipped = c.commitChanges(chain.Collection, rq.EnvUID, res.VarChanges)
	}
	return out, nil
}

// writeStep answers a step. The AI channel gets one more masking pass over
// the serialized result as defence in depth.
func (c *collectionsAPI) writeStep(w http.ResponseWriter, r *http.Request, v any) {
	if !isAISource(r) {
		writeJSON(w, http.StatusOK, v)
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(c.reg.Mask(string(b))))
}

func persistOrDefault(p string, def string) (string, bool) {
	switch p {
	case "":
		return def, true
	case "keep", "discard":
		return p, true
	}
	return "", false
}

func (c *collectionsAPI) send(w http.ResponseWriter, r *http.Request) {
	var in stepRequest
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	policy, ok := scopePolicyOverride(r, in.ScopePolicy)
	if !ok {
		httpErr(w, http.StatusBadRequest, "scopePolicy must be block, warn or off (not settable from the AI channel)")
		return
	}
	// A single interactive send keeps script variable writes (Postman parity);
	// the AI channel must opt in.
	def := "discard"
	if !isAISource(r) {
		def = "keep"
	}
	if in.Persist, ok = persistOrDefault(in.Persist, def); !ok {
		httpErr(w, http.StatusBadRequest, "persist must be keep or discard")
		return
	}
	if in.ItemUID == "" {
		httpErr(w, http.StatusBadRequest, "itemUid required")
		return
	}
	chain, err := collexec.LoadChain(c.h.st, in.ItemUID)
	if err != nil {
		collErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runDeadline)
	defer cancel()
	out, err := c.runOne(ctx, chain, in, sourceOf(r), isAISource(r), policy, nil, "", 0, 1)
	if err != nil {
		collErr(w, err)
		return
	}
	c.writeStep(w, r, out)
}

type runRequest struct {
	CollectionUID string   `json:"collectionUid"`
	FolderUID     string   `json:"folderUid"`
	ItemUIDs      []string `json:"itemUids"`
	EnvUID        string   `json:"envUid"`
	NoScripts     bool     `json:"noScripts"`
	Persist       string   `json:"persist"`
	Bail          string   `json:"bail"` // none | on-failure | on-error
	DelayMs       int      `json:"delayMs"`
	MaxItems      int      `json:"maxItems"`
	ScopePolicy   string   `json:"scopePolicy"`
	Identity      string   `json:"identity"`
}

// planItems lists the requests a run would execute in tree order.
func planItems(items []store.Item, folderUID string, pick []string) []store.Item {
	byUID := map[string]store.Item{}
	for _, it := range items {
		byUID[it.UID] = it
	}
	if len(pick) > 0 {
		var out []store.Item
		for _, u := range pick {
			if it, ok := byUID[u]; ok && it.Kind == "request" {
				out = append(out, it)
			}
		}
		return out
	}
	var out []store.Item
	var walk func(ns []*collection.Node)
	walk = func(ns []*collection.Node) {
		for _, n := range ns {
			if n.Kind == "request" {
				out = append(out, n.Item)
			}
			walk(n.Children)
		}
	}
	roots := collection.BuildTree(items)
	if folderUID == "" {
		walk(roots)
		return out
	}
	var find func(ns []*collection.Node) *collection.Node
	find = func(ns []*collection.Node) *collection.Node {
		for _, n := range ns {
			if n.UID == folderUID {
				return n
			}
			if f := find(n.Children); f != nil {
				return f
			}
		}
		return nil
	}
	if f := find(roots); f != nil && f.Kind == "folder" {
		walk(f.Children)
	}
	return out
}

type runRow struct {
	ItemUID    string               `json:"itemUid"`
	Name       string               `json:"name"`
	Outcome    collexec.Outcome     `json:"outcome"`
	Block      collexec.BlockReason `json:"blockReason,omitempty"`
	FlowID     int64                `json:"flowId,omitempty"`
	Status     int                  `json:"status,omitempty"`
	Error      string               `json:"error,omitempty"`
	Tests      map[string]int       `json:"tests,omitempty"`
	Quarantine int                  `json:"quarantinedScripts,omitempty"`
}

func testCounts(ts []collexec.TestResult) map[string]int {
	if len(ts) == 0 {
		return nil
	}
	m := map[string]int{}
	for _, t := range ts {
		m[string(t.Status)]++
	}
	return m
}

func (c *collectionsAPI) run(w http.ResponseWriter, r *http.Request) {
	var in runRequest
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	policy, ok := scopePolicyOverride(r, in.ScopePolicy)
	if !ok {
		httpErr(w, http.StatusBadRequest, "scopePolicy must be block, warn or off (not settable from the AI channel)")
		return
	}
	if policy == "" {
		// Runs are headless-style: scope blocks unless a human says otherwise.
		policy = store.ScopePolicyBlock
	}
	persist, ok := persistOrDefault(in.Persist, "discard")
	if !ok {
		httpErr(w, http.StatusBadRequest, "persist must be keep or discard")
		return
	}
	switch in.Bail {
	case "", "none", "on-failure", "on-error":
	default:
		httpErr(w, http.StatusBadRequest, "bail must be none, on-failure or on-error")
		return
	}
	if in.DelayMs < 0 || in.DelayMs > 60000 {
		httpErr(w, http.StatusBadRequest, "delayMs must be between 0 and 60000")
		return
	}
	limit := in.MaxItems
	if limit <= 0 || limit > maxRunItems {
		limit = maxRunItems
	}
	co, err := c.h.st.GetCollection(in.CollectionUID)
	if err != nil {
		collErr(w, err)
		return
	}
	items, err := c.h.st.ListItems(co.UID)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	plan := planItems(items, in.FolderUID, in.ItemUIDs)
	if len(plan) == 0 {
		httpErr(w, http.StatusBadRequest, "nothing to run: no requests selected")
		return
	}
	if len(plan) > limit {
		httpErr(w, http.StatusBadRequest, "run would exceed the item limit; narrow the selection or raise maxItems (max 1000)")
		return
	}
	src, ai := sourceOf(r), isAISource(r)
	run, err := c.h.st.PutRun(store.CollRun{CollectionUID: co.UID, EnvUID: in.EnvUID, Source: string(src), Status: "running"})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runDeadline)
	defer cancel()
	rows, status := c.execPlan(ctx, *co, items, plan, in, persist, policy, src, ai, run.UID)
	summary := summarize(rows)
	sumJSON, _ := json.Marshal(summary)
	run.Status, run.FinishedTS, run.SummaryJSON = status, time.Now().UnixMilli(), string(sumJSON)
	_, _ = c.h.st.PutRun(*run)
	c.writeStep(w, r, map[string]any{"runUid": run.UID, "status": status, "summary": summary, "results": rows})
}

func (c *collectionsAPI) execPlan(ctx context.Context, co store.Collection, items []store.Item, plan []store.Item, in runRequest, persist, policy string, src collexec.Source, ai bool, runUID string) ([]runRow, string) {
	ov := newOverlay()
	byName := map[string]int{}
	for i, it := range plan {
		byName[it.UID] = i
		if _, dup := byName[it.Name]; !dup {
			byName[it.Name] = i
		}
	}
	var rows []runRow
	maxSteps := len(plan) * 10
	if maxSteps > maxRunSteps {
		maxSteps = maxRunSteps
	}
	status := "done"
	for i, steps := 0, 0; i < len(plan); steps++ {
		if steps >= maxSteps {
			status = "aborted: step limit (setNextRequest loop guard)"
			break
		}
		if ctx.Err() != nil {
			status = "aborted"
			break
		}
		it := plan[i]
		chain, err := collexec.ChainFromItems(co, items, it.UID)
		if err != nil {
			rows = append(rows, runRow{ItemUID: it.UID, Name: it.Name, Outcome: collexec.OutcomeError, Error: "chain: " + err.Error()})
			i++
			continue
		}
		rq := stepRequest{ItemUID: it.UID, EnvUID: in.EnvUID, NoScripts: in.NoScripts, Persist: persist, Identity: in.Identity}
		out, err := c.runOne(ctx, chain, rq, src, ai, policy, ov, runUID, 0, 1)
		if err != nil {
			rows = append(rows, runRow{ItemUID: it.UID, Name: it.Name, Outcome: collexec.OutcomeError, Error: "step failed"})
			i++
			continue
		}
		row := runRow{ItemUID: it.UID, Name: it.Name, Outcome: out.Outcome, Block: out.BlockReason, FlowID: out.FlowID,
			Error: out.Error, Tests: testCounts(out.Tests)}
		if out.Response != nil {
			row.Status = out.Response.Status
		}
		for _, s := range out.Scripts {
			if strings.HasPrefix(s.Reason, "quarantined") {
				row.Quarantine++
			}
		}
		rows = append(rows, row)
		raw, _ := json.Marshal(out)
		_, _ = c.h.st.AddRunResult(store.CollRunResult{RunUID: runUID, ItemUID: it.UID, FlowID: out.FlowID,
			Status: string(out.Outcome), ResultJSON: c.reg.Mask(string(raw))})
		if (in.Bail == "on-failure" && (out.Failed() || out.Outcome != collexec.OutcomeSent)) ||
			(in.Bail == "on-error" && (out.Outcome == collexec.OutcomeError || out.Outcome == collexec.OutcomeBlocked)) {
			status = "bailed"
			break
		}
		next := i + 1
		if out.Flow.HasNext {
			if out.Flow.NextRequest == "" {
				break
			}
			if j, ok := byName[out.Flow.NextRequest]; ok {
				next = j
			}
		}
		i = next
		if in.DelayMs > 0 && i < len(plan) {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(in.DelayMs) * time.Millisecond):
			}
		}
	}
	return rows, status
}

type runSummary struct {
	Total      int            `json:"total"`
	Outcomes   map[string]int `json:"outcomes"`
	Tests      map[string]int `json:"tests"`
	Quarantine int            `json:"quarantinedScripts"`
}

func summarize(rows []runRow) runSummary {
	s := runSummary{Total: len(rows), Outcomes: map[string]int{}, Tests: map[string]int{}}
	for _, r := range rows {
		s.Outcomes[string(r.Outcome)]++
		for k, v := range r.Tests {
			s.Tests[k] += v
		}
		s.Quarantine += r.Quarantine
	}
	return s
}

func (c *collectionsAPI) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := c.h.st.ListRuns(r.PathValue("uid"), 50)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (c *collectionsAPI) getRun(w http.ResponseWriter, r *http.Request) {
	rows, err := c.h.st.ListRunResults(r.PathValue("uid"))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": rows})
}
