package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/preview"
)

type intruderStartJSON struct {
	Target       string     `json:"target"`
	Template     string     `json:"template"`
	AttackType   string     `json:"attackType"`
	Payloads     [][]string `json:"payloads"`
	Repeat       int        `json:"repeat"`
	Threads      int        `json:"threads"`
	DelayMs      int        `json:"delayMs"`
	GrepMatch    string     `json:"grepMatch"`
	GrepExtract  string     `json:"grepExtract"`
	ProcessRules []string   `json:"processRules"`
	Barrier      bool       `json:"barrier"`
}

func (h *toolsAPI) intruderStart(w http.ResponseWriter, r *http.Request) {
	var in intruderStartJSON
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	if h.targetsOwnListener(in.Target) {
		httpErr(w, http.StatusForbidden, "refusing to attack Interseptor's own listener")
		return
	}
	if in.Threads <= 0 {
		httpErr(w, http.StatusBadRequest, "threads must be a positive number")
		return
	}
	err := h.intr.Start(intruder.Spec{
		Target:       in.Target,
		Template:     in.Template,
		AttackType:   in.AttackType,
		Payloads:     in.Payloads,
		Repeat:       in.Repeat,
		Threads:      in.Threads,
		DelayMs:      in.DelayMs,
		GrepMatch:    in.GrepMatch,
		GrepExtract:  in.GrepExtract,
		ProcessRules: in.ProcessRules,
		ExtraFlags:   aiSourceFlag(r),
		Barrier:      in.Barrier,
	})
	if err != nil {
		if errors.Is(err, intruder.ErrClosed) {
			httpErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.intr.State())
}

func (h *toolsAPI) intruderState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.intr.State())
}

func (h *toolsAPI) intruderStop(w http.ResponseWriter, r *http.Request) {
	if h.intr != nil {
		h.intr.Stop()
		writeJSON(w, http.StatusOK, h.intr.State())
		return
	}
	httpErr(w, http.StatusServiceUnavailable, "intruder engine unavailable")
}

// intruderRunEnvelope is the JSON stored per finished run. The top-level
// startedTs/finishedTs/attack fields feed the store's listing columns.
type intruderRunEnvelope struct {
	RunID      string               `json:"runId"`
	StartedTS  int64                `json:"startedTs"`
	FinishedTS int64                `json:"finishedTs"`
	Attack     string               `json:"attack"`
	State      intruder.State       `json:"state"`
	Spec       intruder.SpecSummary `json:"spec"`
}

func newIntruderEnvelope(rec intruder.RunRecord) (intruderRunEnvelope, bool) {
	if rec.State.RunID == "" {
		return intruderRunEnvelope{}, false
	}
	attack := rec.State.Attack
	if attack == "" {
		attack = rec.Spec.Attack
	}
	return intruderRunEnvelope{
		RunID: rec.State.RunID, StartedTS: rec.State.StartedTs, FinishedTS: time.Now().UnixMilli(),
		Attack: attack, State: rec.State, Spec: rec.Spec,
	}, true
}

// loadIntruderRun resolves an attack id (or the "latest" alias) to its stored
// record. It returns the real run id so provenance never records an alias.
func (e *evidenceAPI) loadIntruderRun(id string) (intruderRunEnvelope, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return intruderRunEnvelope{}, "", evErr(http.StatusBadRequest, "attack id is required")
	}
	if id == latestRunAlias {
		metas := e.st.ListIntruderRuns(1)
		if len(metas) == 0 {
			return intruderRunEnvelope{}, "", evErr(http.StatusNotFound, "no finished Intruder runs recorded yet")
		}
		id = metas[0].RunID
	}
	raw, ok, err := e.st.GetIntruderRun(id)
	if err != nil {
		return intruderRunEnvelope{}, "", err
	}
	if !ok {
		return intruderRunEnvelope{}, "", evErr(http.StatusNotFound, "intruder attack not found")
	}
	var env intruderRunEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return intruderRunEnvelope{}, "", err
	}
	env.RunID = id
	return env, id, nil
}

// GET /api/intruder/attacks
func (e *evidenceAPI) intruderAttacks(w http.ResponseWriter, r *http.Request) {
	metas := e.st.ListIntruderRuns(0)
	latest := ""
	if len(metas) > 0 {
		latest = metas[0].RunID
	}
	writeJSON(w, http.StatusOK, map[string]any{"attacks": metas, "latest": latest})
}

// GET /api/intruder/attacks/{id}
func (e *evidenceAPI) intruderAttack(w http.ResponseWriter, r *http.Request) {
	env, _, err := e.loadIntruderRun(r.PathValue("id"))
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

// GET /api/intruder/attacks/{id}/render.png?kind=timeline|distribution|race|strip&width=&mask=&format=json
func (e *evidenceAPI) intruderAttackRender(w http.ResponseWriter, r *http.Request) {
	kind, ok := canonicalEvidenceKind(orVal(r.URL.Query().Get("kind"), "timeline"))
	if !ok || !isIntruderKind(kind) {
		httpErr(w, http.StatusBadRequest, "kind must be timeline, distribution, race or strip")
		return
	}
	width, mask, err := widthAndMask(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	expected, err := parseExpectedParam(r.URL.Query().Get("expected"))
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	id := r.PathValue("id")
	e.serveRender(w, r, evidenceRequest{Kind: kind, RunID: id, Width: width, Mask: mask, Expected: expected}, "intruder-"+id+"-"+r.URL.Query().Get("kind"))
}

func (env intruderRunEnvelope) attack() string {
	return orVal(env.State.Attack, env.Spec.Attack)
}

func (env intruderRunEnvelope) threads() int {
	if env.State.Threads > 0 {
		return env.State.Threads
	}
	return env.Spec.Threads
}

func resultSeq(r intruder.Result) int {
	if r.Seq > 0 {
		return r.Seq
	}
	return r.ID
}

func intruderTimelineInput(env intruderRunEnvelope) preview.TimelineInput {
	in := preview.TimelineInput{
		RunID: clipText(env.RunID, maxEvidenceRunIDRunes), Attack: clipText(env.attack(), maxEvidenceNameRunes), Threads: env.threads(),
		DelayMs: max(env.State.DelayMs, env.Spec.DelayMs),
		Target:  evidenceText(orVal(env.State.TargetHost, env.Spec.Target), maxEvidenceNameRunes*2), Capped: env.State.Capped,
		Method: clipText(env.Spec.Method, 16), Path: evidenceText(env.Spec.Path, maxEvidenceURLRunes),
		StartedUnixMs: env.State.StartedTs,
	}
	for _, r := range env.State.Results {
		row := preview.TimelineRow{
			Seq: resultSeq(r), Worker: r.Worker, StartUs: r.StartUs, EndUs: r.EndUs,
			Status: r.Status, Length: int(r.Length), Error: r.Error != "", Flagged: r.Flagged,
			Matched: r.Matched,
		}
		if len(r.RLHeaders) > 0 {
			row.RLHeaders = make(map[string]string, len(r.RLHeaders))
			for k, v := range r.RLHeaders {
				row.RLHeaders[clipText(k, maxEvidenceNameRunes)] = redactHeaderValue(k, v)
			}
		}
		in.Rows = append(in.Rows, row)
	}
	return in
}

func intruderDistributionInput(env intruderRunEnvelope) preview.DistributionInput {
	in := preview.DistributionInput{RunID: clipText(env.RunID, maxEvidenceRunIDRunes)}
	for _, r := range env.State.Results {
		in.Rows = append(in.Rows, preview.DistRow{
			Seq: resultSeq(r), Status: r.Status, Length: int(r.Length), TimeMs: int(r.TimeMs),
			Flagged: r.Flagged, Anomaly: r.Anomaly, Matched: r.Matched, BodyHash: clipText(r.BodyHash, 64),
		})
	}
	return in
}

// intruderRaceInput adapts a run for the race view. Extracted values are the
// proof a race finding needs but are often tokens or coupon codes, so they are
// shown as length plus digest unless the caller unmasks. expected is the
// optional baseline of requests the application should have accepted.
func intruderRaceInput(env intruderRunEnvelope, mask bool, expected int) preview.RaceInput {
	in := preview.RaceInput{
		RunID: clipText(env.RunID, maxEvidenceRunIDRunes), Threads: env.threads(), Barrier: env.State.Barrier,
		SuccessLabel: redactEvidenceText(env.Spec.GrepMatch), ExpectedMax: expected,
	}
	for _, r := range env.State.Results {
		row := preview.RaceRow{
			Seq: resultSeq(r), Worker: r.Worker, StartUs: r.StartUs, EndUs: r.EndUs,
			Status: r.Status, Length: int(r.Length), BodyHash: clipText(r.BodyHash, 64), Matched: r.Matched,
		}
		if r.Extracted != "" {
			v := clipText(r.Extracted, maxEvidenceValueRunes)
			if mask {
				v = maskedValue(v)
			}
			row.Extracted = []string{v}
		}
		in.Rows = append(in.Rows, row)
	}
	return in
}

// intruderStripInput adapts a run for the payload heat strip. Payloads are
// masked by default (brute-force and credential-stuffing payloads are the
// credentials); mask=false is an explicit opt-in.
func intruderStripInput(env intruderRunEnvelope, mask bool) preview.StripInput {
	in := preview.StripInput{RunID: clipText(env.RunID, maxEvidenceRunIDRunes), Attack: clipText(env.attack(), maxEvidenceNameRunes), Mask: mask, Premasked: mask}
	for _, r := range env.State.Results {
		payload := clipText(r.Payload, maxEvidenceValueRunes)
		if mask {
			payload = maskedValue(payload)
		}
		in.Rows = append(in.Rows, preview.StripRow{
			Seq: resultSeq(r), Payload: payload, Status: r.Status,
			Length: int(r.Length), TimeMs: int(r.TimeMs), Matched: r.Matched, Anomaly: r.Anomaly,
		})
	}
	return in
}
