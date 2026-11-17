package verify

import (
	"context"
	"encoding/json"
	"strings"
)

// Severity ranks a candidate's impact. It drives whether Gate 4 (human confirm)
// applies: only High and Critical candidates require a human touchpoint before
// filing; Medium/Low/Info auto-file once the machine gates pass.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// ParseSeverity maps a case-insensitive severity name to a Severity. Unknown or
// empty strings fall back to SeverityInfo (the safest default: it never triggers
// the human gate on its own, but such a candidate still needs Gates 1-2).
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "medium", "med":
		return SeverityMedium
	case "low":
		return SeverityLow
	default:
		return SeverityInfo
	}
}

// String renders the canonical lowercase name.
func (s Severity) String() string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	case SeverityLow:
		return "low"
	default:
		return "info"
	}
}

// Candidate is everything the verifier needs to prove one candidate vulnerability.
// It carries the class-specific differential (Gate 1) and, for blind classes, the
// OOB spec (Gate 3), plus enough human-readable context for the agent and human
// gates. A Candidate is never a Finding: it only becomes one if Verify proves it.
type Candidate struct {
	VulnClass string   // e.g. "sqli-boolean","xss-reflected","ssrf-blind","cmdi-time"
	Severity  Severity // drives whether Gate 4 applies
	Target    string   // url/endpoint
	Point     string   // injection point description
	Blind     bool     // requires OOB proof (Gate 3)
	Diff      DiffSpec // for Gate 1
	OOB       *OOBSpec // for Gate 3 when Blind; nil for non-blind classes
	Summary   string   // human-readable candidate description for the agent/human gates
}

// Verdict is Gate 2's answer: an adversarial verifier agent told to *disprove* the
// candidate. Result is one of "real" | "refuted" | "uncertain"; anything but
// "real" rejects.
type Verdict struct {
	Result    string // real | refuted | uncertain
	Reasoning string
}

// real reports whether the verdict clears Gate 2.
func (v Verdict) real() bool {
	return strings.EqualFold(strings.TrimSpace(v.Result), "real")
}

// AgentVerifier is Gate 2: an adversarial verifier agent, given the candidate plus
// the Gate-1 differential evidence and told to disprove it. The concrete impl is
// built in Phase 2 over internal/aiagent; tests inject a scripted fake.
type AgentVerifier interface {
	Disprove(ctx context.Context, c Candidate, evidence DiffResult) Verdict
}

// ConfirmResult is Gate 4's answer: a human's one-click decision for Critical/High
// candidates before filing.
type ConfirmResult struct {
	Confirmed bool
	By        string
	Note      string
}

// Confirmer is Gate 4: a human confirm for Critical/High candidates. The concrete
// implementation is injected by the caller; tests inject a fake.
type Confirmer interface {
	Confirm(ctx context.Context, c Candidate, proof Proof) ConfirmResult
}

// Gate name constants — the values RejectedAt takes and the keys of the Gates map.
const (
	GateDifferential = "differential" // Gate 1
	GateAgent        = "agent"        // Gate 2
	GateOOB          = "oob"          // Gate 3
	GateHuman        = "human"        // Gate 4
)

// Proof is the machine proof-record the verifier emits for one candidate. It is
// the source Phase 2 serializes into a store.FindingVerification row (via
// ToVerification / Verification): Proven says whether the candidate cleared every
// applicable gate; RejectedAt names the first gate that failed (empty when
// proven); Gates carries per-gate detail JSON-serializable straight into
// finding_verification.gates.
type Proof struct {
	Proven       bool
	RejectedAt   string // "" if proven, else GateDifferential|GateAgent|GateOOB|GateHuman
	ReproCount   int
	OOBToken     string
	BaselineFlow int64
	PayloadFlow  int64
	Confidence   int            // 0-100, derived from which gates a class required AND passed
	Gates        map[string]any // per-gate detail for finding_verification.gates
	AgentVerdict Verdict
	HumanConfirm ConfirmResult
}

// VerifyDeps injects the four gate collaborators. Sender/OOB drive the two
// deterministic primitives (Gate 1 / Gate 3); Agent/Human are the two LLM/human
// gates. The OOB poll cadence is injected on the Candidate's OOBSpec (its sleep
// hook), so no separate clock is needed here — Verify itself never sleeps.
type VerifyDeps struct {
	Sender Sender
	OOB    OOBPoller
	Agent  AgentVerifier
	Human  Confirmer
}

// Verify runs the 4-gate verifier over one candidate in cost order. Any gate that
// fails rejects immediately: Verify returns Proven=false with RejectedAt naming
// that gate, so an unproven candidate never becomes a Finding. Gates run in this
// order (later gates only run if all earlier ones held):
//
//   - Gate 1 (always): differential reproduction. Not reproduced ⇒ reject.
//   - Gate 2 (always): adversarial agent. Verdict != "real" ⇒ reject.
//   - Gate 3 (only if Candidate.Blind): OOB callback. Not confirmed ⇒ reject.
//   - Gate 4 (only if Severity >= High): human confirm. Declined ⇒ reject.
//
// Confidence is derived from which gates the candidate required and passed (see
// confidence()). ctx cancellation surfaces as the current gate failing (the
// underlying primitives return not-reproduced / not-confirmed on a cancelled ctx).
func Verify(ctx context.Context, c Candidate, d VerifyDeps) Proof {
	p := Proof{Gates: map[string]any{}}

	// Gate 1 — differential reproduction (always).
	diff := ReproduceDifferential(ctx, d.Sender, c.Diff)
	p.ReproCount = diff.Times
	if len(diff.Baseline) > 0 {
		p.BaselineFlow = diff.Baseline[0]
	}
	if len(diff.PayloadFlows) > 0 {
		p.PayloadFlow = diff.PayloadFlows[0]
	}
	p.Gates[GateDifferential] = map[string]any{
		"reproN":     diff.Times,
		"expected":   string(c.Diff.Class),
		"reproduced": diff.Reproduced,
		"detail":     diff.Detail,
	}
	if !diff.Reproduced {
		return reject(p, GateDifferential, c)
	}

	// Gate 2 — adversarial verifier agent (always).
	verdict := d.Agent.Disprove(ctx, c, diff)
	p.AgentVerdict = verdict
	p.Gates[GateAgent] = map[string]any{
		"verdict":   verdict.Result,
		"reasoning": verdict.Reasoning,
	}
	if !verdict.real() {
		return reject(p, GateAgent, c)
	}

	// Gate 3 — OOB proof (only for blind classes).
	if c.Blind {
		var oob OOBResult
		if c.OOB != nil {
			oob = ConfirmOOB(ctx, d.Sender, d.OOB, *c.OOB)
		}
		p.OOBToken = oob.Token
		p.Gates[GateOOB] = map[string]any{
			"token":     oob.Token,
			"confirmed": oob.Confirmed,
			"polls":     oob.Polls,
			"probeFlow": oob.ProbeFlow,
			"detail":    oob.Detail,
		}
		if !oob.Confirmed {
			return reject(p, GateOOB, c)
		}
	}

	// Gate 4 — human confirm (only for Critical/High). Medium/Low/Info skip it.
	if c.Severity >= SeverityHigh {
		hc := d.Human.Confirm(ctx, c, p)
		p.HumanConfirm = hc
		p.Gates[GateHuman] = map[string]any{
			"confirmed":  hc.Confirmed,
			"answeredBy": hc.By,
			"note":       hc.Note,
		}
		if !hc.Confirmed {
			return reject(p, GateHuman, c)
		}
	}

	p.Proven = true
	p.Confidence = confidence(c, "")
	return p
}

// reject finalizes a rejected proof: Proven stays false, RejectedAt names the gate,
// and Confidence reflects the (partial) gates that held before the failure.
func reject(p Proof, gate string, c Candidate) Proof {
	p.Proven = false
	p.RejectedAt = gate
	p.Confidence = confidence(c, gate)
	return p
}

// Gate weights for the confidence score. The deterministic ground-truth gates
// (differential, OOB) carry the most weight; the adversarial agent is a strong but
// LLM-based check; the human confirm is a light final sign-off.
const (
	wDiff  = 40
	wAgent = 25
	wOOB   = 25
	wHuman = 10
)

// confidence derives a 0-100 score from which gates a candidate *required* and how
// far it got. The applicable gates depend on the candidate's shape:
//
//	Gate 1 differential .............. 40  (always)
//	Gate 2 adversarial agent ......... 25  (always)
//	Gate 3 OOB callback .............. 25  (only if Blind)
//	Gate 4 human confirm ............. 10  (only if Severity >= High)
//
// The score is the sum of the weights of the gates that *held*, divided by the sum
// of the weights of the gates that *apply* to this candidate's shape, ×100. So a
// fully proven candidate always lands at 100 regardless of which gates applied:
//
//   - reflected/error/boolean/timing (non-blind, <High): Gates 1+2 → 100.
//   - blind (e.g. ssrf-blind, <High): Gates 1+2+3 → 100.
//   - High/Critical non-blind: Gates 1+2+4 → 100.
//   - High/Critical blind: Gates 1+2+3+4 → 100.
//
// rejectedAt names the gate that failed ("" ⇒ proven, all applicable gates held).
// A rejected candidate scores only the gates that held *before* the failing gate,
// as a fraction of that same reachable maximum — so a candidate killed at Gate 1
// scores 0, and one killed at the human gate (after 1+2+3 held) scores high but
// under 100: a "how close did it get" signal for triage.
func confidence(c Candidate, rejectedAt string) int {
	// max = weights of the gates that apply to this candidate's shape.
	max := wDiff + wAgent
	if c.Blind {
		max += wOOB
	}
	if c.Severity >= SeverityHigh {
		max += wHuman
	}
	if max == 0 { // unreachable (wDiff+wAgent > 0), defensive.
		return 0
	}

	// earned = weights of the gates that held before the rejection (all applicable
	// gates when proven).
	earned := 0
	if rejectedAt != GateDifferential {
		earned += wDiff
	}
	if rejectedAt != GateDifferential && rejectedAt != GateAgent {
		earned += wAgent
	}
	if c.Blind && rejectedAt != GateDifferential && rejectedAt != GateAgent && rejectedAt != GateOOB {
		earned += wOOB
	}
	if c.Severity >= SeverityHigh && rejectedAt == "" {
		earned += wHuman
	}

	return earned * 100 / max
}

// Verification mirrors the persistable fields of store.FindingVerification without
// importing internal/store (keeping this package free of that coupling). Phase 2
// maps it 1:1 onto a store.FindingVerification row. Gates is the JSON-encoded
// per-gate detail (Proof.Gates marshaled); the numeric/string fields are copied
// straight across.
type Verification struct {
	FindingID    int64
	RunID        int64
	VulnClass    string
	Gates        string // JSON of Proof.Gates
	ReproCount   int
	OOBToken     string
	BaselineFlow int64
	PayloadFlow  int64
	Confidence   int
}

// gatesJSON marshals Proof.Gates to a JSON object string. Proof.Gates only ever
// holds string/number/bool leaves inside nested maps (all JSON-safe), so marshal
// cannot realistically fail; on the impossible error it yields "{}" so a caller
// never persists a malformed column.
func (p Proof) gatesJSON() string {
	g := p.Gates
	if g == nil {
		g = map[string]any{}
	}
	b, err := json.Marshal(g)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Verification builds the persistable proof-record fields for this proof. It does
// not import internal/store; Phase 2 constructs the store row from the returned
// struct. oobToken overrides the proof's recorded token when non-empty (the caller
// may know the run-scoped token); otherwise Proof.OOBToken is used.
func (p Proof) Verification(findingID, runID int64, vulnClass, oobToken string) Verification {
	token := oobToken
	if token == "" {
		token = p.OOBToken
	}
	return Verification{
		FindingID:    findingID,
		RunID:        runID,
		VulnClass:    vulnClass,
		Gates:        p.gatesJSON(),
		ReproCount:   p.ReproCount,
		OOBToken:     token,
		BaselineFlow: p.BaselineFlow,
		PayloadFlow:  p.PayloadFlow,
		Confidence:   p.Confidence,
	}
}

// ToVerification is the map form of Verification, for callers that serialize the
// proof-record generically (e.g. straight into a JSON body or a generic store
// upsert). Keys match store.FindingVerification's JSON tags so Phase 2 can decode
// them into the struct without a manual field map.
func (p Proof) ToVerification(findingID, runID int64, vulnClass, oobToken string) map[string]any {
	v := p.Verification(findingID, runID, vulnClass, oobToken)
	return map[string]any{
		"findingId":    v.FindingID,
		"runId":        v.RunID,
		"vulnClass":    v.VulnClass,
		"gates":        v.Gates,
		"reproCount":   v.ReproCount,
		"oobToken":     v.OOBToken,
		"baselineFlow": v.BaselineFlow,
		"payloadFlow":  v.PayloadFlow,
		"confidence":   v.Confidence,
	}
}
