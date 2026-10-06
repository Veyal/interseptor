package mcp

// scalarTargetNotice is appended to create/update finding results when the
// caller sent only the legacy scalar `target`. A client whose cached tool
// schema predates `targets` cannot send it, so the server says so explicitly
// instead of letting the call look fully successful.
const scalarTargetNotice = "\n\nCOMPATIBILITY: this Interseptor server supports structured `targets` (method, url, role, relation, flow_ids per affected endpoint), " +
	"but this call sent only the legacy scalar `target`. If your tool schema has no `targets` parameter it is stale: restart or reconnect the MCP client to reload tools/list."

// scalarTargetCompatNotice returns the compatibility notice when the arguments
// carry a scalar target without a structured targets list.
func scalarTargetCompatNotice(a map[string]any) string {
	if argStr(a, "target") == "" {
		return ""
	}
	if _, ok := a["targets"]; ok {
		return ""
	}
	return scalarTargetNotice
}
