package collmatrix

import (
	"encoding/json"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

func specItem(uid, name, method, path, opID string, tags ...string) store.Item {
	side := `{"openapi":{"path":"` + path + `","method":"` + method + `"` + func() string {
		if opID != "" {
			return `,"operationId":"` + opID + `"`
		}
		return ""
	}() + `}}`
	it := store.Item{UID: uid, Name: name, Kind: "request", Method: method, Sidecar: json.RawMessage(side)}
	if len(tags) > 0 {
		b, _ := json.Marshal(tags)
		it.Tags = b
	}
	return it
}

type fakeRuns struct {
	runs map[string][]store.CollRun
	rows map[string][]store.CollRunResult
}

func (f *fakeRuns) PutRun(r store.CollRun) (*store.CollRun, error) { return &r, nil }
func (f *fakeRuns) ListRuns(c string, n int) ([]store.CollRun, error) {
	return f.runs[c], nil
}
func (f *fakeRuns) AddRunResult(store.CollRunResult) (int64, error) { return 0, nil }
func (f *fakeRuns) ListRunResults(u string) ([]store.CollRunResult, error) {
	return f.rows[u], nil
}

func row(run, item string, it collrun.ItemResult) store.CollRunResult {
	b, _ := json.Marshal(it)
	return store.CollRunResult{RunUID: run, ItemUID: item, Status: string(it.Outcome), ResultJSON: string(b)}
}

func TestCoverageStates(t *testing.T) {
	items := []store.Item{
		{UID: "f1", Kind: "folder", Name: "users"},
		specItem("a", "List users", "GET", "/users", "listUsers", "users"),
		specItem("b", "Create user", "POST", "/users", "", "users"),
		specItem("c", "Delete user", "DELETE", "/users/{id}", "deleteUser", "users", "deprecated"),
		specItem("d", "Get order", "GET", "/orders/{id}", "getOrder", "orders"),
		{UID: "x", Name: "Handwritten", Kind: "request", Method: "GET"},
	}
	runs := &fakeRuns{
		runs: map[string][]store.CollRun{"c1": {{UID: "r2"}, {UID: "r1"}}},
		rows: map[string][]store.CollRunResult{
			"r2": {
				row("r2", "a", collrun.ItemResult{ItemUID: "a", Outcome: collexec.OutcomeSent, HTTPStatus: 200, FlowID: 9, Tests: []collexec.TestResult{{Name: "ok", Status: collexec.TestPass}}}),
				row("r2", "b", collrun.ItemResult{ItemUID: "b", Outcome: collexec.OutcomeSent, HTTPStatus: 500, FlowID: 10, Tests: []collexec.TestResult{{Name: "created", Status: collexec.TestFail}}}),
				row("r2", "d", collrun.ItemResult{ItemUID: "d", Outcome: collexec.OutcomeBlocked, BlockReason: collexec.BlockScope}),
			},
			"r1": {
				row("r1", "a", collrun.ItemResult{ItemUID: "a", Outcome: collexec.OutcomeSent, HTTPStatus: 404}),
				row("r1", "x", collrun.ItemResult{ItemUID: "x", Outcome: collexec.OutcomeSent, HTTPStatus: 200}),
			},
		},
	}
	rep, err := Coverage(items, runs, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 4 || rep.Exercised != 2 || rep.NonSpecItems != 1 {
		t.Fatalf("report = %+v", rep)
	}
	by := map[string]OpCoverage{}
	for _, o := range rep.Operations {
		by[o.Key] = o
	}
	a := by["GET /users"]
	if a.State != CovPassing || a.Hits != 2 || len(a.Statuses) != 2 || a.LastFlowID != 9 || a.OperationID != "listUsers" || a.ItemName != "List users" {
		t.Fatalf("a = %+v", a)
	}
	if by["POST /users"].State != CovFailing {
		t.Fatalf("b = %+v", by["POST /users"])
	}
	if by["DELETE /users/{id}"].State != CovUntested || !by["DELETE /users/{id}"].Deprecated {
		t.Fatalf("c = %+v", by["DELETE /users/{id}"])
	}
	if by["GET /orders/{id}"].State != CovBlocked || by["GET /orders/{id}"].Hits != 0 {
		t.Fatalf("d = %+v", by["GET /orders/{id}"])
	}
	if rep.Percent != 50 {
		t.Fatalf("percent = %v", rep.Percent)
	}
	if g := rep.Groups; len(g) != 2 || g[0].Name != "orders" || g[0].Exercised != 0 || g[1].Name != "users" || g[1].Total != 3 {
		t.Fatalf("groups = %+v", g)
	}
}

func TestCoverageNoSpecItems(t *testing.T) {
	rep, err := Coverage([]store.Item{{UID: "x", Kind: "request"}}, &fakeRuns{}, "c1")
	if err != nil || rep.Total != 0 || rep.Percent != 0 || rep.NonSpecItems != 1 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
}

func TestCoverageRunsOptional(t *testing.T) {
	rep, err := Coverage([]store.Item{specItem("a", "A", "GET", "/a", "")}, nil, "c1")
	if err != nil || rep.Total != 1 || rep.Exercised != 0 || rep.Operations[0].State != CovUntested {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
}
