package store

import "testing"

// A second process (the headless `interseptor run` CLI) can write flows into
// a project a live instance has open; the instance's in-memory id counter then
// hands out an id the CLI already used. An auto-allocated insert must resync
// to the table and retry instead of losing the flow.
func TestInsertFlowSurvivesIDTakenByAnotherWriter(t *testing.T) {
	dir := t.TempDir()
	live, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	for i := 0; i < 3; i++ {
		if _, err := other.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/other"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &Flow{Method: "GET", Host: "example.com", Path: "/live"}
	id, err := live.InsertFlow(f)
	if err != nil {
		t.Fatalf("insert after another writer took the id: %v", err)
	}
	if id <= 3 || f.ID != id {
		t.Fatalf("flow id = %d (f.ID %d), want an id past the other writer's rows", id, f.ID)
	}
	if _, err := live.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/live2"}); err != nil {
		t.Fatalf("the counter must stay in step after the resync: %v", err)
	}
}
