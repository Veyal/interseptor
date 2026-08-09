package searchscript

import (
	"context"
	"strings"
	"testing"
)

func TestCompile_Match_returns_predicate_result_when_flow_matches(t *testing.T) {
	// Given
	script, err := Compile(`def match(flow):
    return flow.host == "example.com" and flow.status == 200 and flow.req_header("X-Test") == "needle"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	flow := Flow{Host: "example.com", Status: 200, ReqHeaders: map[string][]string{"X-Test": {"needle"}}}

	// When
	matched, err := script.Match(flow)

	// Then
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !matched {
		t.Fatal("Match = false, want true")
	}
}

func TestCompile_rejects_unsafe_or_invalid_scripts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "missing match", src: `x = 1`},
		{name: "load", src: `load("other.star", "x")
def match(flow): return True`},
		{name: "module step limit", src: `x = [i for i in range(1000000000)]
def match(flow): return True`},
		{name: "oversized source", src: strings.Repeat("#", maxSourceBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := Compile(test.src)

			// Then
			if err == nil {
				t.Fatal("Compile error = nil, want error")
			}
		})
	}
}

func TestScript_MatchContext_stops_when_context_cancelled(t *testing.T) {
	// Given
	script, err := Compile(`def match(flow):
    if flow.host == "run":
        for i in range(1000000000): pass
    return True`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err = script.MatchContext(ctx, Flow{Host: "run"})

	// Then
	if err == nil {
		t.Fatal("MatchContext error = nil, want cancellation")
	}
	if strings.Contains(err.Error(), "run") {
		t.Fatalf("error leaked flow data: %v", err)
	}
}

func TestScript_Match_fails_safely_when_runtime_limit_exceeded(t *testing.T) {
	// Given
	script, err := Compile(`def match(flow):
    if flow.host == "run":
        for i in range(1000000000): pass
    return True`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// When
	_, err = script.Match(Flow{Host: "run"})

	// Then
	if err == nil {
		t.Fatal("Match error = nil, want error")
	}
	if strings.Contains(err.Error(), "run") {
		t.Fatalf("error leaked flow data: %v", err)
	}
}

func TestScript_Match_exposes_immutable_helpers(t *testing.T) {
	// Given
	script, err := Compile(`def match(flow):
    return flow.query_param("q") == "needle" and flow.res_header_all("Set-Cookie")[1] == "b=2" and flow.req_headers["X-Test"] == "needle"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	flow := Flow{Path: "/search?q=needle", ReqHeaders: map[string][]string{"x-test": {"needle"}}, ResHeaders: map[string][]string{"Set-Cookie": {"a=1", "b=2"}}}

	// When
	matched, err := script.Match(flow)

	// Then
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !matched {
		t.Fatal("Match = false, want true")
	}
}
