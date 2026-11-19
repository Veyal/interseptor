package abilities

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestValidAndDefaults(t *testing.T) {
	raw := []byte(`{
		"schema": "interseptor.ability/v1",
		"id": "jwt-inspector",
		"name": "JWT Inspector",
		"version": "1.0.0",
		"description": "Inspects bearer tokens on example.com",
		"author": "Test Author",
		"capabilities": ["flow.read"],
		"menus": [
			{"id": "main", "label": "JWT Panel", "entry": "ui/panel.js", "style": "ui/style.css"}
		],
		"contextMenus": [
			{"id": "ctx1", "label": "Decode JWT", "panel": "main", "action": "decode", "entry": "ui/context.js"}
		],
		"inspectorTabs": [
			{"id": "tab1", "label": "JWT", "entry": "ui/inspector.js"}
		],
		"commands": [
			{"id": "cmd1", "title": "Decode JWT", "keywords": ["jwt", "token"], "panel": "main", "action": "decode"}
		],
		"hooks": [
			{"event": "flow.captured", "entry": "hooks/on_flow.star"}
		],
		"actions": [
			{"id": "decode", "name": "Decode", "description": "Decode token", "entry": "hooks/action.star", "mcp": true}
		]
	}`)

	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Schema != SchemaV1 {
		t.Fatalf("schema = %q want %q", m.Schema, SchemaV1)
	}
	if m.Menus[0].Group != "abilities" {
		t.Fatalf("default menu group = %q want abilities", m.Menus[0].Group)
	}
	if m.ContextMenus[0].Target != "flow" {
		t.Fatalf("default context menu target = %q want flow", m.ContextMenus[0].Target)
	}
	if m.InspectorTabs[0].Side != "both" {
		t.Fatalf("default inspector tab side = %q want both", m.InspectorTabs[0].Side)
	}

	// Also accept whitespace-separated string keywords in CommandSpec
	strKeywords := []byte(`{
		"schema": "interseptor.ability/v1",
		"id": "cmd-str",
		"name": "Cmd Str",
		"version": "1.0.0",
		"commands": [{"id":"c1","title":"Open","keywords":"jwt bearer token"}]
	}`)
	m2, err := ParseManifest(strKeywords)
	if err != nil {
		t.Fatalf("ParseManifest string keywords: %v", err)
	}
	if len(m2.Commands[0].Keywords) != 3 || m2.Commands[0].Keywords[1] != "bearer" {
		t.Fatalf("keywords = %v", m2.Commands[0].Keywords)
	}
}

func TestParseManifestRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"empty json", `{}`},
		{"missing schema", `{"id":"ok-id","name":"Ok","version":"1.0.0"}`},
		{"wrong schema", `{"schema":"interseptor.ability/v2","id":"ok-id","name":"Ok","version":"1.0.0"}`},
		{"uppercase id", `{"schema":"interseptor.ability/v1","id":"BadID","name":"Ok","version":"1.0.0"}`},
		{"leading dash id", `{"schema":"interseptor.ability/v1","id":"-bad","name":"Ok","version":"1.0.0"}`},
		{"underscore id", `{"schema":"interseptor.ability/v1","id":"bad_id","name":"Ok","version":"1.0.0"}`},
		{"too long id", `{"schema":"interseptor.ability/v1","id":"` + strings.Repeat("a", 65) + `","name":"Ok","version":"1.0.0"}`},
		{"missing name", `{"schema":"interseptor.ability/v1","id":"ok-id","version":"1.0.0"}`},
		{"missing version", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok"}`},
		{"bad menu group", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","group":"invalid","entry":"ui/panel.js"}]}`},
		{"bad context target", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","contextMenus":[{"id":"c1","label":"C","target":"host"}]}`},
		{"bad inspector side", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","inspectorTabs":[{"id":"t1","label":"T","side":"headers","entry":"ui/tab.js"}]}`},
		{"bad hook event", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","hooks":[{"event":"flow.deleted","entry":"hooks/h.star"}]}`},
		{"hook not star", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","hooks":[{"event":"flow.captured","entry":"hooks/h.js"}]}`},
		{"action not star", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","actions":[{"id":"a1","name":"A","entry":"hooks/a.txt"}]}`},
		{"menu outside ui", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"hooks/on_flow.star"}]}`},
		{"menu style outside ui", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"ui/p.js","style":"ability.json"}]}`},
		{"inspector outside ui", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","inspectorTabs":[{"id":"t1","label":"T","entry":"other/tab.js"}]}`},
		{"hook outside hooks", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","hooks":[{"event":"flow.captured","entry":"ui/on_flow.star"}]}`},
		{"action outside hooks", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","actions":[{"id":"a1","name":"A","entry":"ui/a.star"}]}`},
		{"traversal in entry", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"ui/../hooks/on_flow.star"}]}`},
		{"absolute entry", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"/ui/panel.js"}]}`},
		{"backslash entry", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"ui\\panel.js"}]}`},
		{"contextMenu entry outside ui", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","contextMenus":[{"id":"c1","label":"C","entry":"hooks/c.js"}]}`},
		{"hidden path segment", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"ui/.secret/panel.js"}]}`},
		{"reserved windows filename", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M","entry":"ui/CON.js"}]}`},
		{"duplicate menu id", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","menus":[{"id":"m1","label":"M1","entry":"ui/a.js"},{"id":"m1","label":"M2","entry":"ui/b.js"}]}`},
		{"duplicate action id", `{"schema":"interseptor.ability/v1","id":"ok-id","name":"Ok","version":"1.0.0","actions":[{"id":"a1","name":"A1","entry":"hooks/a.star"},{"id":"a1","name":"A2","entry":"hooks/b.star"}]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.json)); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func writeTestAbilityFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateDirSymlinkMissingAndSizeBounds(t *testing.T) {
	baseManifest := `{
		"schema":"interseptor.ability/v1",
		"id":"sample-ability",
		"name":"Sample",
		"version":"0.1.0",
		"menus":[{"id":"main","label":"Main","entry":"ui/panel.js"}]
	}`

	t.Run("missing referenced file", func(t *testing.T) {
		dir := t.TempDir()
		writeTestAbilityFiles(t, dir, map[string]string{
			"ability.json": baseManifest,
		})
		if _, _, err := ValidateDir(dir); err == nil {
			t.Fatal("expected error when ui/panel.js is missing")
		}
	})

	t.Run("symlink escape rejected", func(t *testing.T) {
		parent := t.TempDir()
		outside := filepath.Join(parent, "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(parent, "ability")
		writeTestAbilityFiles(t, dir, map[string]string{
			"ability.json": baseManifest,
		})
		if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "ui", "panel.js")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateDir(dir); err == nil {
			t.Fatal("expected symlink escape error")
		}
	})

	t.Run("unreferenced symlink escape in dir rejected", func(t *testing.T) {
		parent := t.TempDir()
		outside := filepath.Join(parent, "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(parent, "ability")
		writeTestAbilityFiles(t, dir, map[string]string{
			"ability.json": baseManifest,
			"ui/panel.js":  "export function mount() {}",
		})
		if err := os.Symlink(outside, filepath.Join(dir, "ui", "leak.js")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateDir(dir); err == nil {
			t.Fatal("expected symlink escape error for unreferenced file")
		}
	})

	t.Run("oversized single file rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeTestAbilityFiles(t, dir, map[string]string{
			"ability.json": baseManifest,
			"ui/panel.js":  "",
		})
		big := make([]byte, MaxFileBytes+1)
		if err := os.WriteFile(filepath.Join(dir, "ui", "panel.js"), big, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateDir(dir); err == nil {
			t.Fatal("expected error for file > MaxFileBytes")
		}
	})

	t.Run("oversized total ability rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeTestAbilityFiles(t, dir, map[string]string{
			"ability.json": baseManifest,
			"ui/panel.js":  "export function mount() {}",
		})
		// Create 9 files of 2 MiB each -> 18 MiB > 16 MiB
		chunk := make([]byte, MaxFileBytes)
		for i := 0; i < 9; i++ {
			name := filepath.Join(dir, "ui", "blob-"+string(rune('a'+i))+".bin")
			if err := os.WriteFile(name, chunk, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := ValidateDir(dir); err == nil {
			t.Fatal("expected error for cumulative size > MaxAbilityBytes")
		}
	})
}

func TestComputeDigestCanonical(t *testing.T) {
	dir := t.TempDir()
	writeTestAbilityFiles(t, dir, map[string]string{
		"ability.json": `{"schema":"interseptor.ability/v1","id":"d1","name":"D1","version":"1.0.0"}`,
		"ui/a.js":      "console.log('a');",
		".DS_Store":    "junk",
		".hidden/x":    "ignored",
	})
	d1, err := ComputeDigest(dir)
	if err != nil {
		t.Fatalf("ComputeDigest: %v", err)
	}
	if !strings.HasPrefix(d1, "sha256:") {
		t.Fatalf("digest = %q want sha256: prefix", d1)
	}

	// Modifying .DS_Store should not change digest
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("different junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2, err := ComputeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("digest changed on .DS_Store edit: %s != %s", d1, d2)
	}

	// Modifying regular file changes digest
	if err := os.WriteFile(filepath.Join(dir, "ui", "a.js"), []byte("console.log('b');"), 0o644); err != nil {
		t.Fatal(err)
	}
	d3, err := ComputeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("expected digest change when ui/a.js changed")
	}
}
