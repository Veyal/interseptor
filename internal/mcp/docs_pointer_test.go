package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/version"
)

// Agents discover the generated guide from the MCP connect instructions and from
// the capabilities payload; both must carry the published llms.txt URLs.
func TestMCPInstructionsPointToAgentGuide(t *testing.T) {
	instr := mcpInstructions()
	for _, want := range []string{version.LLMSURL, version.LLMSFullURL} {
		if !strings.Contains(instr, want) {
			t.Fatalf("mcpInstructions missing %q", want)
		}
	}
	srv := New("http://127.0.0.1:1")
	res, rpcErr := srv.dispatch("initialize", json.RawMessage(`{"protocolVersion":"2024-11-05"}`))
	if rpcErr != nil {
		t.Fatalf("initialize: %+v", rpcErr)
	}
	got, _ := res.(map[string]any)["instructions"].(string)
	if !strings.Contains(got, version.LLMSFullURL) {
		t.Fatalf("initialize instructions missing %q", version.LLMSFullURL)
	}
}

func TestMCPCapabilitiesAdvertiseDocumentation(t *testing.T) {
	docs, ok := New("http://127.0.0.1:1").Capabilities()["documentation"].(map[string]string)
	if !ok {
		t.Fatalf("capabilities.documentation missing or wrong type")
	}
	for key, want := range map[string]string{"agentGuide": version.AgentGuideURL, "llmsTxt": version.LLMSURL, "llmsFullTxt": version.LLMSFullURL} {
		if docs[key] != want {
			t.Fatalf("documentation[%s] = %q, want %q", key, docs[key], want)
		}
	}
}
