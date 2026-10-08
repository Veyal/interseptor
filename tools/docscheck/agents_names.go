package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
)

// The hand-written part of the agent guide may only name things that exist in
// the code: MCP tools, finding fields, REST routes, and CLI flags.

var (
	inlineCode   = regexp.MustCompile("`([^`\n]+)`")
	snakeToken   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$`)
	camelToken   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)+$`)
	snakeInText  = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)
	camelInText  = regexp.MustCompile(`[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)+`)
	methodRoute  = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) (/\S*)$`)
	fenceJSON    = regexp.MustCompile("(?s)```json\n(.*?)```")
	fenceAny     = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	fencedRoute  = regexp.MustCompile(`(?:\$C|127\.0\.0\.1:\d+)(/[A-Za-z0-9_/{}.\-]+)`)
	flagInText   = regexp.MustCompile(`(?:^|[\s"'(])(--[a-z][a-z0-9-]+)`)
	pathParamRe  = regexp.MustCompile(`\{[^}]*\}`)
	numericSeg   = regexp.MustCompile(`^[0-9]+$`)
	generatedRef = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(refBegin) + `.*?` + regexp.QuoteMeta(refEnd))
)

// guideExtraNames are real names the registries do not expose as schema
// properties or description text. Keep each entry verifiable in the code.
var guideExtraNames = map[string]string{
	"schemaVersion":     "internal/mcp/mcp.go",
	"schemaHash":        "internal/mcp/mcp.go",
	"apiVersion":        "internal/mcp/mcp.go",
	"reconnectRequired": "internal/mcp/mcp.go",
	"clientConfig":      "internal/control/mcpconfig.go",
	"stdioClientConfig": "internal/control/mcpconfig.go",
	"searchNote":        "internal/control/control.go",
	"auth_identities":   "internal/control/readiness.go",
	"login_macro":       "internal/control/readiness.go",
	// Third-party client vocabulary, not Interseptor names.
	"mcpServers":     "",
	"streamableHttp": "",
	// Readiness stages (internal/store/findings.go ReadinessSummary).
	"draft":             "",
	"evidence_attached": "internal/store/findings.go",
	"reproducible":      "",
	"report_ready":      "internal/store/findings.go",
}

// guideClientFlags are flags of other programs the guide shows (Claude Code, Codex, Gemini CLI).
var guideClientFlags = map[string]bool{
	"--transport": true, "--header": true, "--url": true, "--bearer-token-env-var": true,
}

func collectSchemaNames(schema map[string]any, names map[string]bool, text *strings.Builder) {
	if d, ok := schema["description"].(string); ok {
		text.WriteString(d + "\n")
	}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range props {
		names[k] = true
		if sub, ok := v.(map[string]any); ok {
			collectSchemaNames(sub, names, text)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		collectSchemaNames(items, names, text)
	}
}

func collectJSONTags(t reflect.Type, seen map[reflect.Type]bool, names map[string]bool) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
			names[tag] = true
		}
		collectJSONTags(f.Type, seen, names)
	}
}

// knownGuideNames returns every identifier-like name the code defines, plus the
// text corpus of all descriptions (which carries enumerations).
func knownGuideNames() (names map[string]bool, corpus string) {
	names = map[string]bool{}
	var text strings.Builder
	srv := mcp.New("http://127.0.0.1:1")
	for _, tool := range srv.ToolNames() {
		names[tool] = true
		desc, schema, _ := srv.ToolMeta(tool)
		text.WriteString(desc + "\n")
		collectSchemaNames(schema, names, &text)
	}
	for _, r := range control.RouteCatalog() {
		text.WriteString(r.Desc + "\n")
	}
	seen := map[reflect.Type]bool{}
	for _, v := range []any{store.Finding{}, store.FindingReadiness{}, store.FindingBlock{}, store.FindingTarget{}, store.FindingProofReview{}, store.FindingClaim{}, store.FindingNotExecuted{}, store.FindingRelation{}, store.FindingCapabilityClaim{}} {
		collectJSONTags(reflect.TypeOf(v), seen, names)
	}
	corpus = text.String()
	for _, m := range snakeInText.FindAllString(corpus, -1) {
		names[m] = true
	}
	for _, m := range camelInText.FindAllString(corpus, -1) {
		names[m] = true
	}
	for k := range guideExtraNames {
		names[k] = true
	}
	for _, code := range store.FindingGapCodes() {
		names[code] = true
	}
	return names, corpus
}

func normalizeRoutePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return pathParamRe.ReplaceAllString(p, "{}")
}

func routePatterns() (exact map[string]bool, regexes []*regexp.Regexp) {
	exact = map[string]bool{}
	for _, r := range control.RouteCatalog() {
		norm := normalizeRoutePath(r.Path)
		exact[r.Method+" "+norm] = true
		exact[norm] = true
		parts := strings.Split(norm, "/")
		for i, p := range parts {
			if p == "{}" {
				parts[i] = `[^/]+`
			} else {
				parts[i] = regexp.QuoteMeta(p)
			}
		}
		regexes = append(regexes, regexp.MustCompile("^"+strings.Join(parts, "/")+"$"))
	}
	return exact, regexes
}

func cliReferenceFlags(root string) map[string]bool {
	flags := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(root, "docs", "cli-reference.md"))
	if err != nil {
		return flags
	}
	for _, m := range regexp.MustCompile(`--[a-z][a-z0-9-]+`).FindAllString(string(data), -1) {
		flags[m] = true
	}
	return flags
}

// walkAgainstSchema checks that every object key in value is defined by schema.
func walkAgainstSchema(where string, schema map[string]any, value any) []string {
	var problems []string
	switch v := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			return nil
		}
		for key, child := range v {
			sub, ok := props[key].(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s.%s is not a field of the schema", where, key))
				continue
			}
			problems = append(problems, walkAgainstSchema(where+"."+key, sub, child)...)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return nil
		}
		for i, child := range v {
			problems = append(problems, walkAgainstSchema(fmt.Sprintf("%s[%d]", where, i), items, child)...)
		}
	}
	return problems
}

// validateAgentGuide returns an error listing every name in the hand-written
// guide that does not exist in the code.
func validateAgentGuide(root, body string) error {
	hand := generatedRef.ReplaceAllString(body, "")
	names, _ := knownGuideNames()
	exact, regexes := routePatterns()
	flags := cliReferenceFlags(root)
	var problems []string

	for _, m := range inlineCode.FindAllStringSubmatch(hand, -1) {
		tok := strings.TrimSpace(m[1])
		switch {
		case methodRoute.MatchString(tok):
			sub := methodRoute.FindStringSubmatch(tok)
			if !exact[sub[1]+" "+normalizeRoutePath(sub[2])] {
				problems = append(problems, "unknown REST route "+tok)
			}
		case snakeToken.MatchString(tok), camelToken.MatchString(tok):
			if !names[tok] {
				problems = append(problems, "unknown tool, field, or value `"+tok+"`")
			}
		}
	}

	for _, m := range fenceAny.FindAllStringSubmatch(hand, -1) {
		for _, r := range fencedRoute.FindAllStringSubmatch(m[1], -1) {
			p := strings.TrimRight(normalizeRoutePath(r[1]), ".")
			ok := exact[p]
			for _, re := range regexes {
				if re.MatchString(p) {
					ok = true
				}
			}
			if !ok {
				// "$C/api/flows/12/raw": numeric segments were already matched by {} patterns.
				problems = append(problems, "unknown REST path in a code block: "+r[1])
			}
		}
	}

	for _, m := range flagInText.FindAllStringSubmatch(hand, -1) {
		if !flags[m[1]] && !guideClientFlags[m[1]] {
			problems = append(problems, "unknown flag "+m[1]+" (not in docs/cli-reference.md)")
		}
	}

	srv := mcp.New("http://127.0.0.1:1")
	for _, m := range fenceJSON.FindAllStringSubmatch(hand, -1) {
		var call struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(m[1]), &call); err != nil || call.Name == "" {
			continue
		}
		_, schema, ok := srv.ToolMeta(call.Name)
		if !ok {
			problems = append(problems, "example calls unknown tool "+call.Name)
			continue
		}
		problems = append(problems, walkAgainstSchema(call.Name, schema, call.Arguments)...)
		for _, req := range requiredList(schema) {
			if _, present := call.Arguments[req]; !present {
				problems = append(problems, fmt.Sprintf("example %s lacks required argument %s", call.Name, req))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	var uniq []string
	for i, p := range problems {
		if i == 0 || p != problems[i-1] {
			uniq = append(uniq, p)
		}
	}
	return fmt.Errorf("%s names things that do not exist in the code:\n%s", agentSource, strings.Join(uniq, "\n"))
}
