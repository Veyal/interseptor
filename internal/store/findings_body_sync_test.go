package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUpdateFindingBodySyncsFindingFlows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f1, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/a", Status: 200})
	f2, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Host: "example.com", Path: "/b", Status: 201})
	id, err := s.CreateFinding(&Finding{Title: "sync test", Detail: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachFlow(id, f1, "old", -1); err != nil {
		t.Fatal(err)
	}

	body := `[{"type":"text","md":"step 1"},{"type":"flow","flowId":` + fmtInt64(f2) + `,"note":"new poc"},{"type":"flow","flowId":` + fmtInt64(f1) + `,"note":"again"}]`
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateFinding: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("flows=%+v", got.Flows)
	}
	if got.Flows[0].FlowID != f2 || got.Flows[0].Note != "new poc" || got.Flows[0].Ord != 0 {
		t.Fatalf("flow[0]=%+v", got.Flows[0])
	}
	if got.Flows[1].FlowID != f1 || got.Flows[1].Note != "again" || got.Flows[1].Ord != 1 {
		t.Fatalf("flow[1]=%+v", got.Flows[1])
	}
	if got.Blocks[1].Missing || got.Blocks[1].Method != "POST" {
		t.Fatalf("block enrichment: %+v", got.Blocks[1])
	}
}

func TestUpdateFindingBodyRejectsUnknownFlowID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Title: "bad flow"})
	body := `[{"type":"flow","flowId":99999,"note":"ghost"}]`
	err = s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "flow") {
		t.Fatalf("want flow-not-found error, got %v", err)
	}
}

func TestNormalizeFindingBodyCoerceAndReject(t *testing.T) {
	out, err := NormalizeFindingBody(`[{"type":"md","md":"hello"},{"type":"markdown","md":"world"}]`)
	if err != nil {
		t.Fatal(err)
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(out), &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Type != "text" || recs[0].MD != "hello" || recs[1].Type != "text" {
		t.Fatalf("coerce=%+v", recs)
	}
	_, err = NormalizeFindingBody(`[{"type":"essay","md":"nope"}]`)
	if err == nil || !strings.Contains(err.Error(), "type must be") {
		t.Fatalf("want type error, got %v", err)
	}
}

func fmtInt64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
