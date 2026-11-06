package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// playbookStub serves the two control endpoints the resource layer reads.
func playbookStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/playbooks":
			_ = json.NewEncoder(w).Encode(map[string]any{"playbooks": []map[string]any{
				{"id": "object-authz", "title": "Object-level authorization", "trusted": true, "source": "bundled", "appliesTo": []string{"param:id"}},
				{"id": "house", "title": "House rules", "trusted": false, "source": "project", "severityHint": "High"},
			}})
		case r.URL.Path == "/api/playbooks/object-authz":
			_ = json.NewEncoder(w).Encode(map[string]any{"markdown": "# Object-level authorization\n\nSwap the id.\n"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rpc(t *testing.T, s *Server, method string, params any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(params)
	res, rerr := s.dispatch(method, raw)
	if rerr != nil {
		t.Fatalf("%s: %v", method, rerr.Message)
	}
	out, _ := res.(map[string]any)
	return out
}

func TestResourcesListExposesPlaybooks(t *testing.T) {
	s := New(playbookStub(t).URL)
	out := rpc(t, s, "resources/list", map[string]any{})
	list, _ := out["resources"].([]any)
	if len(list) != 2 {
		t.Fatalf("resources = %+v", out)
	}
	first, _ := list[0].(map[string]any)
	if first["uri"] != "playbook://object-authz" || first["mimeType"] != "text/markdown" {
		t.Fatalf("resource = %+v", first)
	}
	second, _ := list[1].(map[string]any)
	if !strings.Contains(second["description"].(string), "untrusted") {
		t.Fatalf("a project playbook must be described as untrusted: %+v", second)
	}
}

func TestResourcesReadReturnsMarkdown(t *testing.T) {
	s := New(playbookStub(t).URL)
	out := rpc(t, s, "resources/read", map[string]any{"uri": "playbook://object-authz"})
	contents, _ := out["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %+v", out)
	}
	c, _ := contents[0].(map[string]any)
	if !strings.Contains(c["text"].(string), "# Object-level authorization") {
		t.Fatalf("text = %q", c["text"])
	}
	// An unknown uri is empty, not an error: clients probe resources.
	if got := rpc(t, s, "resources/read", map[string]any{"uri": "playbook://nope"}); len(got["contents"].([]any)) != 0 {
		t.Fatalf("unknown uri = %+v", got)
	}
}

func TestPromptsListOnePerPlaybook(t *testing.T) {
	s := New(playbookStub(t).URL)
	out := rpc(t, s, "prompts/list", map[string]any{})
	prompts, _ := out["prompts"].([]any)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %+v", out)
	}
	first, _ := prompts[0].(map[string]any)
	if first["name"] != "playbook-object-authz" {
		t.Fatalf("prompt = %+v", first)
	}
}

func TestPromptGetCarriesThePlaybook(t *testing.T) {
	s := New(playbookStub(t).URL)
	out := rpc(t, s, "prompts/get", map[string]any{"name": "playbook-object-authz"})
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", out)
	}
	m, _ := msgs[0].(map[string]any)
	content, _ := m["content"].(map[string]any)
	if m["role"] != "user" || !strings.Contains(content["text"].(string), "Swap the id") {
		t.Fatalf("message = %+v", m)
	}
	if got := rpc(t, s, "prompts/get", map[string]any{"name": "nope"}); got != nil {
		t.Fatalf("unknown prompt = %+v", got)
	}
}
