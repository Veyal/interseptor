package store

import (
	"reflect"
	"testing"
)

func TestNormalizeFindingTargetsAppliesOnlyApprovedTemplates(t *testing.T) {
	hashA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	in := FindingTargets{
		{URL: "https://example.com/users/edit/1", Methods: []string{"GET"}, Role: "reader", FlowIDs: []int64{1}, ImageHashes: []string{hashA}},
		{URL: "https://example.com/users/edit/2", Methods: []string{"GET"}, Role: "reader", FlowIDs: []int64{2}, ImageHashes: []string{hashB}},
		{URL: "https://example.com/users/edit/3", Methods: []string{"POST"}, Role: "reader", FlowIDs: []int64{3}},
		{URL: "http://example.com/users/edit/4", Methods: []string{"GET"}, Role: "reader", FlowIDs: []int64{4}},
		{URL: "https://example.com/users/edit/5", Methods: []string{"GET"}, Role: "admin", FlowIDs: []int64{5}},
		{URL: "https://example.com/users/edit/6", Methods: []string{"GET"}, Role: "reader", Relation: "setup", FlowIDs: []int64{6}},
		{URL: "https://example.com/orders/7", Methods: []string{"GET"}, FlowIDs: []int64{7}},
	}
	p, err := PreviewFindingTargets(in, "")
	if err != nil {
		t.Fatal(err)
	}
	var approve []int
	for _, s := range p.Suggestions {
		if s.Index < 6 {
			approve = append(approve, s.Index)
		}
	}
	got, err := NormalizeFindingTargets(in, "", approve)
	if err != nil {
		t.Fatal(err)
	}
	// GET/reader pair merges; POST, http, admin and setup variants stay distinct;
	// the unapproved orders template is untouched.
	if len(got.Targets) != 6 {
		t.Fatalf("targets %+v", got.Targets)
	}
	first := got.Targets[0]
	if first.URL != "https://example.com/users/edit/{id}" || !reflect.DeepEqual(first.FlowIDs, []int64{1, 2}) || !reflect.DeepEqual(first.ImageHashes, []string{hashA, hashB}) {
		t.Fatalf("merged target %+v", first)
	}
	if got.Targets[1].Methods[0] != "POST" || got.Targets[2].URL[:5] != "http:" || got.Targets[3].Role != "admin" || got.Targets[4].Relation != "setup" {
		t.Fatalf("distinct contexts changed: %+v", got.Targets)
	}
	if got.Targets[5].URL != "https://example.com/orders/7" || !reflect.DeepEqual(got.Targets[5].FlowIDs, []int64{7}) {
		t.Fatalf("unapproved template applied: %+v", got.Targets[5])
	}
	if in[0].URL != "https://example.com/users/edit/1" {
		t.Fatal("input mutated")
	}
}

func TestNormalizeFindingTargetsRejectsUnknownSuggestion(t *testing.T) {
	in := FindingTargets{{URL: "https://example.com/users/1"}}
	if _, err := NormalizeFindingTargets(in, "", []int{9}); err == nil {
		t.Fatal("unknown approval index accepted")
	}
}
