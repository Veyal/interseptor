package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// POST /api/flows/delete removes the listed flows and reports the count; they
// then disappear from History.
func TestDeleteFlowsEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x", Status: 200})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"ids": ids[:2]})
	resp, err := http.Post(ts.URL+"/api/flows/delete", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d, want 200", resp.StatusCode)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Deleted != 2 {
		t.Fatalf("deleted %d, want 2", out.Deleted)
	}

	fresp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatal(err)
	}
	defer fresp.Body.Close()
	var fl struct {
		Flows []map[string]any `json:"flows"`
	}
	json.NewDecoder(fresp.Body).Decode(&fl)
	if len(fl.Flows) != 1 {
		t.Fatalf("remaining flows = %d, want 1", len(fl.Flows))
	}
}

func TestDeleteFlowsRejectsTrailingJSONBeforeMutation(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/flows/delete", "application/json", strings.NewReader(`{"ids":[`+strconv.FormatInt(id, 10)+`]}{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if n := flowCount(t, ts.URL+"/api/flows"); n != 1 {
		t.Fatalf("flows after rejected delete = %d, want 1", n)
	}
}
