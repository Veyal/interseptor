package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
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

// runStep executes one request item through the shared backend (the same
// pipeline, variable layers and script engine as the runner and the CLI) and
// applies variable changes per the persist policy.
func (c *collectionsAPI) runStep(ctx context.Context, chain collexec.Chain, rq stepRequest, src collexec.Source, ai bool, policy string) (*stepOut, error) {
	be := c.backend()
	layers, local, err := be.Layers(chain, rq.EnvUID)
	if err != nil {
		return nil, err
	}
	if len(rq.Local) > 0 {
		if local == nil {
			local = map[string]string{}
		}
		for k, v := range rq.Local {
			local[k] = v
		}
	}
	cookieSnap := be.SnapshotCookies(chain.Collection.UID, rq.EnvUID, rq.Identity)
	res, err := be.Step(ctx, collexec.StepInput{
		Chain: chain, Layers: layers, Local: local, Source: src, AI: ai, ScopePolicy: policy,
		NoScripts: rq.NoScripts, EnvUID: rq.EnvUID, EnvPin: be.EnvPin(rq.EnvUID), Identity: rq.Identity,
	}, collrun.StepMeta{IterationCount: 1})
	if err != nil {
		be.FinishCookies(chain.Collection.UID, rq.EnvUID, rq.Identity, cookieSnap, false)
		return nil, err
	}
	out := &stepOut{StepResult: res}
	keep := rq.Persist == "keep"
	be.FinishCookies(chain.Collection.UID, rq.EnvUID, rq.Identity, cookieSnap, keep)
	if keep {
		out.Skipped = be.Commit(chain.Collection, rq.EnvUID, res.VarChanges)
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
	out, err := c.runStep(ctx, chain, in, sourceOf(r), isAISource(r), policy)
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
	// Runner-view options (async runs).
	Iterations    int      `json:"iterations"`
	RPS           float64  `json:"rps"`
	FailedFromRun string   `json:"failedFromRun"`
	Data          *runData `json:"data"`
}

// runData is an inline CSV/JSON iteration data file.
type runData struct {
	Name string `json:"name"` // file name; the extension picks the parser
	Text string `json:"text"`
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

// runOptions validates a run request into runner options. async selects the
// persist default of the asynchronous API (ask for a human, discard for the
// AI channel); the synchronous API can never ask. It writes the error response
// itself and reports false on failure.
func (c *collectionsAPI) runOptions(w http.ResponseWriter, r *http.Request, in runRequest, async bool) (collrun.Options, bool) {
	var opt collrun.Options
	policy, ok := scopePolicyOverride(r, in.ScopePolicy)
	if !ok {
		httpErr(w, http.StatusBadRequest, "scopePolicy must be block, warn or off (not settable from the AI channel)")
		return opt, false
	}
	if policy == "" {
		// Runs are headless-style: scope blocks unless a human says otherwise.
		policy = store.ScopePolicyBlock
	}
	def := "discard"
	if async && !isAISource(r) {
		def = collrun.PersistAsk
	}
	persist, ok := persistOrDefault(in.Persist, def)
	if !ok && async && in.Persist == collrun.PersistAsk && !isAISource(r) {
		persist, ok = collrun.PersistAsk, true
	}
	if !ok {
		msg := "persist must be keep or discard"
		if async {
			msg += " (ask is for interactive runs only)"
		}
		httpErr(w, http.StatusBadRequest, msg)
		return opt, false
	}
	switch in.Bail {
	case "", "none", "on-failure", "on-error":
	default:
		httpErr(w, http.StatusBadRequest, "bail must be none, on-failure or on-error")
		return opt, false
	}
	if in.DelayMs < 0 || in.DelayMs > 60000 {
		httpErr(w, http.StatusBadRequest, "delayMs must be between 0 and 60000")
		return opt, false
	}
	if in.Iterations < 0 || in.Iterations > collrun.MaxIterations || in.RPS < 0 || in.RPS > 10000 {
		httpErr(w, http.StatusBadRequest, "iterations or rps out of range")
		return opt, false
	}
	var data *collrun.Dataset
	if in.Data != nil && strings.TrimSpace(in.Data.Text) != "" {
		var err error
		if data, err = collrun.ParseData(in.Data.Name, strings.NewReader(in.Data.Text)); err != nil {
			httpErr(w, http.StatusBadRequest, "iteration data: "+err.Error())
			return opt, false
		}
	}
	limit := in.MaxItems
	if limit <= 0 || limit > maxRunItems {
		limit = maxRunItems
	}
	_, items, err := c.backend().Load(in.CollectionUID)
	if err != nil {
		collErr(w, err)
		return opt, false
	}
	pick := in.ItemUIDs
	if in.FailedFromRun != "" {
		failed, err := collrun.FailedItemUIDs(c.h.st, in.FailedFromRun)
		if err != nil || len(failed) == 0 {
			httpErr(w, http.StatusBadRequest, "nothing to rerun: that run had no failed requests")
			return opt, false
		}
		pick = failed
	}
	plan := collrun.PlanItems(items, in.FolderUID, pick)
	if len(plan) == 0 {
		httpErr(w, http.StatusBadRequest, "nothing to run: no requests selected")
		return opt, false
	}
	if len(plan) > limit {
		httpErr(w, http.StatusBadRequest, "run would exceed the item limit; narrow the selection or raise maxItems (max 1000)")
		return opt, false
	}
	return collrun.Options{
		CollectionUID: in.CollectionUID, FolderUID: in.FolderUID, ItemUIDs: in.ItemUIDs, FailedFromRun: in.FailedFromRun,
		EnvUID: in.EnvUID, Iterations: in.Iterations, Data: data,
		Delay: time.Duration(in.DelayMs) * time.Millisecond, RPS: in.RPS, Bail: in.Bail, Persist: persist,
		NoScripts: in.NoScripts, ScopePolicy: policy, Source: sourceOf(r), AI: isAISource(r), Identity: in.Identity,
	}, true
}

// startRun launches a run through the Manager. Lifecycle and per-request
// events are forwarded to every SSE client as {type:"collrun", event:...}.
func (c *collectionsAPI) startRun(opt collrun.Options) (*collrun.LiveRun, error) {
	opt.OnEvent = func(e collrun.Event) {
		c.h.broadcast(map[string]any{"type": "collrun", "event": e})
	}
	return c.mgr.Start(c.backend(), c.h.st, opt)
}

// startErr maps a Manager.Start failure to an HTTP status.
func startErr(w http.ResponseWriter, err error) {
	if errors.Is(err, collrun.ErrTooManyRuns) {
		httpErr(w, http.StatusTooManyRequests, "too many active runs; wait for one to finish or abort it")
		return
	}
	collErr(w, err)
}

// run is the synchronous API (MCP run_collection, scripts): it starts a run on
// the shared runner and answers when it ends, in the compact legacy shape.
func (c *collectionsAPI) run(w http.ResponseWriter, r *http.Request) {
	var in runRequest
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	opt, ok := c.runOptions(w, r, in, false)
	if !ok {
		return
	}
	lr, err := c.startRun(opt)
	if err != nil {
		startErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runDeadline)
	defer cancel()
	rep, err := lr.Wait(ctx)
	if rep == nil && ctx.Err() != nil {
		lr.Abort() // client went away or the deadline passed
		rep, err = lr.Wait(context.Background())
	}
	if err != nil || rep == nil {
		httpInternalErr(w, errors.New("run failed"))
		return
	}
	rows := legacyRows(rep)
	c.writeStep(w, r, map[string]any{"runUid": rep.RunUID, "status": rep.Status, "summary": summarize(rows), "results": rows})
}

// legacyRows folds a report into the compact per-request rows of the
// synchronous API.
func legacyRows(rep *collrun.Report) []runRow {
	rows := make([]runRow, 0, len(rep.Items))
	for _, it := range rep.Items {
		row := runRow{ItemUID: it.ItemUID, Name: it.Name, Outcome: it.Outcome, Block: it.BlockReason, FlowID: it.FlowID,
			Status: it.HTTPStatus, Error: it.Error, Tests: testCounts(it.Tests)}
		for _, s := range it.Scripts {
			if strings.HasPrefix(s.Reason, "quarantined") {
				row.Quarantine++
			}
		}
		rows = append(rows, row)
	}
	return rows
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
