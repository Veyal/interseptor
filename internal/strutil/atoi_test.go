package strutil

import "testing"

func TestAtoiOr(t *testing.T) {
	if got := AtoiOr("", 7); got != 7 {
		t.Fatalf("empty = %d, want 7", got)
	}
	if got := AtoiOr("42", 0); got != 42 {
		t.Fatalf("42 = %d", got)
	}
	if got := AtoiOr("nope", 3); got != 3 {
		t.Fatalf("invalid = %d, want 3", got)
	}
}
