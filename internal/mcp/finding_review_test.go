package mcp

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindingRevisionToolValidation(t *testing.T) {
	var paths []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	s := New(backend.URL)
	s.SetActivityReporter(nil)
	invalid := []any{nil, "", 0, -1, "abc", "9223372036854775808", float64(1 << 63), 1.5, math.Inf(1), math.NaN(), true, json.Number("9223372036854775808")}
	for _, name := range []string{"list_finding_revisions", "get_finding_revision", "restore_finding_revision"} {
		keys := []string{"id"}
		if name != "list_finding_revisions" {
			keys = append(keys, "revisionId")
		}
		for _, key := range keys {
			for _, value := range invalid {
				args := map[string]any{"id": 1, "revisionId": 2}
				args[key] = value
				if value == nil {
					delete(args, key)
				}
				count := len(paths)
				if _, err := s.Call(name, args); err == nil || len(paths) != count {
					t.Fatalf("%s %s=%v: err=%v paths=%v", name, key, value, err, paths)
				}
			}
		}
	}
	for _, before := range []any{-1, "abc", "9223372036854775808", 1.25, true, json.Number("1.5")} {
		count := len(paths)
		if _, err := s.Call("list_finding_revisions", map[string]any{"id": 1, "before": before}); err == nil || len(paths) != count {
			t.Fatalf("before=%v: %v", before, err)
		}
	}
	for _, before := range []any{nil, "", 0, "0", float64(0)} {
		args := map[string]any{"id": float64(1)}
		if before != nil {
			args["before"] = before
		}
		if _, err := s.Call("list_finding_revisions", args); err != nil {
			t.Fatal(err)
		}
		if paths[len(paths)-1] != "GET /api/finding-revisions/1?before=0" {
			t.Fatal(paths)
		}
	}
	for _, id := range []any{1, int64(1), float64(1), "1", json.Number("1")} {
		if _, err := s.Call("list_finding_revisions", map[string]any{"id": id, "before": int64(2)}); err != nil {
			t.Fatal(err)
		}
		if paths[len(paths)-1] != "GET /api/finding-revisions/1?before=2" {
			t.Fatal(paths)
		}
	}
	for _, tc := range []struct{ name, want string }{{"get_finding_revision", "GET /api/finding-revisions/1/9223372036854775807"}, {"restore_finding_revision", "POST /api/finding-revisions/1/9223372036854775807/restore"}} {
		if _, err := s.Call(tc.name, map[string]any{"id": 1, "revisionId": "9223372036854775807"}); err != nil {
			t.Fatal(err)
		}
		if paths[len(paths)-1] != tc.want {
			t.Fatal(paths)
		}
	}
}
