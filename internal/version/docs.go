package version

// DocsSite is the published documentation root. It must equal the Jekyll
// `url` plus `baseurl` in _config.yml (tools/docscheck verifies this).
const DocsSite = "https://veyal.github.io/interseptor"

// Machine-readable entry points for AI agents. llms.txt and llms-full.txt are
// generated from the documentation sources by `go run ./tools/docscheck generate`.
const (
	AgentGuideURL = DocsSite + "/ai-agents/"
	LLMSURL       = DocsSite + "/llms.txt"
	LLMSFullURL   = DocsSite + "/llms-full.txt"
)

// Docs returns the agent documentation pointers advertised by the MCP server,
// GET /api/mcp, and GET /api/mcp/capabilities.
func Docs() map[string]string {
	return map[string]string{
		"agentGuide":  AgentGuideURL,
		"llmsTxt":     LLMSURL,
		"llmsFullTxt": LLMSFullURL,
	}
}
