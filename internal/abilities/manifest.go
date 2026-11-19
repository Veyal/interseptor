package abilities

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// SchemaV1 is the required schema identifier for ability.json manifests.
	SchemaV1 = "interseptor.ability/v1"

	// ManifestFileName is the manifest file expected at the root of an ability.
	ManifestFileName = "ability.json"

	// StateFileName stores per-scope trust and enablement records.
	StateFileName = "abilities-state.json"

	// ScopeGlobal and ScopeProject identify where an ability is stored.
	ScopeGlobal  = "global"
	ScopeProject = "project"

	// EventFlowCaptured is emitted asynchronously when a captured flow settles.
	EventFlowCaptured = "flow.captured"

	// MaxFileBytes bounds a single file inside an ability directory.
	MaxFileBytes int64 = 2 << 20 // 2 MiB

	// MaxAbilityBytes bounds the cumulative uncompressed size of an ability.
	MaxAbilityBytes int64 = 16 << 20 // 16 MiB

	// MaxArchiveFiles bounds the number of entries in an installed archive or directory.
	MaxArchiveFiles = 512

	// MaxHookBodyBytes caps request/response bodies exposed to Starlark scripts.
	MaxHookBodyBytes = 64 << 10 // 64 KiB

	// MaxExecutionSteps bounds Starlark execution steps per invocation.
	MaxExecutionSteps = 100_000

	// MaxHookTags bounds the number of tags a single flow.captured hook may apply.
	MaxHookTags = 16

	// MaxTagLen bounds each tag length returned by a Starlark hook.
	MaxTagLen = 64

	// MaxNoteLen bounds note length returned by a Starlark hook.
	MaxNoteLen = 4096

	// defaultMaxArchiveBytes is used when InstallArchive is called with maxBytes <= 0.
	defaultMaxArchiveBytes int64 = 32 << 20 // 32 MiB
)

var (
	ErrNotFound         = errors.New("ability not found")
	ErrNotTrusted       = errors.New("ability is not trusted")
	ErrActionNotExposed = errors.New("action is not exposed to non-UI callers")
	ErrDrift            = errors.New("ability files changed on disk since approval")
	ErrInvalidManifest  = errors.New("invalid ability manifest")
	ErrAlreadyExists    = errors.New("ability already exists")
	ErrInvalidArchive   = errors.New("invalid ability archive")
)

var (
	idPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	subIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// AbilityManifest describes an ability's metadata, UI extensions, hooks, and actions.
type AbilityManifest struct {
	Schema        string             `json:"schema"`
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Version       string             `json:"version"`
	Description   string             `json:"description,omitempty"`
	Author        string             `json:"author,omitempty"`
	Icon          string             `json:"icon,omitempty"`
	Capabilities  []string           `json:"capabilities,omitempty"`
	Menus         []MenuSpec         `json:"menus,omitempty"`
	ContextMenus  []ContextMenuSpec  `json:"contextMenus,omitempty"`
	InspectorTabs []InspectorTabSpec `json:"inspectorTabs,omitempty"`
	Commands      []CommandSpec      `json:"commands,omitempty"`
	Hooks         []HookSpec         `json:"hooks,omitempty"`
	Actions       []ActionSpec       `json:"actions,omitempty"`
}

// MenuSpec declares a top-level UI navigation panel contributed by an ability.
type MenuSpec struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Group       string `json:"group,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Entry       string `json:"entry"`
	Style       string `json:"style,omitempty"`
	Description string `json:"description,omitempty"`
}

// ContextMenuSpec declares a right-click / context action contributed by an ability.
type ContextMenuSpec struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Target string `json:"target,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Panel  string `json:"panel,omitempty"`
	Action string `json:"action,omitempty"`
	Entry  string `json:"entry,omitempty"`
}

// InspectorTabSpec declares a flow request/response inspector tab.
type InspectorTabSpec struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Side  string `json:"side,omitempty"`
	Entry string `json:"entry"`
	Style string `json:"style,omitempty"`
}

// CommandSpec declares a command-palette entry for an ability.
type CommandSpec struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Keywords []string `json:"keywords,omitempty"`
	Panel    string   `json:"panel,omitempty"`
	Action   string   `json:"action,omitempty"`
}

// UnmarshalJSON accepts keywords as either a string slice or a whitespace-separated string.
func (c *CommandSpec) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID       string          `json:"id"`
		Title    string          `json:"title"`
		Keywords json.RawMessage `json:"keywords,omitempty"`
		Panel    string          `json:"panel,omitempty"`
		Action   string          `json:"action,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.ID = raw.ID
	c.Title = raw.Title
	c.Panel = raw.Panel
	c.Action = raw.Action
	c.Keywords = nil
	if len(raw.Keywords) == 0 || string(raw.Keywords) == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw.Keywords, &list); err == nil {
		c.Keywords = list
		return nil
	}
	var text string
	if err := json.Unmarshal(raw.Keywords, &text); err == nil {
		c.Keywords = strings.Fields(text)
		return nil
	}
	return fmt.Errorf("command %q keywords must be string array or string", raw.ID)
}

// HookSpec declares an event-triggered Starlark script under hooks/.
type HookSpec struct {
	Event string `json:"event"`
	Entry string `json:"entry"`
}

// ActionSpec declares an invocable Starlark action under hooks/.
type ActionSpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Entry       string `json:"entry"`
	MCP         bool   `json:"mcp,omitempty"`
}

// TrustState is the persisted approval and enablement record for one ability ID.
type TrustState struct {
	TrustedDigest string    `json:"trustedDigest,omitempty"`
	Enabled       bool      `json:"enabled"`
	TrustedAt     time.Time `json:"trustedAt,omitempty"`
}

// Ability represents a discovered ability with its trust, enablement, and load status.
type Ability struct {
	ID              string          `json:"id"`
	Scope           string          `json:"scope"`
	Dir             string          `json:"dir,omitempty"`
	Manifest        AbilityManifest `json:"manifest"`
	Digest          string          `json:"digest"`
	TrustedDigest   string          `json:"trustedDigest,omitempty"`
	Trusted         bool            `json:"trusted"`
	Enabled         bool            `json:"enabled"`
	Quarantined     bool            `json:"quarantined"`
	Drift           bool            `json:"drift,omitempty"`
	TrustedAt       time.Time       `json:"trustedAt,omitempty"`
	TrustError      string          `json:"trustError,omitempty"`
	LoadError       string          `json:"loadError,omitempty"`
	OverridesGlobal bool            `json:"overridesGlobal,omitempty"`
}

// ScaffoldRequest configures generation of a starter ability directory.
type ScaffoldRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Group       string `json:"group,omitempty"`
}

// ValidID reports whether id matches ^[a-z0-9][a-z0-9-]{0,63}$.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

func validSubID(id string) bool {
	return subIDPattern.MatchString(id)
}

func validGroup(group string) bool {
	switch group {
	case "capture", "test", "recon", "report", "configure", "abilities":
		return true
	default:
		return false
	}
}

func validTarget(target string) bool {
	switch target {
	case "flow", "selection", "workspace":
		return true
	default:
		return false
	}
}

func validSide(side string) bool {
	switch side {
	case "both", "req", "res":
		return true
	default:
		return false
	}
}

// Clone returns a deep copy of m with independent slices.
func (m AbilityManifest) Clone() AbilityManifest {
	out := m
	out.Capabilities = slices.Clone(m.Capabilities)
	out.Menus = slices.Clone(m.Menus)
	out.ContextMenus = slices.Clone(m.ContextMenus)
	out.InspectorTabs = slices.Clone(m.InspectorTabs)
	out.Hooks = slices.Clone(m.Hooks)
	out.Actions = slices.Clone(m.Actions)
	if m.Commands != nil {
		out.Commands = make([]CommandSpec, len(m.Commands))
		for i, c := range m.Commands {
			out.Commands[i] = c
			out.Commands[i].Keywords = slices.Clone(c.Keywords)
		}
	}
	return out
}

// Clone returns a deep copy of a.
func (a Ability) Clone() Ability {
	out := a
	out.Manifest = a.Manifest.Clone()
	return out
}

// ParseManifest decodes, validates, and normalizes an ability.json payload.
func ParseManifest(data []byte) (AbilityManifest, error) {
	if int64(len(data)) > MaxFileBytes {
		return AbilityManifest{}, fmt.Errorf("%w: manifest exceeds %d bytes", ErrInvalidManifest, MaxFileBytes)
	}
	var m AbilityManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return AbilityManifest{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateAndNormalizeManifest(&m); err != nil {
		return AbilityManifest{}, err
	}
	return m, nil
}

func validateAndNormalizeManifest(m *AbilityManifest) error {
	if m.Schema != SchemaV1 {
		return fmt.Errorf("%w: schema must be %q", ErrInvalidManifest, SchemaV1)
	}
	if !ValidID(m.ID) {
		return fmt.Errorf("%w: invalid id %q", ErrInvalidManifest, m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	if err := validateMenus(m.Menus); err != nil {
		return err
	}
	if err := validateContextMenus(m.ContextMenus); err != nil {
		return err
	}
	if err := validateInspectorTabs(m.InspectorTabs); err != nil {
		return err
	}
	if err := validateCommands(m.Commands); err != nil {
		return err
	}
	if err := validateHooks(m.Hooks); err != nil {
		return err
	}
	if err := validateActions(m.Actions); err != nil {
		return err
	}
	return nil
}

func validateMenus(menus []MenuSpec) error {
	seen := make(map[string]struct{}, len(menus))
	for i := range menus {
		ms := &menus[i]
		if !validSubID(ms.ID) {
			return fmt.Errorf("%w: invalid menu id %q", ErrInvalidManifest, ms.ID)
		}
		if _, dup := seen[ms.ID]; dup {
			return fmt.Errorf("%w: duplicate menu id %q", ErrInvalidManifest, ms.ID)
		}
		seen[ms.ID] = struct{}{}
		if strings.TrimSpace(ms.Label) == "" {
			return fmt.Errorf("%w: menu %q missing label", ErrInvalidManifest, ms.ID)
		}
		if ms.Group == "" {
			ms.Group = "abilities"
		}
		if !validGroup(ms.Group) {
			return fmt.Errorf("%w: menu %q has invalid group %q", ErrInvalidManifest, ms.ID, ms.Group)
		}
		if err := validatePrefixedRelPath(ms.Entry, "ui/"); err != nil {
			return fmt.Errorf("%w: menu %q entry: %v", ErrInvalidManifest, ms.ID, err)
		}
		if ms.Style != "" {
			if err := validatePrefixedRelPath(ms.Style, "ui/"); err != nil {
				return fmt.Errorf("%w: menu %q style: %v", ErrInvalidManifest, ms.ID, err)
			}
		}
	}
	return nil
}

func validateContextMenus(items []ContextMenuSpec) error {
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		cm := &items[i]
		if !validSubID(cm.ID) {
			return fmt.Errorf("%w: invalid contextMenu id %q", ErrInvalidManifest, cm.ID)
		}
		if _, dup := seen[cm.ID]; dup {
			return fmt.Errorf("%w: duplicate contextMenu id %q", ErrInvalidManifest, cm.ID)
		}
		seen[cm.ID] = struct{}{}
		if strings.TrimSpace(cm.Label) == "" {
			return fmt.Errorf("%w: contextMenu %q missing label", ErrInvalidManifest, cm.ID)
		}
		if cm.Target == "" {
			cm.Target = "flow"
		}
		if !validTarget(cm.Target) {
			return fmt.Errorf("%w: contextMenu %q has invalid target %q", ErrInvalidManifest, cm.ID, cm.Target)
		}
		if cm.Entry != "" {
			if err := validatePrefixedRelPath(cm.Entry, "ui/"); err != nil {
				return fmt.Errorf("%w: contextMenu %q entry: %v", ErrInvalidManifest, cm.ID, err)
			}
		}
	}
	return nil
}

func validateInspectorTabs(tabs []InspectorTabSpec) error {
	seen := make(map[string]struct{}, len(tabs))
	for i := range tabs {
		tab := &tabs[i]
		if !validSubID(tab.ID) {
			return fmt.Errorf("%w: invalid inspectorTab id %q", ErrInvalidManifest, tab.ID)
		}
		if _, dup := seen[tab.ID]; dup {
			return fmt.Errorf("%w: duplicate inspectorTab id %q", ErrInvalidManifest, tab.ID)
		}
		seen[tab.ID] = struct{}{}
		if strings.TrimSpace(tab.Label) == "" {
			return fmt.Errorf("%w: inspectorTab %q missing label", ErrInvalidManifest, tab.ID)
		}
		if tab.Side == "" {
			tab.Side = "both"
		}
		if !validSide(tab.Side) {
			return fmt.Errorf("%w: inspectorTab %q has invalid side %q", ErrInvalidManifest, tab.ID, tab.Side)
		}
		if err := validatePrefixedRelPath(tab.Entry, "ui/"); err != nil {
			return fmt.Errorf("%w: inspectorTab %q entry: %v", ErrInvalidManifest, tab.ID, err)
		}
		if tab.Style != "" {
			if err := validatePrefixedRelPath(tab.Style, "ui/"); err != nil {
				return fmt.Errorf("%w: inspectorTab %q style: %v", ErrInvalidManifest, tab.ID, err)
			}
		}
	}
	return nil
}

func validateCommands(cmds []CommandSpec) error {
	seen := make(map[string]struct{}, len(cmds))
	for _, cmd := range cmds {
		if !validSubID(cmd.ID) {
			return fmt.Errorf("%w: invalid command id %q", ErrInvalidManifest, cmd.ID)
		}
		if _, dup := seen[cmd.ID]; dup {
			return fmt.Errorf("%w: duplicate command id %q", ErrInvalidManifest, cmd.ID)
		}
		seen[cmd.ID] = struct{}{}
		if strings.TrimSpace(cmd.Title) == "" {
			return fmt.Errorf("%w: command %q missing title", ErrInvalidManifest, cmd.ID)
		}
	}
	return nil
}

func validateHooks(hooks []HookSpec) error {
	for i, h := range hooks {
		if h.Event != EventFlowCaptured {
			return fmt.Errorf("%w: hook[%d] has unsupported event %q", ErrInvalidManifest, i, h.Event)
		}
		if err := validatePrefixedRelPath(h.Entry, "hooks/"); err != nil {
			return fmt.Errorf("%w: hook[%d] entry: %v", ErrInvalidManifest, i, err)
		}
		if !strings.HasSuffix(h.Entry, ".star") {
			return fmt.Errorf("%w: hook[%d] entry %q must end with .star", ErrInvalidManifest, i, h.Entry)
		}
	}
	return nil
}

func validateActions(actions []ActionSpec) error {
	seen := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		if !validSubID(a.ID) {
			return fmt.Errorf("%w: invalid action id %q", ErrInvalidManifest, a.ID)
		}
		if _, dup := seen[a.ID]; dup {
			return fmt.Errorf("%w: duplicate action id %q", ErrInvalidManifest, a.ID)
		}
		seen[a.ID] = struct{}{}
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: action %q missing name", ErrInvalidManifest, a.ID)
		}
		if err := validatePrefixedRelPath(a.Entry, "hooks/"); err != nil {
			return fmt.Errorf("%w: action %q entry: %v", ErrInvalidManifest, a.ID, err)
		}
		if !strings.HasSuffix(a.Entry, ".star") {
			return fmt.Errorf("%w: action %q entry %q must end with .star", ErrInvalidManifest, a.ID, a.Entry)
		}
	}
	return nil
}

func validateRelPath(raw string) error {
	if raw == "" {
		return errors.New("path is empty")
	}
	if strings.ContainsAny(raw, "\\\x00") {
		return fmt.Errorf("invalid characters in path %q", raw)
	}
	if strings.HasPrefix(raw, "/") || filepath.IsAbs(raw) {
		return fmt.Errorf("absolute path %q not allowed", raw)
	}
	if len(raw) >= 2 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' {
		return fmt.Errorf("drive-prefixed path %q not allowed", raw)
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("unclean or traversing segment in path %q", raw)
		}
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("hidden path segment %q not allowed", raw)
		}
		if err := validatePortableSegment(seg, raw); err != nil {
			return err
		}
	}
	if !fs.ValidPath(raw) {
		return fmt.Errorf("invalid relative path %q", raw)
	}
	if _, err := filepath.Localize(raw); err != nil {
		return fmt.Errorf("non-portable path %q: %w", raw, err)
	}
	return nil
}

func validatePortableSegment(seg, full string) error {
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return fmt.Errorf("trailing dot or space in path %q", full)
	}
	for i := 0; i < len(seg); i++ {
		if seg[i] < 0x20 {
			return fmt.Errorf("control character in path %q", full)
		}
	}
	if strings.ContainsAny(seg, `:*?"<>|`) {
		return fmt.Errorf("invalid filename character in path %q", full)
	}
	stem := seg
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return fmt.Errorf("reserved filename in path %q", full)
	}
	return nil
}

func validatePrefixedRelPath(raw, prefix string) error {
	if err := validateRelPath(raw); err != nil {
		return err
	}
	if !strings.HasPrefix(raw, prefix) || len(raw) <= len(prefix) {
		return fmt.Errorf("path %q must stay under %s", raw, prefix)
	}
	return nil
}

func manifestReferencedPaths(m AbilityManifest) []string {
	var refs []string
	for _, ms := range m.Menus {
		refs = append(refs, ms.Entry)
		if ms.Style != "" {
			refs = append(refs, ms.Style)
		}
	}
	for _, cm := range m.ContextMenus {
		if cm.Entry != "" {
			refs = append(refs, cm.Entry)
		}
	}
	for _, tab := range m.InspectorTabs {
		refs = append(refs, tab.Entry)
		if tab.Style != "" {
			refs = append(refs, tab.Style)
		}
	}
	for _, h := range m.Hooks {
		refs = append(refs, h.Entry)
	}
	for _, a := range m.Actions {
		refs = append(refs, a.Entry)
	}
	return refs
}

// ValidateDir verifies an ability directory's manifest, referenced assets/scripts,
// symlink containment, size limits, and computes its canonical SHA-256 digest.
func ValidateDir(dir string) (AbilityManifest, string, error) {
	snap, err := snapshotDir(dir)
	if err != nil {
		return AbilityManifest{}, "", err
	}
	m, err := snap.manifest()
	if err != nil {
		return AbilityManifest{}, "", err
	}
	return m, snap.digest, nil
}

// ComputeDigest computes the canonical SHA-256 digest over all regular non-hidden
// files in dir, sorted by slash-separated relative path.
func ComputeDigest(dir string) (string, error) {
	snap, err := snapshotDir(dir)
	if err != nil {
		return "", err
	}
	return snap.digest, nil
}
