package checkscript

import (
	"slices"
	"testing"
)

// flow.query_param used to return "" for the whole query string as soon as any
// one pair failed to decode, hiding every valid parameter from a check. The
// enumerating flow.query_params already kept the valid pairs (and falls back to
// the raw text for the bad one), so the single-value accessor must agree.
func TestQueryParamKeepsValidPairsNextToAMalformedOne(t *testing.T) {
	c, _, err := CompileWithLogs("t", `
def check(flow):
    print("u=" + flow.query_param("u"))
    print("seen=" + str("u" in flow.query_params))
    print("bad=" + flow.query_params["bad"][0])
    return []
`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, logs, err := c.RunWithLogs(Flow{Method: "GET", Host: "example.com", Path: "/x?u=admin&bad=%zz"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"u=admin", "seen=True", "bad=%zz"}
	if !slices.Equal(logs, want) {
		t.Fatalf("print output = %q, want %q", logs, want)
	}
}
