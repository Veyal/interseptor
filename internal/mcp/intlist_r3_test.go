package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// A list of ids with an unusable entry used to be rendered from the entries
// that happened to parse (flowIds [1, "x", 2.5] drew flow 1 only), so the
// evidence image silently covered less than the caller asked for.
func TestR3RenderEvidenceRejectsBadListEntries(t *testing.T) {
	bad := []struct {
		name string
		args map[string]any
		want string
	}{
		{"string entry", map[string]any{"kind": "flow_waterfall", "flowIds": []any{1, "x", 2.5}}, `flowIds[1]`},
		{"fraction", map[string]any{"kind": "flow_waterfall", "flowIds": []any{1, 2.5}}, `flowIds[1]`},
		{"zero", map[string]any{"kind": "flow_waterfall", "flowIds": []any{0}}, `flowIds[0]`},
		{"negative", map[string]any{"kind": "finding_chain", "findingIds": []any{3, -4}}, `findingIds[1]`},
		{"out of range", map[string]any{"kind": "flow_waterfall", "flowIds": []any{1e300}}, `flowIds[0]`},
		{"null entry", map[string]any{"kind": "flow_waterfall", "flowIds": []any{1, nil}}, `flowIds[1]`},
		{"attach path", map[string]any{"kind": "finding_chain", "findingIds": []any{1, "x"}, "findingId": 9}, `findingIds[1]`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var got []evReq
			s := evidenceServer(t, &got, func(*http.Request) string { return evJSON() })
			_, err := s.Call("render_evidence", tc.args)
			if err == nil {
				t.Fatalf("accepted; sent %+v", got)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "positive integer") {
				t.Fatalf("error %q does not name %s as a bad positive integer", err, tc.want)
			}
			if len(got) != 0 {
				t.Fatalf("a request was sent despite the bad list: %+v", got)
			}
		})
	}
}

// Valid lists, including the quoted numbers agents often send, are passed on
// in order and as numbers on both paths.
func TestR3RenderEvidenceAcceptsValidLists(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(r *http.Request) string {
		if r.Method == http.MethodGet {
			return evJSON()
		}
		return `{"id":9}`
	})
	if _, err := s.Call("render_evidence", map[string]any{"kind": "flow_waterfall", "flowIds": []any{3, "1", 2.0}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0].query, "flowIds=3%2C1%2C2") {
		t.Fatalf("query = %q", got[0].query)
	}
	// A non-array never reaches the handler: schema validation refuses it.
	if _, err := s.Call("render_evidence", map[string]any{"kind": "flow_waterfall", "flowIds": "4, 5"}); err == nil || !strings.Contains(err.Error(), "flowIds must be an array") {
		t.Fatalf("string list: %v", err)
	}
	if _, err := s.Call("render_evidence", map[string]any{"kind": "finding_chain", "findingIds": []any{"7", 8}, "findingId": 9}); err != nil {
		t.Fatal(err)
	}
	ids, _ := got[1].body["findingIds"].([]any)
	if len(ids) != 2 || ids[0] != float64(7) || ids[1] != float64(8) {
		t.Fatalf("attach body findingIds = %#v, want numbers [7 8]", got[1].body["findingIds"])
	}
	// An absent or empty list is still fine (other kinds do not use them).
	if _, err := s.Call("render_evidence", map[string]any{"kind": "flow_diff", "flowIdA": 1, "flowIdB": 2, "flowIds": []any{}}); err != nil {
		t.Fatal(err)
	}
}
