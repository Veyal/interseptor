package intruder

import (
	"bytes"
	"reflect"
	"testing"
)

// applyGrep must give the same verdicts whatever the body's size or encoding:
// regex rules, the literal fallback for an invalid regex, multi-rule vectors,
// first-capture extraction, and bodies that are not valid UTF-8.
func TestApplyGrepVerdicts(t *testing.T) {
	match := compileMatchRules([]string{"wel+come", "[unclosed", "absent", "caf\u00e9"})
	extract := compileExtractRules([]string{`token=(\w+)`, `nope=(\w+)`, `id=(\d+)`})
	body := []byte("hello [unclosed welcome caf\u00e9 \xff\xfe token=abc123 id=42 id=43")

	var res Result
	applyGrep(&res, body, match, extract)
	if !res.Matched || !reflect.DeepEqual(res.Matches, []bool{true, true, false, true}) {
		t.Fatalf("matches = %v matched=%v", res.Matches, res.Matched)
	}
	if res.Extracted != "abc123" || !reflect.DeepEqual(res.Extracts, []string{"abc123", "", "42"}) {
		t.Fatalf("extracts = %q first=%q", res.Extracts, res.Extracted)
	}

	// The extracted value must not alias the body buffer: results outlive it.
	for i := range body {
		body[i] = 'x'
	}
	if res.Extracted != "abc123" || res.Extracts[2] != "42" {
		t.Fatalf("extracted value aliases the response buffer: %q %q", res.Extracted, res.Extracts)
	}

	var none Result
	applyGrep(&none, []byte("nothing here"), match, extract)
	if none.Matched || none.Extracted != "" || !reflect.DeepEqual(none.Matches, []bool{false, false, false, false}) {
		t.Fatalf("no-match result = %+v", none)
	}
	var empty Result
	applyGrep(&empty, nil, nil, nil)
	if empty.Matches != nil || empty.Extracts != nil {
		t.Fatalf("no rules must leave the vectors nil: %+v", empty)
	}
}

func BenchmarkApplyGrep1MiB(b *testing.B) {
	body := bytes.Repeat([]byte("<div class=\"row\">lorem ipsum dolor sit amet</div>\n"), (1<<20)/50)
	body = append(body, []byte("csrf_token=deadbeef")...)
	match := compileMatchRules([]string{"csrf_token", "[literal"})
	extract := compileExtractRules([]string{`csrf_token=(\w+)`})
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var res Result
		applyGrep(&res, body, match, extract)
		if res.Extracted != "deadbeef" {
			b.Fatal(res.Extracted)
		}
	}
}
