package store

import (
	"errors"
	"testing"
)

// An empty host pattern matches every host (hostpattern.MatchHost("") is
// documented as match-all for scope), so passing one to a delete purge would
// wipe the whole history from a blank line in a pasted host list. That path must
// refuse the pattern instead of reading it as "everything".
func TestDeleteFlowsByHostRejectsBlankPatterns(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	mustInsertFlow(t, s, "other.test", "", "", 0, 0)

	for _, hosts := range [][]string{{""}, {"   "}, {"example.com", ""}, {"\t"}} {
		for _, keepOnly := range []bool{false, true} {
			n, err := s.DeleteFlowsByHost(hosts, keepOnly)
			if err == nil {
				t.Fatalf("DeleteFlowsByHost(%q, keepOnly=%v) accepted a blank pattern and deleted %d", hosts, keepOnly, n)
			}
			if !errors.Is(err, ErrBlankHostPattern) {
				t.Fatalf("DeleteFlowsByHost(%q) error = %v, want ErrBlankHostPattern", hosts, err)
			}
			remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
			if len(remaining) != 2 {
				t.Fatalf("purge with a blank pattern removed data: %d flows left", len(remaining))
			}
		}
	}
}
