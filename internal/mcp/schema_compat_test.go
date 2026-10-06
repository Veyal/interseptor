package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// A server upgrade that adds a new create_finding input field must change the
// schema hash and capabilities, and a client holding the pre-upgrade schema
// must be told to reconnect instead of silently losing the field.
func TestMCPUpgradeAddingInputFieldInvalidatesStaleClient(t *testing.T) {
	old := New("http://127.0.0.1:1")
	staleHash := old.SchemaHash()

	upgraded := New("http://127.0.0.1:1")
	props := upgraded.tools["create_finding"].schema["properties"].(map[string]any)
	props["newExampleField"] = map[string]any{"type": "string"}

	if upgraded.SchemaHash() == staleHash {
		t.Fatal("adding an input field did not change the schema hash")
	}
	finding := upgraded.Capabilities()["finding"].(map[string]any)
	if !slices.Contains(finding["createFields"].([]string), "newExampleField") {
		t.Fatalf("capabilities omit the new field: %+v", finding)
	}
	_, rpcErr := upgraded.dispatch("initialize", json.RawMessage(`{"protocolVersion":"2024-11-05","schemaHash":"`+staleHash+`"}`))
	if rpcErr == nil || !strings.Contains(strings.ToLower(rpcErr.Message), "reconnect") {
		t.Fatalf("stale client accepted: %+v", rpcErr)
	}
	data, _ := rpcErr.Data.(map[string]any)
	if data["reconnectRequired"] != true || data["serverSchemaHash"] != upgraded.SchemaHash() {
		t.Fatalf("mismatch data: %+v", rpcErr.Data)
	}
	if _, rpcErr := upgraded.dispatch("initialize", json.RawMessage(`{"protocolVersion":"2024-11-05","schemaHash":"`+upgraded.SchemaHash()+`"}`)); rpcErr != nil {
		t.Fatalf("current client rejected: %+v", rpcErr)
	}
}

func TestMCPScalarTargetGetsTargetsCompatibilityNotice(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":42,"ready":false}`))
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	call := func(tool, args string) string {
		var out strings.Builder
		req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}` + "\n"
		if err := s.Serve(strings.NewReader(req), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	for tool, args := range map[string]string{
		"update_finding": `{"id":42,"target":"https://example.com/a"}`,
		"create_finding": `{"title":"Example","target":"https://example.com/a"}`,
	} {
		got := call(tool, args)
		if !strings.Contains(got, "targets") || !strings.Contains(strings.ToLower(got), "restart") {
			t.Fatalf("%s: no compatibility notice: %s", tool, got)
		}
	}
	got := call("update_finding", `{"id":42,"target":"https://example.com/a","targets":[{"url":"https://example.com/a"}]}`)
	if strings.Contains(strings.ToLower(got), "restart") {
		t.Fatalf("notice shown although targets was sent: %s", got)
	}
}
