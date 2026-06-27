package control

import "fmt"

// mcpHTTPClientConfig is the recommended Cursor / Streamable-HTTP MCP config.
// It talks to the running Interceptor process, so MCP always matches the version
// you have live on :9966 after restart (no separate stdio subprocess to refresh).
func mcpHTTPClientConfig(baseURL string) map[string]any {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9966"
	}
	cfg := map[string]any{
		"url": baseURL + "/mcp",
	}
	// Optional auth when API keys exist — set INTERCEPTOR_MCP_API_KEY in the env
	// block of your MCP client config if you created keys in Settings → API & MCP.
	return map[string]any{
		"mcpServers": map[string]any{
			"interceptor": cfg,
		},
	}
}

func mcpStdioClientConfig() map[string]any {
	return map[string]any{
		"mcpServers": map[string]any{
			"interceptor": map[string]any{
				"command": "interceptor",
				"args":    []string{"mcp"},
				"env": map[string]any{
					"INTERCEPTOR_CONTROL_URL": "http://127.0.0.1:9966",
				},
			},
		},
	}
}

func mcpDescriptorForRequest(host string) map[string]any {
	base := "http://" + host
	out := make(map[string]any, len(mcpDescriptor)+4)
	for k, v := range mcpDescriptor {
		out[k] = v
	}
	out["note"] = fmt.Sprintf(
		"Start Interceptor first (%s). Recommended for Cursor: paste clientConfig below — Streamable HTTP at /mcp uses the running process and updates when you restart after `interceptor update`. stdioClientConfig spawns `interceptor mcp` separately; on Windows use scripts/interceptor-mcp.cmd to resolve the latest binary on PATH.",
		base,
	)
	out["clientConfig"] = mcpHTTPClientConfig(base)
	out["stdioClientConfig"] = mcpStdioClientConfig()
	out["httpTransport"] = map[string]any{
		"type": "streamable-http",
		"url":  base + "/mcp",
		"note": "Same tools as stdio. Loopback-only. If you created API keys, add Authorization: Bearer <key> to your MCP client headers.",
	}
	return out
}
