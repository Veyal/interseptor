package collmatrix

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The flow matrix is the captured-flow (History) access-control surface running
// through this one engine: it replays selected captured flows under each
// identity and builds the same Matrix the collection identity matrix produces,
// with the same classification, differential, evidence contract and renderer.
// The replay transport lives in the control layer (it owns the sender and the
// scope/own-listener guards); this package stays transport-free.

var (
	errNoFlows    = errors.New("collmatrix: no flows selected")
	errNoReplayer = errors.New("collmatrix: flow replay is not configured")
)

// FlowTarget identifies one captured flow to replay.
type FlowTarget struct {
	FlowID int64
	Method string
	Host   string
	Path   string
	Name   string // display label; method+path when empty
}

// ReplayResult is the outcome of replaying a FlowTarget under one identity.
type ReplayResult struct {
	Status     int
	Size       int64
	DurationMs int64
	BodyHash   string // comparable (decoded) response body hash, for sameness
	FlowID     int64  // the newly captured replay flow
	Error      string
	Location   string // response Location header, for login-redirect classification
}

// FlowReplayer resolves captured flows and replays them under an identity. The
// control layer implements it over the sender; the identity's credential
// headers replace the captured request's so each replay carries exactly that
// identity (anonymous strips auth).
type FlowReplayer interface {
	Targets(flowIDs []int64) ([]FlowTarget, error)
	Replay(ctx context.Context, target FlowTarget, id Identity) ReplayResult
}

// FlowMatrixRequest selects captured flows and the identities to replay them as.
type FlowMatrixRequest struct {
	FlowIDs    []int64  `json:"flowIds"`
	Identities []string `json:"identities,omitempty"` // empty: all saved + anonymous
	Baseline   string   `json:"baseline,omitempty"`
}

// RunFlowMatrix replays each selected flow under every identity and builds the
// access differential, using the same engine as the collection identity matrix.
func (s *Service) RunFlowMatrix(ctx context.Context, rp FlowReplayer, req FlowMatrixRequest) (*Matrix, error) {
	if rp == nil {
		return nil, errNoReplayer
	}
	if len(req.FlowIDs) == 0 {
		return nil, errNoFlows
	}
	run, _, skipped, err := s.resolveIdentities(MatrixRequest{Identities: req.Identities})
	if err != nil {
		return nil, err
	}
	targets, err := rp.Targets(req.FlowIDs)
	if err != nil {
		return nil, err
	}
	m := &Matrix{
		ID: newID(), StartedMs: s.d.Now().UnixMilli(),
		Baseline: pickBaseline(run, req.Baseline), Skipped: skipped, Runs: map[string]string{}, Expect: map[string]string{},
	}
	for _, id := range run {
		m.Identities = append(m.Identities, id.Name)
	}
	for _, tg := range targets {
		if ctx.Err() != nil {
			m.Partial = true
			break
		}
		if len(m.Rows) >= MaxRows {
			m.Warnings = append(m.Warnings, fmt.Sprintf("more than %d flows; extra rows dropped", MaxRows))
			break
		}
		cells := make([]Cell, len(run))
		for ci, id := range run {
			if ctx.Err() != nil {
				m.Partial = true
				break
			}
			cells[ci] = s.cellOfReplay(rp.Replay(ctx, tg, id), !id.Anonymous)
		}
		label := tg.Name
		if label == "" {
			label = strings.TrimSpace(tg.Method + " " + tg.Path)
		}
		m.Rows = append(m.Rows, Row{
			ItemUID: fmt.Sprintf("flow-%d", tg.FlowID), Name: s.scrub(label),
			Method: tg.Method, URL: s.scrub(tg.Path), Cells: cells,
		})
	}
	s.finalize(m, run)
	m.FinishedMs = s.d.Now().UnixMilli()
	s.remember(m)
	return m, nil
}

// cellOfReplay classifies a replay result into a Cell, exactly as cellOf does
// for a collection step.
func (s *Service) cellOfReplay(rr ReplayResult, hasAuth bool) Cell {
	c := Cell{Status: rr.Status, Size: rr.Size, DurationMs: rr.DurationMs, FlowID: rr.FlowID, Error: s.scrub(rr.Error), bodyHash: rr.BodyHash}
	if rr.Error != "" {
		c.Class = ClassError
		return c
	}
	c.Class = classify(rr.Status, hasAuth, rr.Location)
	c.Denied = c.Class == ClassAuthFailure || c.Class == ClassAuthzFailure
	return c
}
