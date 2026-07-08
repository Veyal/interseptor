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

// PUT /api/flows/{id}/note attaches a note that then appears in the flow detail
// DTO, so the operator (or AI) can annotate a request/response.
func TestFlowNoteEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "h", Path: "/x", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	idStr := strconv.FormatInt(id, 10)

	body, _ := json.Marshal(map[string]string{"note": "check for IDOR"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/flows/"+idStr+"/note", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT note: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT note status %d, want 2xx", resp.StatusCode)
	}

	dresp, err := http.Get(ts.URL + "/api/flows/" + idStr)
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	var detail map[string]any
	json.NewDecoder(dresp.Body).Decode(&detail)
	if detail["note"] != "check for IDOR" {
		t.Fatalf("flow detail note = %v, want %q", detail["note"], "check for IDOR")
	}
}
