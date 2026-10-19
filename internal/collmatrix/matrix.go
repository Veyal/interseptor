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
	Baseline    string `json:"baseline,omitempty"`
	NoScripts   bool   `json:"noScripts,omitempty"`
	ScopePolicy string `json:"scopePolicy,omitempty"` // default block
	// ObjectParam + ObjectIDs drive object-identifier substitution (BOLA/IDOR):
	// ObjectParam is the request variable to substitute (e.g. "userId", used as
	// {{userId}} in the item's URL/query/body) and ObjectIDs maps an identity to
	// the object id it legitimately owns. When both are set the matrix permutes
	// every identity across every owner's object id — the standard access-control
	// differential test. A cross-access success is a candidate, not a finding.
	ObjectParam string            `json:"objectParam,omitempty"`
	ObjectIDs   map[string]string `json:"objectIds,omitempty"`
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

// Row is one request across every identity. In object-identifier mode a row is
// one request against one owner's object id; Owner names that identity and
// ObjectID is the substituted value.
type Row struct {
	ItemUID  string `json:"itemUid"`
	Name     string `json:"name"`
	Method   string `json:"method,omitempty"`
	URL      string `json:"url,omitempty"`
	Owner    string `json:"owner,omitempty"`    // object-owner identity (object-id mode)
	ObjectID string `json:"objectId,omitempty"` // substituted object id for this row
	Cells    []Cell `json:"cells"`              // same order as Matrix.Identities
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
	ObjectParam   string            `json:"objectParam,omitempty"` // set in object-identifier mode
	Expect        map[string]string `json:"expect,omitempty"`      // identity -> allow|deny (resolved)
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
	scen := s.scenarios(req, run)
	m := &Matrix{
		ID: newID(), CollectionUID: req.CollectionUID, EnvUID: req.EnvUID, StartedMs: s.d.Now().UnixMilli(),
		Baseline: pickBaseline(run, req.Baseline), Skipped: skipped, Runs: map[string]string{}, Expect: map[string]string{},
	}
	if objectMode(scen) {
		m.ObjectParam = req.ObjectParam
	}
	for _, id := range run {
		m.Identities = append(m.Identities, id.Name)
	}
	// reports[identity][scenario]
	reports := make([][]*collrun.Report, len(run))
	total := len(run) * len(scen)
	doneN := 0
run:
	for i, id := range run {
		reports[i] = make([]*collrun.Report, len(scen))
		for si, sc := range scen {
			if ctx.Err() != nil {
				m.Partial = true
				break run
			}
			rep, rerr := s.runAs(ctx, req, id, known, sc.local)
			if rerr != nil {
				return nil, rerr
			}
			reports[i][si] = rep
			key := id.Name
			if len(scen) > 1 {
				key = id.Name + " @ " + sc.owner
			}
			m.Runs[key] = rep.RunUID
			if m.Collection == "" {
				m.Collection = s.scrub(rep.CollectionName)
			}
			if rep.Status != collrun.StatusDone && rep.Status != collrun.StatusStopped {
				m.Warnings = append(m.Warnings, s.scrub(runEndedWarning(id.Name, rep)))
				if rep.Status == collrun.StatusAborted {
					m.Partial = true
				}
			}
			doneN++
			if req.OnProgress != nil {
				req.OnProgress(id.Name, doneN, total)
			}
		}
	}
	s.assemble(m, run, scen, reports)
	m.FinishedMs = s.d.Now().UnixMilli()
	s.remember(m)
	return m, nil
}

// scenario is one run configuration. In normal mode there is a single scenario
// with no owner; in object-identifier mode there is one per object owner, each
// pinning ObjectParam to that owner's id via a local variable.
type scenario struct {
	owner    string
	objectID string
	local    map[string]string
}

func objectMode(scen []scenario) bool { return len(scen) > 0 && scen[0].owner != "" }

// scenarios builds the run scenarios. Object-identifier mode activates only when
// ObjectParam and at least one runnable identity's object id are given.
func (s *Service) scenarios(req MatrixRequest, run []Identity) []scenario {
	if strings.TrimSpace(req.ObjectParam) == "" || len(req.ObjectIDs) == 0 {
		return []scenario{{local: req.Local}}
	}
	var out []scenario
	for _, id := range run {
		v, ok := lookupObjectID(req.ObjectIDs, id.Name)
		if !ok {
			continue
		}
		local := map[string]string{}
		for k, vv := range req.Local {
			local[k] = vv
		}
		local[req.ObjectParam] = v
		out = append(out, scenario{owner: id.Name, objectID: v, local: local})
	}
	if len(out) == 0 {
		return []scenario{{local: req.Local}}
	}
	return out
}

func lookupObjectID(ids map[string]string, name string) (string, bool) {
	if v, ok := ids[name]; ok {
		return v, true
	}
	for k, v := range ids {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func (s *Service) runAs(ctx context.Context, req MatrixRequest, id Identity, known []Identity, local map[string]string) (*collrun.Report, error) {
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
		Source: src, AI: req.AI, Identity: id.Name, Local: local,
	})
}

// assemble folds the per-identity, per-scenario reports into rows and cells,
// then derives sameness, flags and the summary. In object-identifier mode each
// (request, owner) pair is its own row; otherwise there is one row per request.
func (s *Service) assemble(m *Matrix, run []Identity, scen []scenario, reports [][]*collrun.Report) {
	object := objectMode(scen)
	rowIdx := map[string]int{}
	for ci := range run {
		for si, rep := range reports[ci] {
			if rep == nil {
				continue
			}
			sc := scen[si]
			for _, it := range rep.Items {
				if it.Iteration != 0 {
					continue
				}
				key := it.ItemUID
				if object {
					key = it.ItemUID + "\x00" + sc.owner
				}
				ri, ok := rowIdx[key]
				if !ok {
					if len(m.Rows) >= MaxRows {
						m.Warnings = append(m.Warnings, fmt.Sprintf("more than %d requests; extra rows dropped", MaxRows))
						continue
					}
					ri = len(m.Rows)
					rowIdx[key] = ri
					cells := make([]Cell, len(run))
					for i := range cells {
						cells[i] = Cell{Class: ClassNotRun}
					}
					row := Row{ItemUID: it.ItemUID, Name: s.scrub(it.Name), Method: it.Method, URL: s.scrub(it.URL), Cells: cells}
					if sc.owner != "" {
						row.Owner = sc.owner
						row.ObjectID = s.scrub(sc.objectID)
						row.Name = fmt.Sprintf("%s — %s's object", row.Name, sc.owner)
					}
					m.Rows = append(m.Rows, row)
				}
				if m.Rows[ri].URL == "" {
					m.Rows[ri].URL = s.scrub(it.URL)
				}
				m.Rows[ri].Cells[ci] = s.cellOf(it, !run[ci].Anonymous)
			}
		}
	}
	s.finalize(m, run)
}

// finalize derives per-cell sameness, flags, the summary and hypotheses from the
// assembled rows. It is the one differential core shared by the collection
// identity matrix and the captured-flow (history) matrix, and it covers both the
// expectation-based and the object-identifier flagging modes.
func (s *Service) finalize(m *Matrix, run []Identity) {
	base := indexOf(m.Identities, m.Baseline)
	object := false
	for _, id := range run {
		m.Expect[id.Name] = expectation(id, id.Name == m.Baseline)
		if m.Expect[id.Name] == "" {
			delete(m.Expect, id.Name)
		}
	}
	for ri := range m.Rows {
		row := &m.Rows[ri]
		ownerIdx := -1
		if row.Owner != "" {
			ownerIdx = indexOf(m.Identities, row.Owner)
			object = true
		}
		for ci := range row.Cells {
			c := &row.Cells[ci]
			if base >= 0 && ci != base {
				c.SameAsBaseline = sameAccess(row.Cells[base], *c)
			} else if ci == base {
				c.SameAsBaseline = c.Class != ClassNotRun
			}
			switch {
			case ownerIdx >= 0:
				// Object-id mode: any identity other than the object's owner that
				// gets a successful response has reached another identity's object.
				if ci != ownerIdx && c.Class == ClassSuccess {
					c.Flag = FlagViolation
					c.Reason = "candidate cross-object access"
				}
			case ci != base:
				c.Flag = flagFor(m.Expect[run[ci].Name], *c)
			}
		}
	}
	m.Summary = summarize(m)
	switch {
	case object && m.Summary.Violations > 0:
		m.Hypotheses = append(m.Hypotheses, "Hypothesis: an identity received a successful response to another identity's object id — a candidate object-level authorization bypass (BOLA/IDOR, horizontal privilege escalation). This is a candidate to confirm, not a finding; reproduce before reporting.")
	case m.Summary.Violations > 0:
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

// runEndedWarning words why one identity's run did not finish. A run stopped by
// unapproved scripts gets a plain instruction instead of the raw status code.
func runEndedWarning(identity string, rep *collrun.Report) string {
	if rep.Status == collrun.StatusNotApproved {
		n := rep.Totals.Quarantined
		what := "scripts are"
		if n == 1 {
			what = "script is"
		}
		count := ""
		if n > 0 {
			count = fmt.Sprintf("%d ", n)
		}
		return fmt.Sprintf("%s: nothing was sent because %s%s not approved. Approve them in Collections > Scripts, or run the matrix without scripts.", identity, count, what)
	}
	return fmt.Sprintf("%s: run ended %s %s", identity, rep.Status, rep.StopReason)
}
