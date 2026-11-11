package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The UI can switch projects between two tool calls. Nothing in a tool result
// said which project it touched, so an agent could write a codec into A and
// prune B while believing both were the same engagement. Every result now ends
// with the project it acted on, and a destructive tool can be pinned to the
// project the caller expects.

// projectMeta is the subset of GET /api/project the guard needs.
type projectMeta struct {
	Current string `json:"current"`
	Dir     string `json:"dir"`
}

// noteProject records the project named by a response's headers.
func (s *Server) noteProject(name, dir string) {
	if name == "" {
		return
	}
	s.projMu.Lock()
	s.projName, s.projDir = name, dir
	s.projMu.Unlock()
}

// knownProject is the project the last response named, with no request of its own.
func (s *Server) knownProject() (name, dir string, ok bool) {
	s.projMu.Lock()
	defer s.projMu.Unlock()
	return s.projName, s.projDir, s.projName != ""
}

// activeProject fetches the active project from the control plane. Used only by
// the destructive guard, when a caller pins a project: ordinary results learn it
// from the header of the response they were already making.
func (s *Server) activeProject() (name, dir string, ok bool) {
	raw, err := s.apiGet("/api/project")
	if err != nil {
		return s.knownProject()
	}
	var meta projectMeta
	if json.Unmarshal([]byte(raw), &meta) != nil || meta.Current == "" {
		return s.knownProject()
	}
	s.noteProject(meta.Current, meta.Dir)
	return meta.Current, meta.Dir, true
}

// projectFooter is appended to every successful tool result.
func (s *Server) projectFooter() string {
	name, dir, ok := s.knownProject()
	if !ok || name == "" {
		return ""
	}
	if dir == "" {
		return "\n\nproject: " + name
	}
	return "\n\nproject: " + name + " (" + dir + ")"
}

// destructiveTools are refused when the caller pins a different project than the
// one that is active, so a switch cannot silently redirect a delete.
var destructiveTools = map[string]bool{
	"prune_history":            true,
	"prune_undo":               true,
	"delete_finding":           true,
	"restore_finding_revision": true,
	"delete_codec":             true,
	"delete_rule":              true,
	"import_full_project":      true,
}

// requireExpectedProject rejects a destructive call that pinned a project other
// than the active one, naming what is active so the agent can correct course.
func (s *Server) requireExpectedProject(toolName string, args map[string]any) error {
	if !destructiveTools[toolName] {
		return nil
	}
	want := strings.TrimSpace(argStr(args, "expectedProject"))
	if want == "" {
		return nil
	}
	// A pin is the caller opting into verification, so it costs one request.
	name, dir, ok := s.activeProject()
	if !ok || name == "" {
		return nil
	}
	if !strings.EqualFold(want, name) && !strings.EqualFold(want, dir) {
		return fmt.Errorf("active project is %q (%s), not %q: the project changed since your last call — re-run against the right project or refresh with list_flows", name, dir, want)
	}
	return nil
}

// expectedProjectDesc is the schema text of the optional project pin.
const expectedProjectDesc = "optional project pin: the active project's name (or directory) from the `project:` line of an earlier result; the call is rejected, naming the active project, when it no longer matches"

// advertiseExpectedProject adds the expectedProject pin to the input schema of
// every destructive tool. requireExpectedProject already honours it and the
// connect instructions name it, but a client that builds calls from tools/list
// could not know the argument exists.
func (s *Server) advertiseExpectedProject() {
	for name := range destructiveTools {
		t, ok := s.tools[name]
		if !ok {
			continue
		}
		props, _ := t.schema["properties"].(map[string]any)
		if props == nil {
			continue
		}
		if _, exists := props["expectedProject"]; !exists {
			props["expectedProject"] = p("string", expectedProjectDesc)
		}
	}
}
