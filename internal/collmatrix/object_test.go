package collmatrix

import (
	"context"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
)

// Object-identifier substitution permutes every identity across every owner's
// object id. userA reading userB's object (a horizontal escalation bug) is
// flagged as a candidate; userB correctly denied on userA's object is not.
func TestRunMatrixObjectSubstitutionFlagsCrossAccess(t *testing.T) {
	be := newFake("get profile")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		uid := in.Local["uid"]
		switch in.Identity {
		case "userA":
			// BUG: userA can read any profile (own 101 and userB's 102).
			return stepResp(200, 500, 5, 10)
		case "userB":
			if uid == "102" { // own object only
				return stepResp(200, 500, 5, 20)
			}
			return stepResp(403, 30, 5, 21) // correctly denied on another's object
		default: // anonymous
			return stepResp(401, 10, 5, 31)
		}
	}
	svc := testService(be, []Identity{
		{Name: "userA", Headers: []Header{{"Authorization", "Bearer a"}}},
		{Name: "userB", Headers: []Header{{"Authorization", "Bearer b"}}},
	})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{
		CollectionUID: "c1",
		ObjectParam:   "uid",
		ObjectIDs:     map[string]string{"userA": "101", "userB": "102"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 1 item x 2 owners (userA, userB) = 2 rows; 3 identity columns each.
	if len(m.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 (item x owners)", len(m.Rows))
	}
	// 3 identities x 2 owners x 1 item = 6 sends.
	if be.calls != 6 {
		t.Fatalf("calls = %d, want 6", be.calls)
	}

	rowA := rowByOwner(t, m, "userA") // uid=101, userA's own object
	rowB := rowByOwner(t, m, "userB") // uid=102, userB's own object
	if rowA.ObjectID != "101" || rowB.ObjectID != "102" {
		t.Fatalf("object ids: rowA=%q rowB=%q", rowA.ObjectID, rowB.ObjectID)
	}

	ua := colIndex(t, m, "userA")
	ub := colIndex(t, m, "userB")
	anon := colIndex(t, m, "anonymous")

	// userB's object accessed by userA -> candidate horizontal escalation.
	if c := rowB.Cells[ua]; c.Class != ClassSuccess || c.Flag != FlagViolation {
		t.Fatalf("userA on userB's object = %+v, want success+violation", c)
	}
	// userB on its own object -> legitimate, never flagged.
	if c := rowB.Cells[ub]; c.Flag != "" {
		t.Fatalf("owner userB on own object flagged: %+v", c)
	}
	// userB denied on userA's object -> not flagged.
	if c := rowA.Cells[ub]; c.Class != ClassAuthzFailure || c.Flag != "" {
		t.Fatalf("userB on userA's object = %+v, want authz_failure, no flag", c)
	}
	// userA on its own object -> legitimate.
	if c := rowA.Cells[ua]; c.Flag != "" {
		t.Fatalf("owner userA on own object flagged: %+v", c)
	}
	// anonymous appears as a column but never as an owner row.
	if c := rowB.Cells[anon]; c.Class != ClassAuthFailure {
		t.Fatalf("anonymous on userB object = %+v", c)
	}

	if m.Summary.Violations < 1 {
		t.Fatalf("summary violations = %d, want >=1", m.Summary.Violations)
	}
	if !hasHypothesis(m, "object") || !hasHypothesis(m, "reproduce") {
		t.Fatalf("missing object-access hypothesis: %v", m.Hypotheses)
	}
}

// Without ObjectIDs the matrix behaves exactly as before (single scenario).
func TestRunMatrixObjectParamWithoutIDsIsNormal(t *testing.T) {
	be := newFake("a")
	svc := testService(be, []Identity{{Name: "u", Headers: []Header{{"Authorization", "t"}}}})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1", ObjectParam: "uid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 1 || m.Rows[0].Owner != "" {
		t.Fatalf("object param alone must not permute: rows=%d owner=%q", len(m.Rows), m.Rows[0].Owner)
	}
}

func rowByOwner(t *testing.T, m *Matrix, owner string) Row {
	t.Helper()
	for _, r := range m.Rows {
		if r.Owner == owner {
			return r
		}
	}
	t.Fatalf("no row for owner %q", owner)
	return Row{}
}

func colIndex(t *testing.T, m *Matrix, name string) int {
	t.Helper()
	for i, n := range m.Identities {
		if n == name {
			return i
		}
	}
	t.Fatalf("identity %q not a column (%v)", name, m.Identities)
	return -1
}

func hasHypothesis(m *Matrix, substr string) bool {
	for _, h := range m.Hypotheses {
		if strings.Contains(strings.ToLower(h), substr) {
			return true
		}
	}
	return false
}
