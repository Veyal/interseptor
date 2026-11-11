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
// from the header of the response they were already making. It does not fall
// back to the cached name: the cache is what an earlier response said, and the
// pin exists precisely because the project can change between two calls.
func (s *Server) activeProject() (name, dir string, err error) {
	raw, err := s.apiGet("/api/project")
	if err != nil {
		return "", "", err
	}
	var meta projectMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return "", "", fmt.Errorf("unreadable /api/project reply: %w", err)
	}
	if meta.Current == "" {
		return "", "", fmt.Errorf("the control API names no active project")
	}
	s.noteProject(meta.Current, meta.Dir)
	return meta.Current, meta.Dir, nil
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
// one that is active, so a switch cannot silently redirect a delete or a
// whole-record replacement. set_authz is pinned only in mode=replace (see
// pinApplies): its default merge mode keeps every other identity.
var destructiveTools = map[string]bool{
	"prune_history":            true,
	"prune_undo":               true,
	"delete_finding":           true,
	"restore_finding_revision": true,
	"delete_codec":             true,
	"delete_rule":              true,
	"import_full_project":      true,
	"set_notes":                true, // replaces the whole notebook
	"set_engagement_brief":     true, // replaces the brief; omitted fields are cleared
	"remove_authz_identity":    true,
	"set_authz":                true, // mode=replace only
}

// pinApplies reports whether this call is one the project pin guards.
func pinApplies(toolName string, args map[string]any) bool {
	if !destructiveTools[toolName] {
		return false
	}
	if toolName == "set_authz" {
		return strings.EqualFold(strings.TrimSpace(argStr(args, "mode")), "replace")
	}
	return true
}

// requireExpectedProject rejects a destructive call that pinned a project other
// than the active one, naming what is active so the agent can correct course.
// It fails closed: a caller that supplied the pin asked for verification, so
// when the active project cannot be determined the call is refused rather than
// run unverified. A call without the pin is not checked.
func (s *Server) requireExpectedProject(toolName string, args map[string]any) error {
	if !pinApplies(toolName, args) {
		return nil
	}
	want := strings.TrimSpace(argStr(args, "expectedProject"))
	if want == "" {
		return nil
	}
	// A pin is the caller opting into verification, so it costs one request.
	name, dir, err := s.activeProject()
	if err != nil {
		return fmt.Errorf("expectedProject %q could not be verified, so %s was not run: the active project is unknown (%v) — retry once the control API answers", want, toolName, err)
	}
	if !strings.EqualFold(want, name) && !strings.EqualFold(want, dir) {
		return fmt.Errorf("active project is %q (%s), not %q: the project changed since your last call — re-run against the right project or refresh with list_flows", name, dir, want)
	}
	return nil
}

// expectedProjectDesc is the schema text of the optional project pin.
const expectedProjectDesc = "optional project pin: the active project's name (or directory) from the `project:` line of an earlier result; the call is rejected, naming the active project, when it no longer matches"

// expectedProjectReplaceDesc is the pin's schema text on set_authz, where it
// only guards the destructive mode.
const expectedProjectReplaceDesc = "optional project pin, checked only with mode=replace: the active project's name (or directory) from the `project:` line of an earlier result; the call is rejected, naming the active project, when it no longer matches"

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
			desc := expectedProjectDesc
			if name == "set_authz" {
				desc = expectedProjectReplaceDesc
			}
			props["expectedProject"] = p("string", desc)
		}
	}
}
