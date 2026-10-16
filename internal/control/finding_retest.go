package control

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// Finding retest: replay a finding's recorded evidence and report what the
// application does now.
//
// The hard rule here is about claims. A retest observes one response at one
// moment, under whatever session and state happen to exist. That is enough to
// say "the same request now returns 403 where the evidence recorded 200"; it is
// not enough to say the issue is fixed, and this endpoint never says so. It
// reports `unchanged`, `changed` or `inconclusive`, lists exactly what differed,
// and leaves the finding's status to the reviewer.
//
// The result is recorded by attaching the replay as a new `retest` evidence
// block, which appends a finding revision. Nothing in the original evidence is
// edited or removed: revisions are immutable, and a retest is a later
// observation, not a correction of the earlier one.

const maxRetestRequestBytes = 4 << 10

// maxRetestFlows bounds one retest so a finding with a long evidence timeline
// cannot turn a single click into a burst of traffic at the target.
const maxRetestFlows = 10

// idempotentMethods are replayed without an explicit opt-in. Anything else may
// change state at the target — re-sending the POST that created an order, or the
// DELETE that removed a record, is not a retest, it is a second attack — so it
// is skipped unless the caller asks for it.
var idempotentMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true, "TRACE": true,
}

type retestRequest struct {
	// FlowIDs narrows the replay to these attached evidence flows; empty means
	// every non-missing attached flow, oldest first.
	FlowIDs []int64 `json:"flowIds"`
	// Session is "flow" (default: replay exactly as captured) or "current"
	// (apply the active session headers), as on POST /api/flows/{id}/replay.
	Session string `json:"session"`
	// AllowUnsafeMethods permits replaying non-idempotent requests.
	AllowUnsafeMethods bool `json:"allowUnsafeMethods"`
	// Attach records the retest as a new evidence revision; default true.
	Attach *bool `json:"attach"`
}

// retestObservation is what was seen for one replayed flow. Both sides are
// stated so a reviewer can judge the comparison instead of trusting a verdict.
type retestObservation struct {
	EvidenceFlowID int64       `json:"evidenceFlowId"`
	RetestFlowID   int64       `json:"retestFlowId,omitempty"`
	Request        string      `json:"request"`
	Observation    string      `json:"observation"` // unchanged | changed | inconclusive
	Recorded       retestSide  `json:"recorded"`
	Observed       *retestSide `json:"observed,omitempty"`
	Changes        []string    `json:"changes,omitempty"`
	Skipped        string      `json:"skipped,omitempty"`
	Error          string      `json:"error,omitempty"`
	Attached       bool        `json:"attached"`
	AttachError    string      `json:"attachError,omitempty"`
}

type retestSide struct {
	Status    int    `json:"status"`
	BodyBytes int64  `json:"bodyBytes"`
	BodyHash  string `json:"bodyHash,omitempty"`
}

type retestResult struct {
	FindingID   int64               `json:"findingId"`
	Observation string              `json:"observation"`
	Conclusion  string              `json:"conclusion"`
	Replayed    int                 `json:"replayed"`
	Flows       []retestObservation `json:"flows"`
	// Status is the finding's status, unchanged. A retest never moves it.
	Status string `json:"status"`
}

// retestFinding replays the finding's evidence and reports what happened.
func (h *findingsAPI) retestFinding(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad finding id")
		return
	}
	var in retestRequest
	if !decodeOptionalLimitedJSON(w, r, maxRetestRequestBytes, &in) {
		return
	}
	if in.Session != "" && in.Session != "flow" && in.Session != "current" {
		httpErr(w, http.StatusBadRequest, "session must be flow or current")
		return
	}
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	if f == nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	targets, err := retestTargets(f, in.FlowIDs)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(targets) == 0 {
		httpErr(w, http.StatusConflict,
			"no replayable evidence: this finding has no attached flow whose request still exists")
		return
	}

	attach := in.Attach == nil || *in.Attach
	out := retestResult{FindingID: id, Status: f.Status, Flows: []retestObservation{}}
	for _, flowID := range targets {
		obs := h.retestOneFlow(r, flowID, in)
		if obs.Observed != nil && attach && obs.RetestFlowID > 0 {
			h.attachRetestEvidence(id, &obs)
		}
		if obs.Observed != nil {
			out.Replayed++
		}
		out.Flows = append(out.Flows, obs)
	}
	out.Observation, out.Conclusion = summariseRetest(out.Flows)
	h.broadcast(map[string]any{"type": "findings.update"})
	writeJSON(w, http.StatusOK, out)
}

// retestTargets are the evidence flows to replay: the requested ids when given,
// otherwise every attached flow, in attachment order and bounded.
//
// An earlier retest's own flow is excluded from the default set. It is attached
// as evidence, so without this each retest would replay every previous retest as
// well and one click would send more traffic at the target than the last. Naming
// it in flowIds still replays it.
func retestTargets(f *store.Finding, requested []int64) ([]int64, error) {
	fromRetest := map[int64]bool{}
	for _, bl := range f.Blocks {
		if bl.Type == "flow" && bl.Role == "retest" && bl.FlowID > 0 {
			fromRetest[bl.FlowID] = true
		}
	}
	attached := map[int64]bool{}
	var order []int64
	add := func(flowID int64, missing bool) {
		if flowID <= 0 || missing || attached[flowID] {
			return
		}
		attached[flowID] = true
		if fromRetest[flowID] {
			return // still attached evidence, just not retest input
		}
		order = append(order, flowID)
	}
	for _, bl := range f.Blocks {
		if bl.Type == "flow" {
			add(bl.FlowID, bl.Missing)
		}
	}
	for _, fl := range f.Flows {
		add(fl.FlowID, fl.Missing)
	}
	if len(requested) == 0 {
		if len(order) > maxRetestFlows {
			order = order[:maxRetestFlows]
		}
		return order, nil
	}
	var picked []int64
	seen := map[int64]bool{}
	for _, flowID := range requested {
		if !attached[flowID] {
			return nil, fmt.Errorf("flow %d is not attached to this finding as retained evidence", flowID)
		}
		if !seen[flowID] {
			seen[flowID] = true
			picked = append(picked, flowID)
		}
	}
	if len(picked) > maxRetestFlows {
		return nil, fmt.Errorf("at most %d flows per retest", maxRetestFlows)
	}
	return picked, nil
}

// retestOneFlow replays one evidence flow and compares the two responses.
func (h *findingsAPI) retestOneFlow(r *http.Request, flowID int64, in retestRequest) retestObservation {
	obs := retestObservation{EvidenceFlowID: flowID, Observation: "inconclusive"}
	evidence, err := h.st.GetFlow(flowID)
	if err != nil || evidence == nil {
		obs.Error = "the recorded evidence flow is no longer in history"
		return obs
	}
	obs.Request = strings.TrimSpace(evidence.Method + " " + flowURLStr(evidence))
	obs.Recorded = retestSide{Status: evidence.Status, BodyBytes: evidence.ResLen, BodyHash: evidence.ResBodyHash}

	if evidence.Method == "" {
		obs.Skipped = "the recorded flow has no request to replay"
		return obs
	}
	if !idempotentMethods[strings.ToUpper(evidence.Method)] && !in.AllowUnsafeMethods {
		obs.Skipped = evidence.Method + " may change state at the target; pass allowUnsafeMethods to replay it"
		return obs
	}
	url := flowURLStr(evidence)
	if h.targetsOwnListener(url) {
		obs.Skipped = "refusing to send to Interseptor's own listener"
		return obs
	}
	body, err := h.bodyBytesResult(evidence.ReqBodyHash)
	if err != nil {
		obs.Error = "the recorded request body is no longer available, so the replay would not be the same request"
		return obs
	}
	flow, err := h.snd.Send(sender.Request{
		Method:    evidence.Method,
		URL:       url,
		Headers:   evidence.ReqHeaders,
		Body:      body,
		Flags:     store.FlagRepeater | aiSourceFlag(r),
		NoSession: in.Session != "current",
	})
	if err != nil {
		obs.Error = err.Error()
		return obs
	}
	obs.RetestFlowID = flow.ID
	obs.Observed = &retestSide{Status: flow.Status, BodyBytes: flow.ResLen, BodyHash: flow.ResBodyHash}
	if flow.Error != "" {
		obs.Error = flow.Error
		return obs
	}
	obs.Changes = retestChanges(obs.Recorded, *obs.Observed)
	if len(obs.Changes) == 0 {
		obs.Observation = "unchanged"
	} else {
		obs.Observation = "changed"
	}
	return obs
}

// retestChanges lists what differs between the recorded and observed responses.
// A body hash comparison is exact; it is reported as a byte-level difference, not
// as a behavioural one, because a timestamp in a response changes the hash too.
func retestChanges(recorded, observed retestSide) []string {
	var changes []string
	if recorded.Status != observed.Status {
		changes = append(changes, fmt.Sprintf("status %d → %d", recorded.Status, observed.Status))
	}
	if recorded.BodyHash != "" && observed.BodyHash != "" && recorded.BodyHash != observed.BodyHash {
		changes = append(changes, fmt.Sprintf("response body differs (%d → %d bytes)", recorded.BodyBytes, observed.BodyBytes))
	} else if recorded.BodyBytes != observed.BodyBytes {
		changes = append(changes, fmt.Sprintf("response body %d → %d bytes", recorded.BodyBytes, observed.BodyBytes))
	}
	return changes
}

// attachRetestEvidence records the replay as a new retest block on the finding.
// It is an addition: the original evidence and every earlier revision stay as
// they were.
func (h *findingsAPI) attachRetestEvidence(findingID int64, obs *retestObservation) {
	proof := retestProof(*obs)
	err := h.st.AttachFlowWithMetadata(findingID, obs.RetestFlowID,
		"retest replay of flow #"+strconv.FormatInt(obs.EvidenceFlowID, 10), -1,
		"retest", proof, "captured_flow", obs.EvidenceFlowID, findingAPIChange("retest"))
	if err != nil {
		obs.AttachError = err.Error()
		return
	}
	obs.Attached = true
}

// retestProof states what this replay establishes, and no more.
func retestProof(obs retestObservation) string {
	if obs.Observation == "unchanged" {
		return fmt.Sprintf("Retest of flow #%d: same status %d and an identical body. The behaviour has not changed.",
			obs.EvidenceFlowID, obs.Recorded.Status)
	}
	return fmt.Sprintf("Retest of flow #%d: %s. One differing response is not proof of remediation.",
		obs.EvidenceFlowID, strings.Join(obs.Changes, "; "))
}

// summariseRetest turns the per-flow observations into one verdict and one
// sentence. The wording is deliberate: a retest never reports a fix.
func summariseRetest(flows []retestObservation) (string, string) {
	var unchanged, changed, inconclusive int
	var changeNotes, blockers []string
	for _, f := range flows {
		switch f.Observation {
		case "unchanged":
			unchanged++
		case "changed":
			changed++
			changeNotes = append(changeNotes, fmt.Sprintf("flow #%d: %s", f.EvidenceFlowID, strings.Join(f.Changes, "; ")))
		default:
			inconclusive++
			reason := f.Skipped
			if reason == "" {
				reason = f.Error
			}
			if reason == "" {
				reason = "no response was observed"
			}
			blockers = append(blockers, fmt.Sprintf("flow #%d: %s", f.EvidenceFlowID, reason))
		}
	}
	sort.Strings(blockers)
	switch {
	case changed > 0:
		return "changed", "The replayed evidence no longer behaves as recorded — " + strings.Join(changeNotes, "; ") +
			". A differing response is not proof of remediation: it may reflect a different session, a changed precondition, " +
			"a rate limit or an unrelated deployment. Review the attached retest flow and re-run under the original " +
			"preconditions before changing this finding's status."
	case unchanged > 0 && inconclusive == 0:
		return "unchanged", "Every replayed request returned the same status and an identical response body as the " +
			"recorded evidence, so the observed behaviour has not changed. This is a replay result, not a fresh " +
			"verification of the claimed impact."
	case unchanged > 0:
		return "inconclusive", "Part of the evidence replayed unchanged, but the rest could not be replayed (" +
			strings.Join(blockers, "; ") + "), so the finding's current state is not established."
	default:
		return "inconclusive", "Nothing could be replayed (" + strings.Join(blockers, "; ") +
			"), so nothing is established about the current behaviour."
	}
}
