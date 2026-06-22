package store

import (
	"testing"
	"time"
)

func TestRuleCRUD(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if rules, err := s.ListRules(); err != nil || len(rules) != 0 {
		t.Fatalf("expected no rules, got %v err=%v", rules, err)
	}

	id, err := s.CreateRule(&Rule{Ord: 0, Enabled: true, Type: "req-header", Match: "User-Agent: .*", Replace: "User-Agent: interceptor"})
	if err != nil || id == 0 {
		t.Fatalf("CreateRule: id=%d err=%v", id, err)
	}

	rules, err := s.ListRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("ListRules: %v err=%v", rules, err)
	}
	if rules[0].Match != "User-Agent: .*" || !rules[0].Enabled || rules[0].Type != "req-header" {
		t.Fatalf("unexpected rule: %+v", rules[0])
	}

	rules[0].Enabled = false
	rules[0].Replace = "User-Agent: changed"
	if err := s.UpdateRule(&rules[0]); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	got, _ := s.ListRules()
	if got[0].Enabled || got[0].Replace != "User-Agent: changed" {
		t.Fatalf("update not applied: %+v", got[0])
	}

	if err := s.DeleteRule(id); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if rules, _ := s.ListRules(); len(rules) != 0 {
		t.Fatalf("expected rule deleted, got %v", rules)
	}
}

func TestFlagsRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	in := &Flow{TS: time.UnixMilli(1), Method: "GET", Host: "h", Path: "/", Flags: FlagIntercepted | FlagEdited}
	id, err := s.InsertFlow(in)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	got, err := s.GetFlow(id)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Flags != FlagIntercepted|FlagEdited {
		t.Fatalf("flags not round-tripped: %d", got.Flags)
	}
}

func TestQueryFlowsFilter(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	seed := []*Flow{
		{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/users", Status: 200},
		{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "api.example.com", Path: "/login", Status: 401},
		{TS: time.UnixMilli(3), Method: "GET", Scheme: "http", Host: "cdn.other.com", Path: "/img.png", Status: 200},
		{TS: time.UnixMilli(4), Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/users/42", Status: 500},
	}
	for _, f := range seed {
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	// Host substring.
	if got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, Host: "example.com"}); len(got) != 3 {
		t.Fatalf("host filter: expected 3, got %d", len(got))
	}
	// Method.
	if got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, Method: "POST"}); len(got) != 1 || got[0].Path != "/login" {
		t.Fatalf("method filter: %+v", got)
	}
	// Status class 5xx.
	if got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, StatusClass: 5}); len(got) != 1 || got[0].Status != 500 {
		t.Fatalf("status-class filter: %+v", got)
	}
	// Scheme.
	if got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, Scheme: "http"}); len(got) != 1 || got[0].Host != "cdn.other.com" {
		t.Fatalf("scheme filter: %+v", got)
	}
	// Path search.
	if got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, Search: "/users"}); len(got) != 2 {
		t.Fatalf("search filter: expected 2, got %d", len(got))
	}
	// Newest-first ordering + pagination via BeforeID.
	all, _ := s.QueryFlowsFilter(FlowFilter{Limit: 2})
	if len(all) != 2 || all[0].Path != "/users/42" {
		t.Fatalf("expected newest-first page, got %+v", all)
	}
	page2, _ := s.QueryFlowsFilter(FlowFilter{Limit: 2, BeforeID: all[len(all)-1].ID})
	if len(page2) != 2 || page2[0].ID >= all[len(all)-1].ID {
		t.Fatalf("pagination broken: %+v", page2)
	}
}
