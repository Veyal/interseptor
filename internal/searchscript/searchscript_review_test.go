package searchscript

import "testing"

// The dialect is spelled out instead of read from the process-wide resolver
// flags; it must be unchanged: set() allowed, while loops rejected.
func TestCompileDialectIsExplicitAndUnchanged(t *testing.T) {
	s, err := Compile("def match(flow):\n    return len(set([1, 1])) == 1\n")
	if err != nil {
		t.Fatalf("set() must stay available: %v", err)
	}
	if ok, err := s.Match(Flow{}); err != nil || !ok {
		t.Fatalf("match = %v, %v", ok, err)
	}
	if _, err := Compile("def match(flow):\n    while True:\n        pass\n    return True\n"); err == nil {
		t.Fatal("while loops must stay rejected")
	}
}
