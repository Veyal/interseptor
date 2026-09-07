package store

import (
	"reflect"
	"testing"
)

func TestTargetCleanupKeepsDistinctContextsAndEvidence(t *testing.T) {
	in := FindingTargets{
		{URL: "https://EXAMPLE.com/users/42", Methods: []string{"get"}, Role: "reader", FlowIDs: []int64{1}, Note: "First"},
		{URL: "https://example.com/users/42", Methods: []string{"GET"}, Role: "reader", FlowIDs: []int64{2}, Note: "Second"},
		{URL: "https://example.com/users/42", Methods: []string{"POST"}, Role: "reader"},
		{URL: "http://example.com/users/42", Methods: []string{"GET"}, Role: "reader"},
		{URL: "https://example.com/users/42", Methods: []string{"GET"}, Role: "admin"},
		{URL: "https://example.com/users/42", Methods: []string{"GET"}, Role: "reader", Variant: "email"},
	}
	p, err := PreviewFindingTargets(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 5 || !reflect.DeepEqual(p.Targets[0].FlowIDs, []int64{1, 2}) || p.Targets[0].Note != "First\n\nSecond" {
		t.Fatalf("cleanup %+v", p)
	}
	if p.Targets[0].URL != "https://example.com/users/42" || p.Suggestions[0].Template != "https://example.com/users/{id}" {
		t.Fatalf("template applied without consent: %+v", p)
	}
	if in[0].URL != "https://EXAMPLE.com/users/42" || len(in[0].FlowIDs) != 1 {
		t.Fatal("preview mutated original")
	}
}
func TestTargetCleanupLegacySplittingIsConservative(t *testing.T) {
	p, err := PreviewFindingTargets(nil, "https://example.com/a; https://example.com/b")
	if err != nil || len(p.Targets) != 2 {
		t.Fatalf("split %+v %v", p, err)
	}
	p, err = PreviewFindingTargets(nil, "https://example.com/a;parameter=1")
	if err != nil || len(p.Targets) != 1 {
		t.Fatalf("path semicolon changed %+v %v", p, err)
	}
}
