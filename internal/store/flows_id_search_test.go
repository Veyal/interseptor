package store

import (
	"strconv"
	"testing"
)

func TestFlowSearchAsID(t *testing.T) {
	tests := []struct {
		term, scope string
		want        int64
		ok          bool
	}{
		{"#42", "", 42, true},
		{"# 42", "", 0, false},
		{"id:99", "", 99, true},
		{"ID:7", "", 7, true},
		{"285", "id", 285, true},
		{"285", "", 0, false},
		{"abc", "id", 0, false},
		{"", "id", 0, false},
	}
	for _, tc := range tests {
		got, ok := flowSearchAsID(tc.term, tc.scope)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("flowSearchAsID(%q, %q) = (%d, %v), want (%d, %v)", tc.term, tc.scope, got, ok, tc.want, tc.ok)
		}
	}
}

func TestQueryFlowsFilterByID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := make([]int64, 3)
	for i, p := range []string{"/a", "/b", "/c"} {
		id, err := s.InsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "t.com", Path: p, Status: 200})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	for _, tc := range []struct {
		search, scope string
		want          int64
	}{
		{"#" + strconv.FormatInt(ids[1], 10), "", ids[1]},
		{"id:" + strconv.FormatInt(ids[2], 10), "", ids[2]},
		{strconv.FormatInt(ids[0], 10), "id", ids[0]},
	} {
		got, err := s.QueryFlowsListFilter(FlowFilter{Limit: 10, Search: tc.search, SearchScope: tc.scope})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != tc.want {
			t.Fatalf("Search=%q scope=%q: got %+v, want id %d", tc.search, tc.scope, got, tc.want)
		}
	}
}
