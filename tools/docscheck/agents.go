package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/version"
)

// The agent guide is the hand-written docs/agents.md plus a generated reference
// block between these markers. The same sources also produce the site-root
// llms.txt and llms-full.txt files.
const (
	agentSource  = "docs/agents.md"
	refBegin     = "<!-- docscheck:begin reference -->"
	refEnd       = "<!-- docscheck:end reference -->"
	llmsFile     = "llms.txt"
	llmsFullFile = "llms-full.txt"
)

// llmsFullSources are concatenated, in order, into llms-full.txt so an agent that
// fetches one URL gets the guide, the finding contract, and the API/MCP reference.
var llmsFullSources = []string{
	"docs/agents.md",
	"docs/findings-and-reporting.md",
	"docs/api-and-mcp.md",
	"docs/product/mcp-cookbook.md",
}

// llmsDescriptions is the one-line description of each public page in llms.txt.
// TestLLMSDescribesEveryPublicPage keeps it complete.
var llmsDescriptions = map[string]string{
	"ai-agents":              "Rules, install, MCP and REST setup, workflow, and the finding-writing contract for AI agents",
	"getting-started":        "Install, start, and configure Interseptor",
	"workspace":              "Tour of the workspace menus and response views",
	"findings-and-reporting": "Finding envelope, evidence rules, readiness gates, and report export",
	"api-and-mcp":            "MCP server, REST API, collections API, and finding endpoints",
	"mcp-cookbook":           "Task recipes for external agents using MCP tools",
	"collections":            "Request collections, environments, scripts, trust, and the runner",
	"proxy-and-tls":          "Proxy listeners, CA trust, TLS interception, and upstream chaining",
	"history-search":         "History filters, Anywhere search, and saved Starlark searches",
	"projects-and-data":      "Projects, imports, exports, archives, and retention",
	"settings":               "Where each setting lives",
	"engagement-closeout":    "Checklist for final delivery and cleanup",
	"mobile-testing":         "Android and iOS interception setup",
	"content-discovery":      "Forced browsing through the proxy",
	"vault":                  "Project vault for archive backups",
	"custom-checks":          "Authoring passive Starlark scanner checks",
	"rule-packs":             "Building, installing, and signing rule packs",
	"message-codecs":         "App-layer encrypt and decrypt codecs for captured bodies",
	"extensions":             "Extension points",
	"architecture":           "Architecture and security model",
	"cli-reference":          "Command-line flags and subcommands",
	"http2":                  "HTTP/2 behavior",
	"troubleshooting":        "Connection, certificate, rendering, and update problems",
	"benchmarks":             "Performance benchmarks",
}

var (
	liquidRaw       = regexp.MustCompile(`\{%\s*raw\s*%\}|\{%\s*endraw\s*%\}`)
	liquidVariable  = regexp.MustCompile(`\{\{[^}\n]*\}\}`)
	rawWrappedBlock = regexp.MustCompile(`(?s)\{%\s*raw\s*%\}.*?\{%\s*endraw\s*%\}`)
)

// liquidSafe protects text from Liquid, which would otherwise evaluate a
// double-brace variable in generated tables.
func liquidSafe(s string) string {
	return liquidVariable.ReplaceAllStringFunc(s, func(m string) string {
		return "{% raw %}" + m + "{% endraw %}"
	})
}

// validateLiquidSafe fails when a page would be evaluated as Liquid.
func validateLiquidSafe(body string) error {
	rest := rawWrappedBlock.ReplaceAllString(body, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "{%") {
		return errors.New("unprotected Liquid delimiter ({{ or {%): wrap in {% raw %}")
	}
	return nil
}

var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

// oneLine returns the first sentence of a description, flattened for a table
// cell and bounded in length.
func oneLine(desc string, max int) string {
	s := strings.Join(strings.Fields(desc), " ")
	if loc := sentenceEnd.FindStringIndex(s); loc != nil {
		s = strings.TrimSpace(s[:loc[0]+1])
	}
	if len(s) > max {
		cut := s[:max]
		if i := strings.LastIndex(cut, " "); i > max/2 {
			cut = cut[:i]
		}
		s = strings.TrimRight(cut, " ,;:") + "..."
	}
	return strings.ReplaceAll(s, "|", `\|`)
}

type toolRow struct{ Name, Required, Desc string }

func agentToolRows() []toolRow {
	srv := mcp.New("http://127.0.0.1:1")
	names := srv.ToolNames()
	sort.Strings(names)
	rows := make([]toolRow, 0, len(names))
	for _, name := range names {
		desc, schema, _ := srv.ToolMeta(name)
		req := "none"
		if list := requiredList(schema); len(list) > 0 {
			quoted := make([]string, len(list))
			for i, r := range list {
				quoted[i] = "`" + r + "`"
			}
			req = strings.Join(quoted, ", ")
		}
		rows = append(rows, toolRow{Name: name, Required: req, Desc: oneLine(desc, 110)})
	}
	return rows
}

func requiredList(schema map[string]any) []string {
	switch v := schema["required"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type routeRow struct{ Method, Path, Desc string }

func agentRouteRows() []routeRow {
	catalog := control.RouteCatalog()
	rows := make([]routeRow, 0, len(catalog))
	for _, r := range catalog {
		rows = append(rows, routeRow{Method: r.Method, Path: r.Path, Desc: oneLine(r.Desc, 110)})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		return rows[i].Method < rows[j].Method
	})
	return rows
}

type fieldRow struct{ Path, Type, Desc string }

// flattenSchema lists object properties recursively: arrays of objects add
// "name[]" and nested objects add "name.child".
func flattenSchema(prefix string, schema map[string]any, rows *[]fieldRow) {
	props, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		prop, _ := props[k].(map[string]any)
		typ, _ := prop["type"].(string)
		desc, _ := prop["description"].(string)
		path := prefix + k
		*rows = append(*rows, fieldRow{Path: path, Type: typ, Desc: oneLine(desc, 130)})
		if items, ok := prop["items"].(map[string]any); ok && items["properties"] != nil {
			flattenSchema(path+"[].", items, rows)
		}
		if prop["properties"] != nil {
			flattenSchema(path+".", prop, rows)
		}
	}
}

func agentFieldRows() []fieldRow {
	_, schema, _ := mcp.New("http://127.0.0.1:1").ToolMeta("create_finding")
	var rows []fieldRow
	flattenSchema("", schema, &rows)
	return rows
}

// agentReference renders the generated reference from the live registries.
func agentReference() string {
	var b strings.Builder
	tools := agentToolRows()
	routes := agentRouteRows()
	fields := agentFieldRows()

	fmt.Fprintf(&b, "### MCP tools (%d)\n\n", len(tools))
	b.WriteString("Generated from the MCP tool registry (stdio `interseptor mcp` and `POST /mcp`). Read a tool's JSON Schema with `tools/list`; the table shows the first sentence of its description and its required parameters.\n\n")
	b.WriteString("| Tool | Required | Purpose |\n|---|---|---|\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", t.Name, t.Required, t.Desc)
	}

	fmt.Fprintf(&b, "\n### Finding fields (%d)\n\n", len(fields))
	b.WriteString("Generated from the `create_finding` input schema. `update_finding` accepts the same fields plus a required `id`. Over REST, send the same names to `POST /api/findings` and `PATCH /api/findings/{id}`.\n\n")
	b.WriteString("| Field | Type | Meaning |\n|---|---|---|\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.Path, f.Type, f.Desc)
	}

	fmt.Fprintf(&b, "\n### REST routes (%d)\n\n", len(routes))
	b.WriteString("Generated from the route catalog served by `GET /api/reference`. Paths are relative to the control address (default `http://127.0.0.1:9966`). `{name}` is a path parameter.\n\n")
	b.WriteString("| Method | Path | Purpose |\n|---|---|---|\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n", r.Method, r.Path, r.Desc)
	}
	return liquidSafe(b.String())
}

// injectReference replaces the content between the reference markers.
func injectReference(doc, ref string) (string, error) {
	start := strings.Index(doc, refBegin)
	end := strings.Index(doc, refEnd)
	if start < 0 || end < 0 || end < start {
		return "", fmt.Errorf("%s must contain %q before %q", agentSource, refBegin, refEnd)
	}
	return doc[:start+len(refBegin)] + "\n" + ref + doc[end:], nil
}

// agentGuide returns docs/agents.md with its reference block regenerated.
func agentGuide(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, agentSource))
	if err != nil {
		return "", err
	}
	return injectReference(string(data), agentReference())
}

// readDoc returns a source document; the agent guide always carries a fresh reference.
func readDoc(root, source string) (string, error) {
	if source == agentSource {
		return agentGuide(root)
	}
	data, err := os.ReadFile(filepath.Join(root, source))
	return string(data), err
}

// rewriteLinksText makes every relative link absolute for plain-text output.
func rewriteLinksText(body, source string) string {
	return markdownLink.ReplaceAllStringFunc(body, func(link string) string {
		target := link[2 : len(link)-1]
		if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			return link
		}
		parts := strings.SplitN(target, "#", 2)
		resolved := pathClean(source, parts[0])
		fragment := ""
		if len(parts) == 2 {
			fragment = "#" + parts[1]
		}
		if meta, ok := publicDocs[resolved]; ok {
			return "](" + version.DocsSite + "/" + meta.Slug + "/" + fragment + ")"
		}
		kind := "blob"
		if strings.HasSuffix(parts[0], "/") || extOf(resolved) == "" {
			kind = "tree"
		}
		return "](" + repositoryURL + "/" + kind + "/main/" + resolved + fragment + ")"
	})
}

func plainText(body, source string) string {
	body = liquidRaw.ReplaceAllString(body, "")
	return strings.TrimRight(rewriteLinksText(body, source), "\n") + "\n"
}

func llmsFullText(root string) (string, error) {
	var b strings.Builder
	b.WriteString("# Interseptor: complete agent documentation\n\n")
	b.WriteString("> One file for AI agents: the agent guide, the finding-writing contract, and the API and MCP references. Generated from the project documentation by `go run ./tools/docscheck generate`; do not edit. Index: " + version.LLMSURL + "\n\n")
	b.WriteString("Parts, in order:\n\n")
	for _, source := range llmsFullSources {
		meta, ok := publicDocs[source]
		if !ok {
			return "", fmt.Errorf("llms-full source %s is not a public doc", source)
		}
		fmt.Fprintf(&b, "- %s (%s/%s/)\n", meta.Title, version.DocsSite, meta.Slug)
	}
	for _, source := range llmsFullSources {
		body, err := readDoc(root, source)
		if err != nil {
			return "", err
		}
		meta := publicDocs[source]
		fmt.Fprintf(&b, "\n---\n\nSource: %s/%s/\n\n%s", version.DocsSite, meta.Slug, plainText(body, source))
	}
	return b.String(), nil
}

// llmsSections groups public pages for llms.txt; every other public page is listed under Optional.
var llmsSections = []struct {
	Title string
	Slugs []string
}{
	{"Agent guide", []string{"ai-agents"}},
	{"Core guides", []string{"getting-started", "workspace", "findings-and-reporting", "api-and-mcp", "mcp-cookbook", "collections", "proxy-and-tls", "history-search", "projects-and-data", "engagement-closeout"}},
}

func llmsText() string {
	byslug := map[string]pageMeta{}
	for _, meta := range publicDocs {
		byslug[meta.Slug] = meta
	}
	var b strings.Builder
	b.WriteString("# Interseptor\n\n")
	b.WriteString("> Interseptor is a single-binary HTTP/HTTPS intercepting proxy and security-testing toolkit. AI agents drive the same engine as the web UI through an MCP server (stdio and Streamable HTTP) and a REST/SSE API. These pages explain how to install it, connect, follow the rules, and file findings in one consistent format.\n\n")
	b.WriteString("Fetch [llms-full.txt](" + version.LLMSFullURL + ") first: it is a single file containing the agent guide, the finding-writing contract, and the API and MCP references. Authorized testing only; read the hard rules before sending any request.\n\n")
	listed := map[string]bool{}
	line := func(slug string) {
		meta := byslug[slug]
		fmt.Fprintf(&b, "- [%s](%s/%s/): %s\n", meta.Title, version.DocsSite, slug, llmsDescriptions[slug])
		listed[slug] = true
	}
	for _, sec := range llmsSections {
		fmt.Fprintf(&b, "## %s\n\n", sec.Title)
		if sec.Title == "Agent guide" {
			fmt.Fprintf(&b, "- [Complete guide as one text file](%s): agent guide, finding contract, API and MCP reference\n", version.LLMSFullURL)
		}
		for _, slug := range sec.Slugs {
			line(slug)
		}
		b.WriteString("\n")
	}
	var rest []string
	for slug := range byslug {
		if !listed[slug] {
			rest = append(rest, slug)
		}
	}
	sort.Strings(rest)
	b.WriteString("## Optional\n\n")
	for _, slug := range rest {
		line(slug)
	}
	return b.String()
}

// writeAgentFiles regenerates the agent guide reference and both llms files.
func writeAgentFiles(root string) error {
	guide, err := agentGuide(root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, agentSource), []byte(guide), 0o644); err != nil {
		return err
	}
	full, err := llmsFullText(root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, llmsFullFile), []byte(full), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, llmsFile), []byte(llmsText()), 0o644)
}

// checkAgentFiles verifies the committed reference and llms files are current.
func checkAgentFiles(root string) error {
	want, err := agentGuide(root)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(filepath.Join(root, agentSource))
	if err != nil {
		return err
	}
	if string(got) != want {
		return fmt.Errorf("%s generated reference stale: run go run ./tools/docscheck generate", agentSource)
	}
	if err := validateLiquidSafe(want); err != nil {
		return fmt.Errorf("%s: %w", agentSource, err)
	}
	if err := validateAgentGuide(root, want); err != nil {
		return err
	}
	full, err := llmsFullText(root)
	if err != nil {
		return err
	}
	for name, content := range map[string]string{llmsFullFile: full, llmsFile: llmsText()} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("missing generated file %s: %w", name, err)
		}
		if string(data) != content {
			return fmt.Errorf("generated file stale: %s", name)
		}
	}
	return nil
}

// validateBuiltLLMS verifies the built site serves the llms files as raw text.
func validateBuiltLLMS(site, root string) error {
	for _, name := range []string{llmsFile, llmsFullFile} {
		built, err := os.ReadFile(filepath.Join(site, name))
		if err != nil {
			return fmt.Errorf("missing built %s: %w", name, err)
		}
		source, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("missing source %s: %w", name, err)
		}
		lower := strings.ToLower(string(built[:min(len(built), 512)]))
		if strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<html") {
			return fmt.Errorf("built %s is wrapped in HTML", name)
		}
		if !strings.HasPrefix(string(built), "# ") {
			return fmt.Errorf("built %s must start with a Markdown H1", name)
		}
		if string(built) != string(source) {
			return fmt.Errorf("built %s differs from the generated source", name)
		}
	}
	return nil
}

func pathClean(source, rel string) string { return path.Clean(path.Join(path.Dir(source), rel)) }

func extOf(p string) string { return path.Ext(p) }
