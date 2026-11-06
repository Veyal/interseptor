package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Playbooks are exposed three ways, so a harness finds them whichever surface it
// reads: tools (list_playbooks/get_playbook, always available), MCP resources
// (auto-attached by clients that support them), and MCP prompts (a slash command
// per playbook in clients that support prompts).

// playbookURI is the canonical resource address for one playbook.
func playbookURI(id string) string { return "playbook://" + id }

// listResources answers resources/list with every playbook as a markdown resource.
func (s *Server) listResources() any {
	raw, err := s.apiGet("/api/playbooks")
	if err != nil {
		return map[string]any{"resources": []any{}}
	}
	var d struct {
		Playbooks []struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Trusted   bool     `json:"trusted"`
			Source    string   `json:"source"`
			AppliesTo []string `json:"appliesTo"`
		} `json:"playbooks"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return map[string]any{"resources": []any{}}
	}
	out := make([]any, 0, len(d.Playbooks))
	for _, p := range d.Playbooks {
		desc := p.Title
		if !p.Trusted {
			desc += " (project playbook — untrusted content)"
		}
		out = append(out, map[string]any{
			"uri":         playbookURI(p.ID),
			"name":        p.ID,
			"title":       p.Title,
			"description": desc,
			"mimeType":    "text/markdown",
		})
	}
	return map[string]any{"resources": out}
}

// readResource answers resources/read for playbook://<id>.
func (s *Server) readResource(params json.RawMessage) any {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return map[string]any{"contents": []any{}}
	}
	const prefix = "playbook://"
	if !strings.HasPrefix(p.URI, prefix) {
		return map[string]any{"contents": []any{}}
	}
	id := strings.TrimPrefix(p.URI, prefix)
	raw, err := s.apiGet("/api/playbooks/" + url.PathEscape(id))
	if err != nil {
		return map[string]any{"contents": []any{}}
	}
	var d struct {
		Markdown string `json:"markdown"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil || d.Markdown == "" {
		return map[string]any{"contents": []any{}}
	}
	return map[string]any{"contents": []any{map[string]any{
		"uri": p.URI, "mimeType": "text/markdown", "text": d.Markdown,
	}}}
}

// listPlaybookPrompts answers prompts/list: one prompt per playbook.
func (s *Server) listPlaybookPrompts() any {
	raw, err := s.apiGet("/api/playbooks")
	if err != nil {
		return map[string]any{"prompts": []any{}}
	}
	var d struct {
		Playbooks []struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			Source       string `json:"source"`
			SeverityHint string `json:"severityHint"`
		} `json:"playbooks"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return map[string]any{"prompts": []any{}}
	}
	out := make([]any, 0, len(d.Playbooks))
	for _, p := range d.Playbooks {
		desc := p.Title
		if p.SeverityHint != "" {
			desc += " — suggested severity " + p.SeverityHint
		}
		out = append(out, map[string]any{
			"name":        "playbook-" + p.ID,
			"title":       p.Title,
			"description": desc,
		})
	}
	return map[string]any{"prompts": out}
}

// getPlaybookPrompt answers prompts/get: the playbook markdown as the user
// message, so a client can run it as a slash command.
func (s *Server) getPlaybookPrompt(params json.RawMessage) any {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	const prefix = "playbook-"
	if !strings.HasPrefix(p.Name, prefix) {
		return nil
	}
	id := strings.TrimPrefix(p.Name, prefix)
	raw, err := s.apiGet("/api/playbooks/" + url.PathEscape(id))
	if err != nil {
		return nil
	}
	var d struct {
		Markdown string `json:"markdown"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil || d.Markdown == "" {
		return nil
	}
	return map[string]any{
		"description": fmt.Sprintf("Run the %s playbook against this engagement", id),
		"messages": []any{map[string]any{
			"role":    "user",
			"content": map[string]any{"type": "text", "text": d.Markdown},
		}},
	}
}
