package store

import (
	"testing"
	"time"
)

func TestQueryFlowsFilterExactEndpointBeforeLimit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	seed := []*Flow{
		{TS: time.UnixMilli(1), Scheme: "https", Host: "api.example.com", Port: 443, Path: "/items?id=old", Status: 500, Flags: FlagRepeater},
		{TS: time.UnixMilli(2), Scheme: "https", Host: "other.example.com", Port: 443, Path: "/noise", Status: 200, Flags: FlagRepeater},
		{TS: time.UnixMilli(3), Scheme: "http", Host: "api.example.com", Port: 80, Path: "/items?id=http", Status: 201, Flags: FlagRepeater},
		{TS: time.UnixMilli(4), Scheme: "https", Host: "api.example.com", Port: 8443, Path: "/items?id=other-port", Status: 202, Flags: FlagRepeater},
		{TS: time.UnixMilli(5), Scheme: "https", Host: "api.example.com", Port: 443, Path: "/items/child", Status: 203, Flags: FlagRepeater},
		{TS: time.UnixMilli(6), Scheme: "https", Host: "API.EXAMPLE.COM", Port: 443, Path: "/items?id=new", Status: 401, Flags: FlagRepeater},
	}
	for _, flow := range seed {
		if _, err := s.InsertFlow(flow); err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
	}

	got, err := s.QueryFlowsFilter(FlowFilter{
		Limit:          1,
		RequireFlags:   FlagRepeater,
		EndpointScheme: "https",
		EndpointHost:   "api.example.com",
		EndpointPort:   443,
		EndpointPath:   "/items",
	})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got) != 1 || got[0].Status != 401 {
		t.Fatalf("first endpoint page = %+v, want newest matching status 401", got)
	}

	got, err = s.QueryFlowsFilter(FlowFilter{
		Limit:          2,
		RequireFlags:   FlagRepeater,
		EndpointScheme: "https",
		EndpointHost:   "api.example.com",
		EndpointPort:   443,
		EndpointPath:   "/items",
	})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got) != 2 || got[0].Status != 401 || got[1].Status != 500 {
		t.Fatalf("endpoint history = %+v, want statuses [401 500]", got)
	}
}
