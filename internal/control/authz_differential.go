package control

import (
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Differential authentication/authorization testing: replay ONE captured
// request as anonymous, low-privilege and admin identities, classify each
// outcome, optionally probe whether authentication runs before validation, and
// return typed evidence that keeps every raw flow ID so it can be attached to a
// finding.

// Outcome classes. They separate "who are you" from "you may not" from "your
// input is bad" from "it worked".
const (
	outcomeAuthFailure       = "auth_failure"
	outcomeAuthzFailure      = "authz_failure"
	outcomeValidationFailure = "validation_failure"
	outcomeSuccess           = "success"
	outcomeOther             = "other"
	outcomeError             = "error"
)

// Authentication-vs-validation ordering verdicts.
const (
	authOrderAuthFirst       = "auth_first"
	authOrderValidationFirst = "validation_first"
	authOrderNotEnforced     = "auth_not_enforced"
	authOrderInconclusive    = "inconclusive"
	authOrderNotTested       = "not_tested"
)

const anonymousContextName = "anonymous"

// classifyAuthzOutcome buckets one replay response. hasAuth says whether the
// context sent credentials: a 403 without credentials is an authentication
// problem, with credentials it is an authorization one.
func classifyAuthzOutcome(status int, hasAuth bool, resHeaders map[string][]string) string {
	switch {
	case status == http.StatusUnauthorized:
		return outcomeAuthFailure
	case status == http.StatusForbidden:
		if hasAuth {
			return outcomeAuthzFailure
		}
		return outcomeAuthFailure
	case isRedirect(status) && looksLikeAuthChallenge(status, resHeaders):
		return outcomeAuthFailure
	case status == http.StatusBadRequest, status == http.StatusLengthRequired,
		status == http.StatusRequestEntityTooLarge, status == http.StatusUnsupportedMediaType,
		status == http.StatusUnprocessableEntity:
		return outcomeValidationFailure
	case status >= 200 && status < 300:
		return outcomeSuccess
	}
	return outcomeOther
}

// detectAuthOrder compares the anonymous outcome for the original request with
// the anonymous outcome for a deliberately invalid body. Authentication is
// evaluated first only when both are auth failures.
func detectAuthOrder(anonValid, anonInvalid string) string {
	switch {
	case anonValid == outcomeAuthFailure && anonInvalid == outcomeAuthFailure:
		return authOrderAuthFirst
	case anonValid == outcomeAuthFailure && anonInvalid == outcomeValidationFailure:
		return authOrderValidationFirst
	case anonValid == outcomeSuccess && anonInvalid == outcomeValidationFailure:
		return authOrderNotEnforced
	}
	return authOrderInconclusive
}

// differentialSideEffect records an optional before/after observation of a
// read-only "state" flow around one context's replay.
type differentialSideEffect struct {
	BeforeFlowID int64  `json:"beforeFlowId"`
	AfterFlowID  int64  `json:"afterFlowId"`
	Changed      bool   `json:"changed"`
	Error        string `json:"error,omitempty"`
}

// differentialContext is one identity's replay result.
type differentialContext struct {
	Name           string                  `json:"name"`
	Anonymous      bool                    `json:"anonymous"`
	Outcome        string                  `json:"outcome"`
	Status         int                     `json:"status"`
	Length         int64                   `json:"length"`
	Mime           string                  `json:"mime,omitempty"`
	BodyHash       string                  `json:"bodyHash,omitempty"`
	FlowID         int64                   `json:"flowId"`
	SameAsBaseline bool                    `json:"sameAsBaseline"`
	Error          string                  `json:"error,omitempty"`
	SideEffect     *differentialSideEffect `json:"sideEffect,omitempty"`
}

// authzDifferentialEvidence is the typed result. Every raw request/response is
// a stored flow referenced by ID; nothing is inlined.
type authzDifferentialEvidence struct {
	Kind            string                `json:"kind"`
	FlowID          int64                 `json:"flowId"`
	Method          string                `json:"method"`
	Host            string                `json:"host"`
	Path            string                `json:"path"`
	Contexts        []differentialContext `json:"contexts"`
	InvalidProbe    *differentialContext  `json:"invalidBodyProbe,omitempty"`
	AuthOrder       string                `json:"authOrder"`
	AuthNotEnforced bool                  `json:"authNotEnforced"`
	// Hypotheses are inferences, not proof — reproduce before reporting.
	Hypotheses []string `json:"hypotheses,omitempty"`
}

// differentialOpts are the optional extras of a differential run.
type differentialOpts struct {
	InvalidBody    string      // non-empty: probe anonymous with this body to learn auth/validation order
	SideEffectFlow *store.Flow // optional read-only flow replayed before/after each context
}

// FlowIDs lists every raw flow referenced by the evidence, in stable order.
func (ev authzDifferentialEvidence) FlowIDs() []int64 {
	var ids []int64
	for _, c := range ev.Contexts {
		ids = append(ids, c.FlowID)
		if c.SideEffect != nil {
			ids = append(ids, c.SideEffect.BeforeFlowID, c.SideEffect.AfterFlowID)
		}
	}
	if ev.InvalidProbe != nil {
		ids = append(ids, ev.InvalidProbe.FlowID)
	}
	out := ids[:0:0]
	for _, id := range ids {
		if id > 0 {
			out = append(out, id)
		}
	}
	return out
}

// findingEvidenceSink is the small slice of the store's finding-evidence API
// the differential result needs; *store.Store satisfies it.
type findingEvidenceSink interface {
	AttachFlowWithMetadata(findingID, flowID int64, note string, pos int, role, proof, source string, sourceFlowID int64, changes ...store.FindingChange) error
}

// AttachTo appends every retained flow to a finding as typed evidence blocks:
// the first context is the "baseline", other contexts "result", side-effect
// checks "observation" and the invalid-body probe "control".
func (ev authzDifferentialEvidence) AttachTo(sink findingEvidenceSink, findingID int64) error {
	attach := func(flowID int64, role, note string) error {
		if flowID <= 0 {
			return nil
		}
		return sink.AttachFlowWithMetadata(findingID, flowID, note, -1, role, "", "captured_flow", flowID)
	}
	for i, c := range ev.Contexts {
		role := "result"
		if i == 0 {
			role = "baseline"
		}
		if err := attach(c.FlowID, role, "authz differential — "+c.Name+": "+c.Outcome); err != nil {
			return err
		}
		if c.SideEffect != nil {
			if err := attach(c.SideEffect.BeforeFlowID, "observation", "state before "+c.Name+" replay"); err != nil {
				return err
			}
			if err := attach(c.SideEffect.AfterFlowID, "observation", "state after "+c.Name+" replay"); err != nil {
				return err
			}
		}
	}
	if ev.InvalidProbe != nil {
		return attach(ev.InvalidProbe.FlowID, "control", "authz differential — anonymous with invalid body: "+ev.InvalidProbe.Outcome)
	}
	return nil
}

func contextFromResult(rr authzResult, hasAuth bool) differentialContext {
	c := differentialContext{
		Name: rr.Name, Anonymous: !hasAuth, Status: rr.Status, Length: rr.Length,
		Mime: rr.Mime, BodyHash: rr.BodyHash, FlowID: rr.FlowID, Error: rr.Error,
	}
	if rr.Error != "" {
		c.Outcome = outcomeError
	} else {
		c.Outcome = classifyAuthzOutcome(rr.Status, hasAuth, rr.resHeaders)
	}
	return c
}

// differentialContexts returns the saved (non-broken) identities plus an
// implicit anonymous context unless one is already present.
func differentialContexts(ids []identity) []identity {
	var out []identity
	haveAnon := false
	for _, id := range ids {
		if id.Broken {
			continue
		}
		if !identityHasAuth(id) {
			haveAnon = true
		}
		out = append(out, id)
	}
	if !haveAnon {
		out = append(out, identity{Name: anonymousContextName})
	}
	return out
}

func (h *authzAPI) sideEffectProbe(obs *store.Flow, observer identity) (authzResult, bool) {
	rr := h.authzReplay(obs, observer)
	return rr, rr.Error == ""
}

// observeAround replays obs before and after run() and reports whether the
// observed state changed.
func (h *authzAPI) observeAround(obs *store.Flow, run func()) *differentialSideEffect {
	if obs == nil {
		run()
		return nil
	}
	observer := identity{Name: "observer", Headers: extractAuthHeaders(obs.ReqHeaders)}
	before, okB := h.sideEffectProbe(obs, observer)
	run()
	after, okA := h.sideEffectProbe(obs, observer)
	se := &differentialSideEffect{BeforeFlowID: before.FlowID, AfterFlowID: after.FlowID}
	if !okB || !okA {
		se.Error = strings.TrimSpace(before.Error + " " + after.Error)
		return se
	}
	se.Changed = before.Status != after.Status || before.BodyHash != after.BodyHash
	return se
}

// authzDifferential runs one flow across the given identities (plus
// anonymous) and builds the typed evidence.
func (h *authzAPI) authzDifferential(f *store.Flow, ids []identity, opts differentialOpts) authzDifferentialEvidence {
	ev := authzDifferentialEvidence{
		Kind: "authz_differential", FlowID: f.ID, Method: f.Method, Host: f.Host, Path: f.Path,
		AuthOrder: authOrderNotTested,
	}
	var baseStatus int
	var baseLen int64
	var baseHash, baseMime string
	haveBase := false
	var anonID *identity
	for _, id := range differentialContexts(ids) {
		id := id
		hasAuth := identityHasAuth(id)
		if !hasAuth && anonID == nil {
			anonID = &id
		}
		var rr authzResult
		se := h.observeAround(opts.SideEffectFlow, func() {
			rr = h.authzReplay(f, id)
			rr.Name = id.Name
		})
		c := contextFromResult(rr, hasAuth)
		c.SideEffect = se
		if !haveBase && rr.Error == "" {
			baseStatus, baseLen, baseHash, baseMime, haveBase = rr.Status, rr.Length, rr.BodyHash, rr.Mime, true
			c.SameAsBaseline = true
		} else if haveBase {
			c.SameAsBaseline = authzSameAccess(baseStatus, baseLen, baseHash, baseMime, rr)
		}
		ev.Contexts = append(ev.Contexts, c)
	}
	h.finishDifferential(&ev, f, anonID, opts)
	return ev
}

// finishDifferential derives the hypotheses and the optional ordering probe.
func (h *authzAPI) finishDifferential(ev *authzDifferentialEvidence, f *store.Flow, anon *identity, opts differentialOpts) {
	var anonCtx *differentialContext
	for i := range ev.Contexts {
		if ev.Contexts[i].Anonymous {
			anonCtx = &ev.Contexts[i]
			break
		}
	}
	if anonCtx != nil && len(ev.Contexts) > 1 && ev.Contexts[0].Outcome == outcomeSuccess &&
		anonCtx.Outcome == outcomeSuccess && anonCtx.SameAsBaseline && !ev.Contexts[0].Anonymous {
		ev.AuthNotEnforced = true
		ev.Hypotheses = append(ev.Hypotheses,
			"Hypothesis: authentication may not be enforced — the anonymous replay succeeded with the same response as the baseline identity. Reproduce before reporting.")
	}
	if opts.InvalidBody != "" && anon != nil && anonCtx != nil {
		rr := h.authzReplayBody(f, *anon, []byte(opts.InvalidBody))
		rr.Name = anonymousContextName + "+invalid-body"
		probe := contextFromResult(rr, false)
		ev.InvalidProbe = &probe
		ev.AuthOrder = detectAuthOrder(anonCtx.Outcome, probe.Outcome)
		switch ev.AuthOrder {
		case authOrderValidationFirst:
			ev.Hypotheses = append(ev.Hypotheses,
				"Hypothesis: input validation runs before authentication — an invalid body gets a validation error while a valid one gets an auth error. Reproduce before reporting.")
		case authOrderNotEnforced:
			ev.Hypotheses = append(ev.Hypotheses,
				"Hypothesis: the endpoint processes anonymous requests (valid body succeeds, invalid body is rejected only by validation). Reproduce before reporting.")
		}
	}
	for _, c := range ev.Contexts {
		if c.SideEffect != nil && c.SideEffect.Changed && c.Outcome != outcomeSuccess {
			ev.Hypotheses = append(ev.Hypotheses,
				"Hypothesis: the "+c.Name+" replay was reported as "+c.Outcome+" but the observed state changed — a side effect may occur despite the rejection.")
		}
	}
}
