package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func Test_ListFlows_normalizes_missing_and_legacy_search_scope_to_anywhere(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "missing scope", query: ""},
		{name: "legacy path alias", query: "searchScope=path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			query, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			h, _, _ := newHub(t)
			server := httptest.NewServer(h.Handler())
			t.Cleanup(server.Close)

			// When
			response, err := http.Get(server.URL + "/api/flows?search=missing&" + query.Encode())
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()

			// Then
			if response.Header.Get("X-Interseptor-Search-Scope") != "anywhere" {
				t.Fatalf("normalized scope = %q, want anywhere", response.Header.Get("X-Interseptor-Search-Scope"))
			}
		})
	}
}

func Test_ListFlows_body_scope_excludes_metadata_matches(t *testing.T) {
	// Given
	h, s, _ := newHub(t)
	bodyID, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/body", Status: http.StatusOK, ReqBodyHash: writeFlowSearchTestBody(t, s, "needle-body")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "needle-metadata.example.com", Path: "/path", Status: http.StatusOK})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)

	// When
	response, err := http.Get(server.URL + "/api/flows?search=needle-body&searchScope=body")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Flows []flowJSON `json:"flows"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Then
	if len(result.Flows) != 1 || result.Flows[0].ID != bodyID {
		t.Fatalf("body search flows = %+v, want body flow %d", result.Flows, bodyID)
	}
}

func writeFlowSearchTestBody(t *testing.T, s *store.Store, body string) string {
	t.Helper()
	writer, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	hash, _, err := writer.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func Test_ListFlows_applies_has_note_before_body_search_candidate_cap(t *testing.T) {
	// Given
	h, s, _ := newHub(t)
	bodyHash := writeFlowSearchTestBody(t, s, "displaced-needle")
	displacedID, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/noted", Status: http.StatusOK, ReqBodyHash: bodyHash})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetFlowNote(displacedID, "keep"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFlowScriptCandidates; i++ {
		if _, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 2)), Method: "POST", Scheme: "https", Host: "example.com", Path: "/recent", Status: http.StatusOK, ReqBodyHash: bodyHash}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)

	// When
	response, err := http.Get(server.URL + "/api/flows?search=displaced-needle&searchScope=body&hasNote=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Flows []flowJSON `json:"flows"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Then
	if len(result.Flows) != 1 || result.Flows[0].ID != displacedID {
		t.Fatalf("flows = %+v, want displaced noted flow %d", result.Flows, displacedID)
	}
}

func Test_ListFlows_anywhere_search_reaches_beyond_saved_search_candidate_cap(t *testing.T) {
	// Given
	h, s, _ := newHub(t)
	matchedID, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/needle", Status: http.StatusOK})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFlowScriptCandidates; i++ {
		if _, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 2)), Method: "GET", Scheme: "https", Host: "example.com", Path: "/recent", Status: http.StatusOK}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)

	// When
	response, err := http.Get(server.URL + "/api/flows?search=needle")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Flows []flowJSON `json:"flows"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Then
	if len(result.Flows) != 1 || result.Flows[0].ID != matchedID {
		t.Fatalf("flows = %+v, want anywhere match %d beyond saved-search cap", result.Flows, matchedID)
	}
}

func Test_SaveFlowSearch_compiles_before_persisting(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "missing match", script: `x = 1`},
		{name: "non bool", script: `def match(flow): return "yes"`},
		{name: "load disabled", script: "load(\"other.star\", \"x\")\ndef match(flow): return True"},
		{name: "module step limit", script: "x = [i for i in range(1000000000)]\ndef match(flow): return True"},
		{name: "runtime step limit", script: "def match(flow):\n    for i in range(1000000000): pass\n    return True"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			h, _, _ := newHub(t)
			server := httptest.NewServer(h.Handler())
			t.Cleanup(server.Close)
			payload, err := json.Marshal(map[string]any{"name": tc.name, "scope": "anywhere", "script": tc.script})
			if err != nil {
				t.Fatal(err)
			}

			// When
			response, err := http.Post(server.URL+"/api/flow-searches", "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()

			// Then
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want compile rejection", response.StatusCode)
			}
		})
	}
}

func Test_TestFlowSearch_matches_selected_flow(t *testing.T) {
	// Given
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{Method: "GET", Host: "api.example.com", Path: "/health", Status: http.StatusOK})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	payload := []byte(`{"name":"selected","scope":"anywhere","flowId":` + strconv.FormatInt(flowID, 10) + `,"script":"def match(flow): return flow.host == 'api.example.com'"}`)

	// When
	response, err := http.Post(server.URL+"/api/flow-searches/test", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Matched bool  `json:"matched"`
		FlowID  int64 `json:"flowId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Then
	if response.StatusCode != http.StatusOK || !result.Matched || result.FlowID != flowID {
		t.Fatalf("test response = status %d, %+v; want selected matching flow", response.StatusCode, result)
	}
}

func Test_TestFlowSearch_rejects_missing_selected_flow(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	payload := []byte(`{"name":"missing","scope":"anywhere","flowId":999999,"script":"def match(flow): return True"}`)

	// When
	response, err := http.Post(server.URL+"/api/flow-searches/test", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	// Then
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
}

func Test_SavedFlowSearch_uses_typed_flow_and_project_scope(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	payload := []byte(`{"name":"typed","scope":"anywhere","script":"def match(flow): return flow.host == 'example.com' and flow.status == 200 and flow.req_header('X-Test') == 'needle'"}`)

	// When
	response, err := http.Post(server.URL+"/api/flow-searches", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	// Then
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want project-scoped saved search", response.StatusCode)
	}
}
