package store

import (
	"reflect"
	"testing"
	"time"
)

func tagFlow(t *testing.T, s *Store, host, path string) int64 {
	t.Helper()
	id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: host, Path: path, Status: 200})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return id
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{" Auth ", "auth", "IDOR!!", "x y z", "", "---"})
	// auth (deduped), idor (punctuation slugged off), x-y-z (spaces -> dashes).
	if !contains(got, "auth") {
		t.Fatalf("expected 'auth' in %v", got)
	}
	if !contains(got, "idor") {
		t.Fatalf("expected 'idor' (slugged) in %v", got)
	}
	if !contains(got, "x-y-z") {
		t.Fatalf("expected 'x-y-z' in %v", got)
	}
	// dedupe: only one 'auth'
	n := 0
	for _, g := range got {
		if g == "auth" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("auth should appear once, got %d in %v", n, got)
	}
	// sorted
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("not sorted: %v", got)
		}
	}
}

func TestSetAndQueryTags(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f1 := tagFlow(t, s, "a.com", "/1")
	f2 := tagFlow(t, s, "b.com", "/2")

	if _, err := s.SetFlowTags(f1, []string{"auth", "idor", "auth"}); err != nil {
		t.Fatalf("SetFlowTags: %v", err)
	}
	if _, err := s.AddFlowTags(f2, []string{"auth"}); err != nil {
		t.Fatalf("AddFlowTags: %v", err)
	}

	tags, _ := s.FlowTags(f1)
	if !reflect.DeepEqual(tags, []string{"auth", "idor"}) {
		t.Fatalf("FlowTags f1 = %v", tags)
	}

	// Batch load + AttachTags
	flows, err := s.QueryFlowsListFilter(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachTags(flows); err != nil {
		t.Fatal(err)
	}
	for _, f := range flows {
		if f.ID == f1 && len(f.Tags) != 2 {
			t.Fatalf("f1 tags via AttachTags = %v", f.Tags)
		}
	}

	// Filter by tag: both flows carry "auth"
	got, err := s.QueryFlowsListFilter(FlowFilter{Tag: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("tag=auth should match 2 flows, got %d", len(got))
	}
	// Only f1 carries "idor"
	got, _ = s.QueryFlowsListFilter(FlowFilter{Tag: "IDOR"}) // case-insensitive via normalize
	if len(got) != 1 || got[0].ID != f1 {
		t.Fatalf("tag=idor should match only f1, got %d", len(got))
	}

	// Distinct + counts
	tc, err := s.DistinctTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tc) != 2 || tc[0].Tag != "auth" || tc[0].Count != 2 {
		t.Fatalf("DistinctTags = %+v", tc)
	}

	// Remove + color
	if err := s.RemoveFlowTag(f1, "idor"); err != nil {
		t.Fatal(err)
	}
	if tags, _ := s.FlowTags(f1); !reflect.DeepEqual(tags, []string{"auth"}) {
		t.Fatalf("after remove, f1 = %v", tags)
	}
	if err := s.SetTagColor("auth", "#ff0000"); err != nil {
		t.Fatal(err)
	}
	tc, _ = s.DistinctTags()
	if tc[0].Color != "#ff0000" {
		t.Fatalf("color not set: %+v", tc[0])
	}
}

func TestEndpointsFilterByTag(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f1 := tagFlow(t, s, "a.com", "/login")
	tagFlow(t, s, "a.com", "/public") // untagged
	s.SetFlowTags(f1, []string{"auth"})

	all, _, err := s.Endpoints(EndpointFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 endpoints unfiltered, got %d", len(all))
	}
	tagged, _, err := s.Endpoints(EndpointFilter{Tag: "AUTH"}) // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 1 || tagged[0].Path != "/login" {
		t.Fatalf("tag=auth should yield only /login, got %+v", tagged)
	}
}

func TestTagsDeletedWithFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := tagFlow(t, s, "a.com", "/x")
	s.SetFlowTags(f, []string{"auth"})
	if _, err := s.DeleteFlows([]int64{f}); err != nil {
		t.Fatal(err)
	}
	if tags, _ := s.FlowTags(f); len(tags) != 0 {
		t.Fatalf("tags should be gone after flow delete, got %v", tags)
	}
	if tc, _ := s.DistinctTags(); len(tc) != 0 {
		t.Fatalf("DistinctTags should be empty, got %+v", tc)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
