package control

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const r3BundleHead = `{"version":"1","har":{"log":{"version":"1.2","entries":[
 {"startedDateTime":"2026-01-01T00:00:00Z","request":{"method":"GET","url":"https://app.example.com/one","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
 {"startedDateTime":"2026-01-01T00:00:01Z","request":{"method":"GET","url":"https://app.example.com/two","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}}
]}},"notes":"imported notes"`

func importBundleR3(h *Hub, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/import/project", strings.NewReader(body)))
	return rec
}

func assertNothingImported(t *testing.T, st *store.Store) {
	t.Helper()
	if n, _ := st.FlowCount(); n != 0 {
		t.Errorf("%d flows were imported from a rejected bundle", n)
	}
	if rules, _ := st.ListRules(); len(rules) != 0 {
		t.Errorf("%d rules were imported from a rejected bundle", len(rules))
	}
	if sc, _ := st.ListScopeRules(); len(sc) != 0 {
		t.Errorf("%d scope rules were imported from a rejected bundle", len(sc))
	}
	if notes, _ := st.LoadNotes(); notes != "" {
		t.Errorf("notes were imported from a rejected bundle: %q", notes)
	}
}

// Every section is validated before any is applied: a bundle whose LAST
// section is unusable used to import its flows, rules, scope and notes and
// then answer 500.
func TestImportProjectRejectsABadLastSectionBeforeApplyingAnything(t *testing.T) {
	good := `,"rules":[{"type":"req-header","match":"X-Old","replace":"X-New","enabled":true}],"scope":[{"action":"include","host":"app.example.com","enabled":true}]`
	for name, tail := range map[string]string{
		"collections from a newer version":  good + `,"collections":{"version":99999}}`,
		"rule with an invalid regex":        `,"scope":[{"action":"include","host":"app.example.com","enabled":true}],"rules":[{"type":"req-header","match":"(","enabled":true}]}`,
		"rule with an unknown type":         `,"rules":[{"type":"nope","match":"x","enabled":true}]}`,
		"scope rule with an unknown action": `,"rules":[{"type":"req-header","match":"X-Old","enabled":true}],"scope":[{"action":"maybe","host":"app.example.com"}]}`,
		"scope rule with an invalid regex":  `,"scope":[{"action":"include","host":"/(/"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, st, _ := newHub(t)
			rec := importBundleR3(h, r3BundleHead+tail)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d %s, want 400", rec.Code, rec.Body.String())
			}
			assertNothingImported(t, st)
		})
	}
}

// A storage failure part-way cannot be undone (the sections are separate
// writes), so the answer names what was applied, and what was applied is live:
// the rules that were stored are compiled into the engine and announced.
func TestImportProjectReportsAppliedSectionsOnAMidWayStorageFailure(t *testing.T) {
	h, st, _ := newHub(t)
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER r3_fail_scope BEFORE INSERT ON scope_rules
		BEGIN SELECT RAISE(ABORT, 'r3 injected storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	events, unsub := subscribeRaw(h, 64)
	defer unsub()

	rec := importBundleR3(h, r3BundleHead+`,"rules":[{"type":"req-header","match":"X-Old","replace":"X-New","enabled":true}],"scope":[{"action":"include","host":"app.example.com","enabled":true}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d %s, want 500", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "r3 injected") {
		t.Fatalf("raw storage error leaked: %s", rec.Body.String())
	}
	var res struct {
		Error         string         `json:"error"`
		FailedSection string         `json:"failedSection"`
		Applied       []string       `json:"applied"`
		NotApplied    []string       `json:"notApplied"`
		Counts        map[string]int `json:"counts"`
		Partial       bool           `json:"partial"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if res.FailedSection != "scope" || !res.Partial {
		t.Errorf("failedSection %q partial %v, want scope/true: %s", res.FailedSection, res.Partial, rec.Body.String())
	}
	if strings.Join(res.Applied, ",") != "flows,rules" {
		t.Errorf("applied = %v, want [flows rules]", res.Applied)
	}
	if strings.Join(res.NotApplied, ",") != "settings,notes,collections" {
		t.Errorf("notApplied = %v, want [settings notes collections]", res.NotApplied)
	}
	if res.Counts["importedFlows"] != 2 || res.Counts["importedRules"] != 1 {
		t.Errorf("counts = %v, want 2 flows and 1 rule", res.Counts)
	}
	for _, want := range []string{"flows", "rules", "scope"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("error %q does not name %q", res.Error, want)
		}
	}
	if n, _ := st.FlowCount(); n != 2 {
		t.Errorf("%d flows stored, want 2", n)
	}
	if rules, _ := st.ListRules(); len(rules) != 1 {
		t.Errorf("%d rules stored, want 1", len(rules))
	}
	seen := map[string]bool{}
	for len(events) > 0 {
		e := <-events
		for _, typ := range []string{"flow.new", "rules.update"} {
			if strings.Contains(e, `"type":"`+typ+`"`) {
				seen[typ] = true
			}
		}
	}
	if !seen["flow.new"] || !seen["rules.update"] {
		t.Errorf("applied sections were not announced (saw %v): History and the rule engine keep the pre-import state", seen)
	}
}
