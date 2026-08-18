package store

import "testing"

func TestFindingTagsRoundTripAndListFilter(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id1, err := s.CreateFinding(&Finding{Title: "API IDOR", Severity: "High", Tags: []string{"API", " oauth ", "api"}})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	id2, err := s.CreateFinding(&Finding{Title: "CMS XSS", Severity: "Medium", Tags: []string{"cms"}})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	_, err = s.CreateFinding(&Finding{Title: "untagged", Severity: "Low"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	got, err := s.GetFinding(id1)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "api" || got.Tags[1] != "oauth" {
		t.Fatalf("tags = %#v, want [api oauth]", got.Tags)
	}

	apiOnly, err := s.ListFindings("", "", "api")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(apiOnly) != 1 || apiOnly[0].ID != id1 {
		t.Fatalf("ListFindings(tag=api) = %+v", apiOnly)
	}

	all, err := s.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 findings, got %d", len(all))
	}
	var sawCMS bool
	for _, f := range all {
		if f.ID == id2 {
			sawCMS = true
			if len(f.Tags) != 1 || f.Tags[0] != "cms" {
				t.Fatalf("cms finding tags = %#v", f.Tags)
			}
		}
		if f.Tags == nil {
			t.Fatalf("finding %d Tags is nil (want [])", f.ID)
		}
	}
	if !sawCMS {
		t.Fatal("missing cms finding in list")
	}

	if _, err := s.SetFindingTags(id2, []string{"website", "cms"}); err != nil {
		t.Fatalf("SetFindingTags: %v", err)
	}
	got2, _ := s.GetFinding(id2)
	if len(got2.Tags) != 2 || got2.Tags[0] != "cms" || got2.Tags[1] != "website" {
		t.Fatalf("after set tags = %#v", got2.Tags)
	}

	counts, err := s.DistinctFindingTags()
	if err != nil {
		t.Fatalf("DistinctFindingTags: %v", err)
	}
	if len(counts) < 2 {
		t.Fatalf("DistinctFindingTags = %+v", counts)
	}

	if err := s.DeleteFinding(id1); err != nil {
		t.Fatalf("DeleteFinding: %v", err)
	}
	apiOnly, err = s.ListFindings("", "", "api")
	if err != nil {
		t.Fatalf("ListFindings after delete: %v", err)
	}
	if len(apiOnly) != 0 {
		t.Fatalf("tag rows should be gone with finding, got %+v", apiOnly)
	}
}

func TestSetFindingTagsRejectsMissingFinding(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SetFindingTags(999, []string{"phantom"}); err == nil {
		t.Fatal("missing finding tag mutation succeeded")
	}
	tags, err := s.DistinctFindingTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("missing finding created phantom tags %+v", tags)
	}
}
