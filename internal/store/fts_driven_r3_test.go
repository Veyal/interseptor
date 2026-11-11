package store

import (
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// r3ListQueryIN is the list query as it was built before the index-driven
// form: the search as "id IN (SELECT rowid ... MATCH ?)". It is the oracle.
func r3ListQueryIN(f FlowFilter, limit int) (string, []any) {
	where, args := buildFlowFilterWhere(f)
	where, args = appendFlowPageCursor(f, where, args)
	q := "SELECT id FROM flows"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	return q + flowListOrderBy(f) + " LIMIT " + strconv.Itoa(limit), args
}

func r3IDs(t *testing.T, s *Store, q string, args ...any) []int64 {
	t.Helper()
	rows, err := s.db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The index-driven search page returns the same flows in the same order as
// the IN form, for random terms combined with every other list filter, both
// directions, cursors and scan bounds.
func TestR3FTSDrivenListMatchesINFormOnRandomData(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rng := rand.New(rand.NewSource(3))
	hosts := []string{"api.example.com", "cdn.example.com", "login.example.org", "static.example.net", "admin.internal.example.com"}
	words := []string{"users", "orders", "login", "assets", "report", "export", "v1", "v2", "search", "token"}
	methods := []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	statuses := []int{0, 200, 204, 301, 302, 400, 404, 500}
	const n = 1500
	for i := 0; i < n; i++ {
		f := &Flow{
			TS: time.UnixMilli(int64(1_700_000_000_000 + i)), Method: methods[rng.Intn(len(methods))], Scheme: "https",
			Host: hosts[rng.Intn(len(hosts))], Port: 443, Status: statuses[rng.Intn(len(statuses))],
			Path: fmt.Sprintf("/%s/%s/%d?q=%s", words[rng.Intn(len(words))], words[rng.Intn(len(words))], rng.Intn(40), words[rng.Intn(len(words))]),
			Mime: []string{"application/json", "text/html", "image/png"}[rng.Intn(3)],
		}
		if rng.Intn(5) == 0 {
			f.Note = "checked " + words[rng.Intn(len(words))] + ` for "idor"`
		}
		if rng.Intn(9) == 0 {
			f.Flags = FlagRepeater
		}
		if rng.Intn(11) == 0 {
			f.Flags |= FlagAI
		}
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatal(err)
		}
		if rng.Intn(6) == 0 {
			if _, err := s.AddFlowTags(f.ID, []string{[]string{"auth", "todo"}[rng.Intn(2)]}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Holes in the id range, as after deletes.
	if _, err := s.DeleteFlows([]int64{10, 11, 12, 700, 701, 1499}); err != nil {
		t.Fatal(err)
	}
	terms := []string{"example", "com", "users", "orders login", "GET", "api", "idor", `"idor"`, "internal", "tok", "v", "nosuchterm", "example nosuchterm", "checked", "admin.internal", "q", "39", "exam\x00ple"}
	driven := 0
	for round := 0; round < 4000; round++ {
		f := FlowFilter{Search: terms[rng.Intn(len(terms))], Limit: 1 + rng.Intn(60)}
		if rng.Intn(3) == 0 {
			f.SortDir = []int{1, -1}[rng.Intn(2)]
		}
		if rng.Intn(4) == 0 {
			f.Host = []string{"example", "api", "CDN", "nosuch"}[rng.Intn(4)]
		}
		if rng.Intn(4) == 0 {
			f.Method = methods[rng.Intn(len(methods))]
		}
		if rng.Intn(4) == 0 {
			f.StatusClass = 1 + rng.Intn(5)
		}
		if rng.Intn(5) == 0 {
			f.Tag = []string{"auth", "todo", "none"}[rng.Intn(3)]
		}
		if rng.Intn(6) == 0 {
			f.HasNote = true
		}
		if rng.Intn(6) == 0 {
			f.NotHosts = []string{"cdn"}
			f.NotMethods = []string{"OPTIONS"}
			f.NotStatuses = []int{404}
			f.NotPaths = []string{"/export"}
		}
		if rng.Intn(6) == 0 {
			f.ExcludeFlags = FlagRepeater | FlagAI
			if rng.Intn(2) == 0 {
				f.IncludeFlags = FlagAI
			}
		}
		if rng.Intn(8) == 0 {
			f.RequireFlags = FlagRepeater
		}
		if rng.Intn(8) == 0 {
			f.WithoutFlags = FlagAI
		}
		if rng.Intn(8) == 0 {
			f.HideBrowserNoise = true
		}
		if rng.Intn(8) == 0 {
			f.Scheme = "https"
			f.EndpointHost = hosts[rng.Intn(len(hosts))]
		}
		switch rng.Intn(5) {
		case 0:
			f.BeforeID = int64(1 + rng.Intn(n+20))
		case 1:
			f.CursorID = int64(1 + rng.Intn(n+20))
		}
		if rng.Intn(6) == 0 {
			f.ScanBoundID = int64(1 + rng.Intn(n))
		}
		if rng.Intn(10) == 0 {
			for i := 0; i < 30; i++ {
				f.FlowIDs = append(f.FlowIDs, int64(1+rng.Intn(n)))
			}
		}
		if rng.Intn(10) == 0 {
			f.AnyText = []string{"orders", "json", "post", "nosuch"}[rng.Intn(4)]
			if rng.Intn(2) == 0 {
				f.AnyTextIDs = []int64{int64(1 + rng.Intn(n)), int64(1 + rng.Intn(n))}
			}
		}
		if rng.Intn(12) == 0 {
			f.SortKey = []string{"host", "status", "time", "size"}[rng.Intn(4)]
		}
		wantQ, wantArgs := r3ListQueryIN(f, f.Limit)
		want := r3IDs(t, s, wantQ, wantArgs...)
		if _, _, ok := ftsDrivenListQuery(f, "id", f.Limit); ok {
			driven++
		}
		for name, query := range map[string]func(FlowFilter) ([]*Flow, error){"QueryFlowsFilter": s.QueryFlowsFilter, "QueryFlowsListFilter": s.QueryFlowsListFilter} {
			flows, err := query(f)
			if err != nil {
				t.Fatalf("round %d %s %+v: %v", round, name, f, err)
			}
			got := make([]int64, 0, len(flows))
			for _, fl := range flows {
				got = append(got, fl.ID)
			}
			if !(len(got) == 0 && len(want) == 0) && !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d %s %+v:\n got %v\nwant %v", round, name, f, got, want)
			}
		}
	}
	if driven < 3000 {
		t.Fatalf("only %d of 4000 searches took the index-driven query", driven)
	}
}

// A search page walks the full-text index in id order and stops at a full
// page: no list subquery of every match, no sort.
func TestR3FTSDrivenListPlan(t *testing.T) {
	s := seedReviewDB(t, 2000)
	for _, f := range []FlowFilter{
		{Search: "example", Limit: 50},
		{Search: "example", Limit: 50, BeforeID: 900, Host: "svc07", Method: "GET", StatusClass: 2},
		{Search: "example", Limit: 50, SortDir: 1, CursorID: 900, ScanBoundID: 1500},
	} {
		q, args := s.flowListQuery(f, flowListColumns, f.Limit)
		plan := explainPlan(t, s, q, args...)
		if !strings.Contains(plan, "SCAN flows_fts VIRTUAL TABLE") || !strings.Contains(plan, "SEARCH flows USING INTEGER PRIMARY KEY") {
			t.Fatalf("%+v: unexpected plan\n%s", f, plan)
		}
		if strings.Contains(plan, "LIST SUBQUERY") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("%+v: plan materialises or sorts\n%s", f, plan)
		}
	}
}

// TestR3MeasureFTSSearch times History search pages on a large scratch
// database (ISP_R3_DB_DIR), index-driven against the IN form.
func TestR3MeasureFTSSearch(t *testing.T) {
	s := r3OpenScratch(t)
	var maxID int64
	_ = s.db.QueryRow(`SELECT MAX(id) FROM flows`).Scan(&maxID)
	for _, tc := range []struct {
		name string
		f    FlowFilter
	}{
		{"common term", FlowFilter{Search: "example"}},
		{"common term, page 2", FlowFilter{Search: "example", BeforeID: maxID / 2}},
		{"common term, oldest first", FlowFilter{Search: "example", SortDir: 1}},
		{"common + host", FlowFilter{Search: "example", Host: "svc07"}},
		{"common + method", FlowFilter{Search: "example", Method: "DELETE"}},
		{"common + status 4xx", FlowFilter{Search: "example", StatusClass: 4}},
		{"common + host with no flows", FlowFilter{Search: "example", Host: "nosuchhost"}},
		{"common + tag", FlowFilter{Search: "example", Tag: "auth"}},
		{"common + not AI/repeater", FlowFilter{Search: "example", ExcludeFlags: FlagRepeater | FlagAI}},
		{"common + tag on no flow", FlowFilter{Search: "example", Tag: "nosuchtag"}},
		{"common + 3 flow ids", FlowFilter{Search: "example", FlowIDs: []int64{5, maxID / 2, maxID - 3}}},
		{"common + TLS failed", FlowFilter{Search: "example", RequireFlags: FlagTLSFailed}},
		{"common + has note", FlowFilter{Search: "example", HasNote: true}},
		{"rare term", FlowFilter{Search: "976"}},
		{"rare + host", FlowFilter{Search: "976", Host: "svc07"}},
		{"no match", FlowFilter{Search: "zzzzqqq"}},
		{"two words", FlowFilter{Search: "orders 976"}},
	} {
		tc.f.Limit = 50
		inQ, inArgs := r3ListQueryIN(tc.f, 50)
		inQ = strings.Replace(inQ, "SELECT id FROM", "SELECT "+flowListColumns+" FROM", 1)
		before, n1 := r3Time(t, s, inQ, inArgs...)
		q, args := s.flowListQuery(tc.f, flowListColumns, 50)
		after, n2 := r3Time(t, s, q, args...)
		t.Logf("%-28s IN form %12v (%d rows)   index-driven %12v (%d rows)", tc.name, before, n1, after, n2)
	}
}
