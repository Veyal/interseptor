package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// guardHAR has one entry per scheme case plus one with an empty method.
const guardHAR = `{"log":{"version":"1.2","entries":[
 {"startedDateTime":"2024-01-01T00:00:00Z","request":{"method":"GET","url":"ftp://example.com/file","httpVersion":"HTTP/1.1","headers":[]},"response":{"status":200,"headers":[],"content":{}}},
 {"startedDateTime":"2024-01-01T00:00:00Z","request":{"method":"GET","url":"javascript://example.com/x","httpVersion":"HTTP/1.1","headers":[]},"response":{"status":200,"headers":[],"content":{}}},
 {"startedDateTime":"2024-01-01T00:00:00Z","request":{"method":"","url":"https://example.com/empty-method","httpVersion":"HTTP/1.1","headers":[]},"response":{"status":200,"headers":[],"content":{}}}
]}}`

func TestImportHARAcceptsOnlyHTTPAndDefaultsMethod(t *testing.T) {
	h, st, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPost, "/api/import/har", bytes.NewReader([]byte(guardHAR)))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importHAR(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Imported int }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Imported != 1 {
		t.Fatalf("imported %d, want only the https entry", resp.Imported)
	}
	flows, err := st.QueryFlowsFilter(store.FlowFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].Scheme != "https" || flows[0].Method != "GET" {
		t.Fatalf("flows = %+v, want one https GET", flows)
	}
}

func TestImportProjectHARAcceptsOnlyHTTPAndDefaultsMethod(t *testing.T) {
	h, st, _ := newHub(t)
	bundle, err := json.Marshal(projectBundle{Version: "1", HAR: json.RawMessage(guardHAR)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(bundle))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	flows, err := st.QueryFlowsFilter(store.FlowFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].Scheme != "https" || flows[0].Method != "GET" {
		t.Fatalf("flows = %+v, want one https GET", flows)
	}
}
