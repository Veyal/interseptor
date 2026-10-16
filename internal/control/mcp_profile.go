package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/mcp"
)

// mcpProfileForRequest resolves which MCP tool profile a /mcp request gets.
// Precedence is explicit over implicit, and an unknown name is an error rather
// than a silent fallback: a client that asks for a profile it misspelled should
// hear about it, not quietly receive all 133 tools.
//
//  1. ?profile= on the request
//  2. INTERSEPTOR_MCP_PROFILE in the server's environment
//  3. full
func mcpProfileForRequest(r *http.Request) (mcp.Profile, error) {
	if !r.URL.Query().Has("profile") {
		return mcp.ProfileFromEnv(), nil
	}
	return mcp.ParseProfile(r.URL.Query().Get("profile"))
}
