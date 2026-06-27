package store

import (
	"testing"
	"time"
)

func TestFlowListSortIdAscPagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		ids = append(ids, id)
	}
	page1, err := s.QueryFlowsListFilter(FlowFilter{Limit: 2, SortKey: "id", SortDir: 1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != ids[0] || page1[1].ID != ids[1] {
		t.Fatalf("page1 = %+v want %v,%v", flowIDs(page1), ids[0], ids[1])
	}
	last := page1[len(page1)-1]
	page2, err := s.QueryFlowsListFilter(FlowFilter{
		Limit: 2, SortKey: "id", SortDir: 1,
		CursorID: last.ID, CursorVal: FlowSortValue(last, "id"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != ids[2] || page2[1].ID != ids[3] {
		t.Fatalf("page2 = %+v want %v,%v", flowIDs(page2), ids[2], ids[3])
	}
}

func TestFlowListSortIdDescBeforeID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var ids []int64
	for i := 0; i < 4; i++ {
		id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		ids = append(ids, id)
	}
	all, err := s.QueryFlowsListFilter(FlowFilter{Limit: 2, SortKey: "id", SortDir: -1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	page2, err := s.QueryFlowsListFilter(FlowFilter{Limit: 2, BeforeID: all[len(all)-1].ID})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != ids[1] || page2[1].ID != ids[0] {
		t.Fatalf("page2 = %+v want %v,%v", flowIDs(page2), ids[1], ids[0])
	}
}

func flowIDs(flows []*Flow) []int64 {
	out := make([]int64, len(flows))
	for i, f := range flows {
		out[i] = f.ID
	}
	return out
}
