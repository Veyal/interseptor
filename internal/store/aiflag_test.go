package store

import (
	"testing"
	"time"
)

// AI-originated sends carry FlagAI on top of their Repeater/Intruder/ActiveScan
// flag. History excludes those flags but must still show AI traffic, so
// IncludeFlags overrides ExcludeFlags: a row with any IncludeFlags bit is kept
// even when an ExcludeFlags bit also matches.
func TestQueryFlowsFilterIncludeFlagsOverridesExclude(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "h", Path: "/plain", Flags: FlagRepeater}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "h", Path: "/ai", Flags: FlagRepeater | FlagAI}); err != nil {
		t.Fatal(err)
	}

	got, err := s.QueryFlowsFilter(FlowFilter{ExcludeFlags: FlagRepeater, IncludeFlags: FlagAI})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/ai" {
		t.Fatalf("want only the AI-tagged /ai flow exempt from exclude, got %d flows", len(got))
	}
}

func TestQueryFlowsFilterWithoutFlags(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "h", Path: "/human", Flags: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "h", Path: "/ai", Flags: FlagAI}); err != nil {
		t.Fatal(err)
	}

	got, err := s.QueryFlowsFilter(FlowFilter{WithoutFlags: FlagAI})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/human" {
		t.Fatalf("WithoutFlags=FlagAI should drop AI rows, got %d flows", len(got))
	}
}
