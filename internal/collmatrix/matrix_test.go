package collmatrix

import (
	"context"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status  int
		hasAuth bool
		loc     string
		want    string
	}{
		{200, true, "", ClassSuccess},
		{204, false, "", ClassSuccess},
		{401, true, "", ClassAuthFailure},
		{403, false, "", ClassAuthFailure},
		{403, true, "", ClassAuthzFailure},
		{302, false, "https://api.example.com/login?next=/x", ClassAuthFailure},
		{302, true, "/dashboard", ClassOther},
		{400, true, "", ClassValidation},
		{422, true, "", ClassValidation},
		{500, true, "", ClassOther},
		{404, true, "", ClassOther},
	}
	for _, c := range cases {
		if got := classify(c.status, c.hasAuth, c.loc); got != c.want {
			t.Errorf("classify(%d,%v,%q) = %s want %s", c.status, c.hasAuth, c.loc, got, c.want)
		}
	}
}

func scripted(statusByIdentity map[string]int) func(collexec.StepInput) *collexec.StepResult {
	return func(in collexec.StepInput) *collexec.StepResult {
		st, ok := statusByIdentity[in.Identity]
		if !ok {
			st = 500
		}
		return stepResp(st, 100+int64(st), 7, int64(1000+len(in.Identity)*10+st%7))
	}
}

func testService(be *fakeBackend, ids []Identity) *Service {
	return New(Deps{
		Backend:    be,
		Identities: staticIdentities(ids),
		Scrub:      be.Scrub,
	})
}

type staticIdentities []Identity

func (s staticIdentities) Identities() []Identity { return []Identity(s) }

func TestRunMatrixDifferential(t *testing.T) {
	be := newFake("list users", "admin panel")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		admin := in.Chain.Item.Name == "admin panel"
		switch in.Identity {
		case "admin":
			return stepResp(200, 500, 9, 11)
		case "user":
			if admin {
				return stepResp(200, 500, 8, 21) // broken access control
			}
			return stepResp(200, 500, 8, 22)
		default: // anonymous
			return stepResp(401, 20, 3, 31)
		}
	}
	svc := testService(be, []Identity{
		{Name: "admin", Headers: []Header{{"Authorization", "Bearer a"}}},
		{Name: "user", Headers: []Header{{"Authorization", "Bearer u"}}, Expect: ExpectDeny},
	})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.Identities, ","); got != "admin,user,anonymous" {
		t.Fatalf("identities = %s", got)
	}
	if m.Baseline != "admin" || len(m.Rows) != 2 {
		t.Fatalf("baseline=%s rows=%d", m.Baseline, len(m.Rows))
	}
	row := m.Rows[1] // admin panel
	if c := row.Cells[1]; c.Flag != FlagViolation || !c.SameAsBaseline || c.Class != ClassSuccess {
		t.Fatalf("user on admin panel = %+v", c)
	}
	if c := row.Cells[2]; c.Class != ClassAuthFailure || c.Flag != "" || c.SameAsBaseline {
		t.Fatalf("anonymous on admin panel = %+v", c)
	}
	if m.Summary.Violations != 2 || m.Summary.Cells != 6 {
		t.Fatalf("summary = %+v", m.Summary)
	}
	if be.calls != 6 {
		t.Fatalf("calls = %d", be.calls)
	}
}

func TestRunMatrixAnonymousExpectedDenyFlagsOpenEndpoint(t *testing.T) {
	be := newFake("health")
	be.respond = func(in collexec.StepInput) *collexec.StepResult { return stepResp(200, 10, 1, 5) }
	svc := testService(be, []Identity{{Name: "user", Headers: []Header{{"Authorization", "Bearer u"}}}})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if c := m.Rows[0].Cells[1]; c.Flag != FlagViolation {
		t.Fatalf("anonymous success must flag, got %+v", c)
	}
}

func TestRunMatrixUnexpectedDenial(t *testing.T) {
	be := newFake("me")
	be.respond = scripted(map[string]int{"admin": 200, "user": 403, "anonymous": 401})
	svc := testService(be, []Identity{
		{Name: "admin", Headers: []Header{{"Authorization", "x"}}},
		{Name: "user", Headers: []Header{{"Authorization", "y"}}, Expect: ExpectAllow},
	})
	m, _ := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	c := m.Rows[0].Cells[1]
	if c.Class != ClassAuthzFailure || c.Flag != FlagUnexpectedDenial || !c.Denied {
		t.Fatalf("cell = %+v", c)
	}
}

func TestRunMatrixSelectionAndLimits(t *testing.T) {
	be := newFake("a", "b", "c")
	svc := testService(be, []Identity{{Name: "u1", Headers: []Header{{"Authorization", "t"}}}, {Name: "u2", Headers: []Header{{"Authorization", "t"}}}})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1", ItemUIDs: []string{"i-b"}, Identities: []string{"u2"}, Baseline: "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 1 || m.Rows[0].Name != "b" || strings.Join(m.Identities, ",") != "u2" {
		t.Fatalf("matrix = %+v", m)
	}
	if _, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1", Identities: []string{"ghost"}}); err == nil {
		t.Fatal("unknown identity must error")
	}
	if _, err := svc.RunMatrix(context.Background(), MatrixRequest{}); err == nil {
		t.Fatal("missing collection must error")
	}
}

func TestRunMatrixSkipsBrokenIdentityAndReportsIt(t *testing.T) {
	be := newFake("a")
	svc := testService(be, []Identity{
		{Name: "ok", Headers: []Header{{"Authorization", "t"}}},
		{Name: "locked", Headers: []Header{{"Authorization", "t"}}, Broken: true},
	})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(m.Identities, ","), "locked") || len(m.Skipped) != 1 || m.Skipped[0] != "locked" {
		t.Fatalf("identities=%v skipped=%v", m.Identities, m.Skipped)
	}
}

func TestRunMatrixBlockedAndErroredCells(t *testing.T) {
	be := newFake("a")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		if in.Identity == "u" {
			return &collexec.StepResult{Outcome: collexec.OutcomeBlocked, BlockReason: collexec.BlockScope}
		}
		return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: "dial tcp: refused"}
	}
	svc := testService(be, []Identity{{Name: "u", Headers: []Header{{"Authorization", "t"}}}})
	m, _ := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if c := m.Rows[0].Cells[0]; c.Class != ClassBlocked || c.Reason != string(collexec.BlockScope) {
		t.Fatalf("blocked cell = %+v", c)
	}
	if c := m.Rows[0].Cells[1]; c.Class != ClassError || c.Flag != "" {
		t.Fatalf("error cell = %+v", c)
	}
	if m.Summary.Blocked != 1 || m.Summary.Errors != 1 {
		t.Fatalf("summary = %+v", m.Summary)
	}
}

func TestRunMatrixUsesBlockPolicyAndDiscardsWrites(t *testing.T) {
	be := newFake("a")
	var pol []string
	be.respond = func(in collexec.StepInput) *collexec.StepResult { pol = append(pol, in.ScopePolicy); return nil }
	svc := testService(be, nil)
	if _, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range pol {
		if p != "block" {
			t.Fatalf("scope policy = %q, want block", p)
		}
	}
}

func TestRunMatrixScrubsAndHidesCredentials(t *testing.T) {
	be := newFake("a")
	be.reg.Add("canary-secret-9f3")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		r := stepResp(500, 5, 1, 9)
		r.Error = "boom canary-secret-9f3"
		r.URL = "https://api.example.com/x?k=canary-secret-9f3"
		return r
	}
	svc := testService(be, []Identity{{Name: "u", Headers: []Header{{"Authorization", "Bearer canary-secret-9f3"}}}})
	m, _ := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	b, _ := jsonMarshal(m)
	if strings.Contains(string(b), "canary-secret-9f3") {
		t.Fatalf("secret leaked in matrix JSON: %s", b)
	}
}

func TestRunMatrixCancel(t *testing.T) {
	be := newFake("a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	be.respond = func(collexec.StepInput) *collexec.StepResult { cancel(); return nil }
	svc := testService(be, []Identity{{Name: "u", Headers: []Header{{"Authorization", "t"}}}, {Name: "v", Headers: []Header{{"Authorization", "t"}}}})
	m, err := svc.RunMatrix(ctx, MatrixRequest{CollectionUID: "c1"})
	if err == nil && (m == nil || !m.Partial) {
		t.Fatalf("cancelled run must be partial or error: %v %+v", err, m)
	}
}

var _ = collrun.StatusDone
