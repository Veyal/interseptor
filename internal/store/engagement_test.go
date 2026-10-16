package store

import (
	"strings"
	"testing"
)

func TestEngagementBriefVersioning(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	b, err := s.GetEngagementBrief()
	if err != nil || b.Version != 0 {
		t.Fatalf("empty brief = %+v err=%v, want version 0", b, err)
	}

	in := EngagementBrief{Scope: "api.example.com", DoNotTouch: "billing"}
	b1, err := s.SetEngagementBrief(in)
	if err != nil || b1.Version != 1 || b1.UpdatedAt == 0 {
		t.Fatalf("first save = %+v err=%v, want version 1", b1, err)
	}
	// Identical content must not bump the version.
	b2, err := s.SetEngagementBrief(in)
	if err != nil || b2.Version != 1 {
		t.Fatalf("idempotent save = %+v err=%v, want version 1", b2, err)
	}
	in.RateLimits = "1 req/s"
	b3, err := s.SetEngagementBrief(in)
	if err != nil || b3.Version != 2 || b3.RateLimits != "1 req/s" {
		t.Fatalf("changed save = %+v err=%v, want version 2", b3, err)
	}
	got, _ := s.GetEngagementBrief()
	if got.Version != 2 || got.Scope != "api.example.com" {
		t.Fatalf("reload = %+v", got)
	}
}

func TestEngagementBriefRejectsOversizedField(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.SetEngagementBrief(EngagementBrief{Scope: strings.Repeat("x", MaxEngagementFieldBytes+1)})
	if err == nil {
		t.Fatal("expected error for oversized field")
	}
	if b, _ := s.GetEngagementBrief(); b.Version != 0 {
		t.Fatalf("rejected save changed state: %+v", b)
	}
}
