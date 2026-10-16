package sender

import "testing"

func eqParams(t *testing.T, got, want []Param) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("param %d = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestParseParamsPreservesOrderAndDuplicates(t *testing.T) {
	got := ParseParams("b=2&a=1&b=3&a=1&flag")
	eqParams(t, got, []Param{
		{Key: "b", Value: "2"},
		{Key: "a", Value: "1"},
		{Key: "b", Value: "3"},
		{Key: "a", Value: "1"},
		{Key: "flag", Value: ""},
	})
}

func TestParseParamsDecodesPercentAndPlus(t *testing.T) {
	got := ParseParams("q=a%20b+c&name%20x=v%26w")
	eqParams(t, got, []Param{
		{Key: "q", Value: "a b c"},
		{Key: "name x", Value: "v&w"},
	})
}

func TestParseParamsSkipsEmptySegments(t *testing.T) {
	got := ParseParams("&a=1&&b=2&")
	eqParams(t, got, []Param{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}})
}

func TestBuildParamsRoundTripsLossless(t *testing.T) {
	// Order and duplicates must survive a parse -> build -> parse cycle, and the
	// second parse must equal the first (idempotent, so a UI can edit either side).
	raw := "b=2&a=1&b=3&a=1&weird=a%20b%26c&flag"
	first := ParseParams(raw)
	rebuilt := BuildParams(first)
	second := ParseParams(rebuilt)
	eqParams(t, second, first)
}

func TestBuildParamsEncodesSpecials(t *testing.T) {
	got := BuildParams([]Param{{Key: "k", Value: "a b&c=d"}, {Key: "x y", Value: "1"}})
	if got != "k=a+b%26c%3Dd&x+y=1" {
		t.Fatalf("BuildParams = %q", got)
	}
}

func TestBuildParamsEmpty(t *testing.T) {
	if got := BuildParams(nil); got != "" {
		t.Fatalf("BuildParams(nil) = %q, want empty", got)
	}
}
