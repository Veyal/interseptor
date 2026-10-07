package collmatrix

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// Outcome classes of one cell. They match the authz differential classes so
// the two features read the same.
const (
	ClassSuccess      = "success"
	ClassAuthFailure  = "auth_failure"
	ClassAuthzFailure = "authz_failure"
	ClassValidation   = "validation_failure"
	ClassOther        = "other"
	ClassError        = "error"
	ClassBlocked      = "blocked"
	ClassNotRun       = "not_run"
)

// Flags raised on a cell against the identity's expectation.
const (
	// FlagViolation: an identity expected to be denied got a success. A
	// hypothesis (candidate BOLA/BFLA or missing auth), not proof.
	FlagViolation = "violation"
	// FlagUnexpectedDenial: an identity expected to be allowed was denied.
	FlagUnexpectedDenial = "unexpected_denial"
)

// Bounds.
const (
	MaxIdentities = 12
	MaxRows       = 200
)

// MatrixRequest selects what to run and as whom.
type MatrixRequest struct {
	CollectionUID string   `json:"collectionUid"`
	FolderUID     string   `json:"folderUid,omitempty"`
	ItemUIDs      []string `json:"itemUids,omitempty"`
	EnvUID        string   `json:"envUid,omitempty"`
	// Identities names the identities to run as. Empty means every saved
	// identity plus anonymous. "anonymous" is always available by name.
	Identities []string `json:"identities,omitempty"`
	// Baseline is the identity the others are compared with. Default: the
	// first non-anonymous identity.
	Baseline    string            `json:"baseline,omitempty"`
	NoScripts   bool              `json:"noScripts,omitempty"`
	ScopePolicy string            `json:"scopePolicy,omitempty"` // default block
	Local       map[string]string `json:"-"`
	Source      collexec.Source   `json:"-"`
	AI          bool              `json:"-"`
	// OnProgress is called after each identity finished (done of total).
	OnProgress func(identity string, done, total int) `json:"-"`
}

// Cell is one identity's result for one request.
type Cell struct {
	Class          string `json:"class"`
	Status         int    `json:"status,omitempty"`
	Size           int64  `json:"size,omitempty"`
	DurationMs     int64  `json:"durationMs,omitempty"`
	FlowID         int64  `json:"flowId,omitempty"`
	SameAsBaseline bool   `json:"sameAsBaseline"`
	Denied         bool   `json:"denied,omitempty"`
	Flag           string `json:"flag,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
	bodyHash       string
}

// Row is one request across every identity.
type Row struct {
	ItemUID string `json:"itemUid"`
	Name    string `json:"name"`
	Method  string `json:"method,omitempty"`
	URL     string `json:"url,omitempty"`
	Cells   []Cell `json:"cells"` // same order as Matrix.Identities
}

// Summary counts the matrix.
type Summary struct {
	Identities        int `json:"identities"`
	Rows              int `json:"rows"`
	Cells             int `json:"cells"`
	Violations        int `json:"violations"`
	UnexpectedDenials int `json:"unexpectedDenials"`
	Differs           int `json:"differs"`
	Blocked           int `json:"blocked"`
	Errors            int `json:"errors"`
}

// Matrix is the identity-by-request differential.
type Matrix struct {
	ID            string            `json:"id"`
	CollectionUID string            `json:"collectionUid"`
	Collection    string            `json:"collection"`
	EnvUID        string            `json:"envUid,omitempty"`
	StartedMs     int64             `json:"startedMs"`
	FinishedMs    int64             `json:"finishedMs"`
	Baseline      string            `json:"baseline"`
	Identities    []string          `json:"identities"`
	Expect        map[string]string `json:"expect,omitempty"` // identity -> allow|deny (resolved)
	Rows          []Row             `json:"rows"`
	Summary       Summary           `json:"summary"`
	Runs          map[string]string `json:"runs,omitempty"` // identity -> run uid
	Skipped       []string          `json:"skipped,omitempty"`
	Warnings      []string          `json:"warnings,omitempty"`
	Partial       bool              `json:"partial,omitempty"`
	// Hypotheses are inferences, not proof; reproduce before reporting.
	Hypotheses []string `json:"hypotheses,omitempty"`
}

var (
	errNoCollection = errors.New("collmatrix: collectionUid is required")
	errNoIdentity   = errors.New("collmatrix: no identity to run as")
)

// classify buckets one response, as the authz differential does: a 403
// without credentials is an authentication problem, with credentials an
// authorization one; a redirect to a login page is an authentication failure.
func classify(status int, hasAuth bool, location string) string {
	switch {
	case status == 401:
		return ClassAuthFailure
	case status == 403:
		if hasAuth {
			return ClassAuthzFailure
		}
		return ClassAuthFailure
	case status >= 300 && status < 400 && looksLikeLogin(location):
		return ClassAuthFailure
	case status == 400, status == 411, status == 413, status == 415, status == 422:
		return ClassValidation
	case status >= 200 && status < 300:
		return ClassSuccess
	}
	return ClassOther
}

func looksLikeLogin(loc string) bool {
	l := strings.ToLower(loc)
	for _, w := range []string{"login", "signin", "sign-in", "sso", "oauth", "/auth", "authorize"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// resolveIdentities picks the runnable identities in order, always ending with
// anonymous unless the caller listed identities without it.
func (s *Service) resolveIdentities(req MatrixRequest) (run, known []Identity, skipped []string, err error) {
	var saved []Identity
	if s.d.Identities != nil {
		saved = s.d.Identities.Identities()
	}
	byName := map[string]Identity{}
	for _, id := range saved {
		byName[strings.ToLower(id.Name)] = id
	}
	anon := Identity{Name: AnonymousName, Anonymous: true, Expect: ExpectDeny}
	byName[AnonymousName] = anon
	known = saved
	want := req.Identities
	if len(want) == 0 {
		for _, id := range saved {
			want = append(want, id.Name)
		}
		want = append(want, AnonymousName)
	}
	seen := map[string]bool{}
	for _, n := range want {
		k := strings.ToLower(strings.TrimSpace(n))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		id, ok := byName[k]
		if !ok {
			return nil, nil, nil, fmt.Errorf("collmatrix: unknown identity %q", n)
		}
		if id.Broken {
			skipped = append(skipped, id.Name)
			continue
		}
		if !id.Anonymous && len(id.Headers) == 0 {
			skipped = append(skipped, id.Name)
			continue
		}
		run = append(run, id)
	}
	if len(run) == 0 {
		return nil, nil, skipped, errNoIdentity
	}
	if len(run) > MaxIdentities {
		run = run[:MaxIdentities]
	}
	return run, known, skipped, nil
}

func pickBaseline(run []Identity, want string) string {
	for _, id := range run {
		if want != "" && strings.EqualFold(id.Name, want) {
			return id.Name
		}
	}
	for _, id := range run {
		if !id.Anonymous {
			return id.Name
		}
	}
	return run[0].Name
}

// RunMatrix runs the selected requests once per identity, sequentially, and
// builds the differential. Runs use the scope policy block by default and
// discard script variable writes; every send passes the collexec scope guard.
func (s *Service) RunMatrix(ctx context.Context, req MatrixRequest) (*Matrix, error) {
	if strings.TrimSpace(req.CollectionUID) == "" {
		return nil, errNoCollection
	}
	run, known, skipped, err := s.resolveIdentities(req)
	if err != nil {
		return nil, err
	}
	m := &Matrix{
		ID: newID(), CollectionUID: req.CollectionUID, EnvUID: req.EnvUID, StartedMs: s.d.Now().UnixMilli(),
		Baseline: pickBaseline(run, req.Baseline), Skipped: skipped, Runs: map[string]string{}, Expect: map[string]string{},
	}
	for _, id := range run {
		m.Identities = append(m.Identities, id.Name)
	}
	reports := make([]*collrun.Report, len(run))
	for i, id := range run {
		if ctx.Err() != nil {
			m.Partial = true
			break
		}
		rep, rerr := s.runAs(ctx, req, id, known)
		if rerr != nil {
			return nil, rerr
		}
		reports[i] = rep
		m.Runs[id.Name] = rep.RunUID
		if m.Collection == "" {
			m.Collection = s.scrub(rep.CollectionName)
		}
		if rep.Status != collrun.StatusDone && rep.Status != collrun.StatusStopped {
			m.Warnings = append(m.Warnings, s.scrub(fmt.Sprintf("%s: run ended %s %s", id.Name, rep.Status, rep.StopReason)))
			if rep.Status == collrun.StatusAborted {
				m.Partial = true
			}
		}
		if req.OnProgress != nil {
			req.OnProgress(id.Name, i+1, len(run))
		}
	}
	s.assemble(m, run, reports)
	m.FinishedMs = s.d.Now().UnixMilli()
	s.remember(m)
	return m, nil
}

func (s *Service) runAs(ctx context.Context, req MatrixRequest, id Identity, known []Identity) (*collrun.Report, error) {
	pol := req.ScopePolicy
	if pol == "" {
		pol = store.ScopePolicyBlock
	}
	src := req.Source
	if src == "" {
		src = collexec.SourceRunner
	}
	r := collrun.New(newIdentityBackend(s.d.Backend, id, known), s.d.Runs)
	return r.Run(ctx, collrun.Options{
		CollectionUID: req.CollectionUID, FolderUID: req.FolderUID, ItemUIDs: req.ItemUIDs, EnvUID: req.EnvUID,
		Iterations: 1, Persist: collrun.PersistDiscard, Bail: collrun.BailNone,
		NoScripts: req.NoScripts, FailOnQuarantine: !req.NoScripts, ScopePolicy: pol,
		Source: src, AI: req.AI, Identity: id.Name, Local: req.Local,
	})
}

// assemble folds the per-identity reports into rows and cells, then derives
// sameness, flags and the summary.
func (s *Service) assemble(m *Matrix, run []Identity, reports []*collrun.Report) {
	type key = string
	rowIdx := map[key]int{}
	for ci, rep := range reports {
		if rep == nil {
			continue
		}
		for _, it := range rep.Items {
			if it.Iteration != 0 {
				continue
			}
			ri, ok := rowIdx[it.ItemUID]
			if !ok {
				if len(m.Rows) >= MaxRows {
					m.Warnings = append(m.Warnings, fmt.Sprintf("more than %d requests; extra rows dropped", MaxRows))
					continue
				}
				ri = len(m.Rows)
				rowIdx[it.ItemUID] = ri
				cells := make([]Cell, len(run))
				for i := range cells {
					cells[i] = Cell{Class: ClassNotRun}
				}
				m.Rows = append(m.Rows, Row{ItemUID: it.ItemUID, Name: s.scrub(it.Name), Method: it.Method, URL: s.scrub(it.URL), Cells: cells})
			}
			if m.Rows[ri].URL == "" {
				m.Rows[ri].URL = s.scrub(it.URL)
			}
			m.Rows[ri].Cells[ci] = s.cellOf(it, !run[ci].Anonymous)
		}
	}
	base := indexOf(m.Identities, m.Baseline)
	for _, id := range run {
		m.Expect[id.Name] = expectation(id, id.Name == m.Baseline)
		if m.Expect[id.Name] == "" {
			delete(m.Expect, id.Name)
		}
	}
	for ri := range m.Rows {
		row := &m.Rows[ri]
		for ci := range row.Cells {
			c := &row.Cells[ci]
			if base >= 0 && ci != base {
				c.SameAsBaseline = sameAccess(row.Cells[base], *c)
			} else if ci == base {
				c.SameAsBaseline = c.Class != ClassNotRun
			}
			if ci != base {
				c.Flag = flagFor(m.Expect[run[ci].Name], *c)
			}
		}
	}
	m.Summary = summarize(m)
	if m.Summary.Violations > 0 {
		m.Hypotheses = append(m.Hypotheses, "Hypothesis: an identity expected to be denied received a successful response. This may be broken access control or missing authentication. Reproduce before reporting.")
	}
	if m.Summary.UnexpectedDenials > 0 {
		m.Hypotheses = append(m.Hypotheses, "Note: an identity expected to be allowed was denied; check the identity's session is still valid.")
	}
}

func (s *Service) cellOf(it collrun.ItemResult, hasAuth bool) Cell {
	c := Cell{Status: it.HTTPStatus, Size: it.Size, DurationMs: it.DurationMs, FlowID: it.FlowID, Error: s.scrub(it.Error)}
	switch it.Outcome {
	case collexec.OutcomeBlocked:
		c.Class, c.Reason = ClassBlocked, string(it.BlockReason)
		return c
	case collexec.OutcomeError:
		c.Class = ClassError
		return c
	case collexec.OutcomeSkipped:
		c.Class, c.Reason = ClassNotRun, "skipped"
		return c
	}
	var loc string
	if s.d.Flows != nil && it.FlowID > 0 {
		if fs, ok := s.d.Flows.FlowSummary(it.FlowID); ok {
			loc, c.bodyHash = fs.Location, fs.BodyHash
			if c.Size == 0 {
				c.Size = fs.Length
			}
		}
	}
	c.Class = classify(it.HTTPStatus, hasAuth, loc)
	c.Denied = c.Class == ClassAuthFailure || c.Class == ClassAuthzFailure
	return c
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func sameAccess(base, c Cell) bool {
	if base.Class == ClassNotRun || c.Class == ClassNotRun || base.Class == ClassBlocked || c.Class == ClassBlocked || base.Class == ClassError || c.Class == ClassError {
		return false
	}
	if base.Status != c.Status {
		return false
	}
	if base.bodyHash != "" && c.bodyHash != "" {
		return base.bodyHash == c.bodyHash
	}
	return base.Size == c.Size
}

// expectation resolves what an identity should get: its own setting, else deny
// for anonymous, else unspecified. The baseline is never flagged.
func expectation(id Identity, baseline bool) string {
	if baseline {
		return ""
	}
	if id.Expect == ExpectAllow || id.Expect == ExpectDeny {
		return id.Expect
	}
	if id.Anonymous {
		return ExpectDeny
	}
	return ""
}

func flagFor(expect string, c Cell) string {
	switch {
	case expect == ExpectDeny && c.Class == ClassSuccess:
		return FlagViolation
	case expect == ExpectAllow && c.Denied:
		return FlagUnexpectedDenial
	}
	return ""
}

func summarize(m *Matrix) Summary {
	sum := Summary{Identities: len(m.Identities), Rows: len(m.Rows)}
	for _, r := range m.Rows {
		for _, c := range r.Cells {
			sum.Cells++
			switch c.Flag {
			case FlagViolation:
				sum.Violations++
			case FlagUnexpectedDenial:
				sum.UnexpectedDenials++
			}
			switch c.Class {
			case ClassBlocked:
				sum.Blocked++
			case ClassError:
				sum.Errors++
			}
			if !c.SameAsBaseline && c.Class != ClassNotRun {
				sum.Differs++
			}
		}
	}
	return sum
}
