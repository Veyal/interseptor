package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImportPostmanReturnsRepeaterRequestsWithoutCreatingHistory(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	payload := []byte(`{
  "collection": {
    "info": {"name": "Example API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
    "variable": [{"key": "baseUrl", "value": "https://example.com"}],
    "item": [{"name": "Read", "request": {"url": "{{baseUrl}}/read"}}]
  },
  "environment": {
    "values": [{"key": "unused", "value": "value", "enabled": true}]
  }
}`)
	resp, err := http.Post(ts.URL+"/api/import/postman", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("import Postman: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var result struct {
		Name     string `json:"name"`
		Requests []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
		} `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Name != "Example API" || len(result.Requests) != 1 || result.Requests[0].Method != "GET" || result.Requests[0].URL != "https://example.com/read" {
		t.Fatalf("result = %+v", result)
	}
	flows, err := st.QueryFlows(10)
	if err != nil {
		t.Fatalf("QueryFlows: %v", err)
	}
	if len(flows) != 0 {
		t.Fatalf("Postman import created %d history flows", len(flows))
	}
}

func TestImportPostmanRejectsMalformedCollection(t *testing.T) {
	h, _, _ := newHub(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/import/postman", bytes.NewBufferString(`{"info":{}}`))
	(&projectAPI{h}).importPostman(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
