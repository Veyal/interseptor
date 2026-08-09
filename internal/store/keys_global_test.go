package store

import (
	"path/filepath"
	"testing"
)

// TestAttachGlobalKeysSurvivesProjectSwitch verifies that a key minted on the
// default project still verifies after opening a named project with the same
// globalDir — the bug that broke Tailscale login after project switch.
func TestAttachGlobalKeysSurvivesProjectSwitch(t *testing.T) {
	global := t.TempDir()
	defaultDir := global
	namedDir := filepath.Join(global, "projects", "client-a")

	def, err := Open(defaultDir)
	if err != nil {
		t.Fatalf("Open default: %v", err)
	}
	if err := def.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys default: %v", err)
	}
	token, _, err := def.CreateAPIKey("laptop", ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	def.Close()

	named, err := Open(namedDir)
	if err != nil {
		t.Fatalf("Open named: %v", err)
	}
	defer named.Close()
	if err := named.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys named: %v", err)
	}
	ok, scope, err := named.VerifyAPIKeyScope(token)
	if err != nil || !ok || scope != ScopeFull {
		t.Fatalf("key should verify on named project: ok=%v scope=%q err=%v", ok, scope, err)
	}
}

// TestAttachGlobalKeysMigratesFromDefaultDB covers the case where the key was
// minted before AttachGlobalKeys existed (project-local on default) and a named
// project opens with an empty local api_keys table.
func TestAttachGlobalKeysMigratesFromDefaultDB(t *testing.T) {
	global := t.TempDir()
	def, err := Open(global)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Mint WITHOUT AttachGlobalKeys — legacy project-local key.
	token, _, err := def.CreateAPIKey("legacy", ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	def.Close()

	named, err := Open(filepath.Join(global, "projects", "eng"))
	if err != nil {
		t.Fatalf("Open named: %v", err)
	}
	defer named.Close()
	if err := named.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys: %v", err)
	}
	ok, _, err := named.VerifyAPIKeyScope(token)
	if err != nil || !ok {
		t.Fatalf("legacy default key should migrate into keys.db: ok=%v err=%v", ok, err)
	}
	required, err := named.APIKeyAuthRequired()
	if err != nil || !required {
		t.Fatalf("live legacy key should arm global auth: required=%v err=%v", required, err)
	}
}

func TestAttachGlobalKeysMigratesArmedStateWithoutLiveKeys(t *testing.T) {
	global := t.TempDir()
	legacy, err := Open(global)
	if err != nil {
		t.Fatalf("Open legacy: %v", err)
	}
	_, key, err := legacy.CreateAPIKey("legacy", ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if err := legacy.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	legacy.Close()

	migrated, err := Open(filepath.Join(global, "projects", "eng"))
	if err != nil {
		t.Fatalf("Open migrated: %v", err)
	}
	if err := migrated.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys: %v", err)
	}
	migrated.Close()

	reopened, err := Open(filepath.Join(global, "projects", "eng"))
	if err != nil {
		t.Fatalf("Reopen migrated: %v", err)
	}
	defer reopened.Close()
	if err := reopened.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys after reopen: %v", err)
	}
	required, err := reopened.APIKeyAuthRequired()
	if err != nil || !required {
		t.Fatalf("deleted final legacy key should leave global auth armed: required=%v err=%v", required, err)
	}
	hasKeys, err := reopened.HasAPIKeys()
	if err != nil || hasKeys {
		t.Fatalf("armed state must not synthesize a live key: hasKeys=%v err=%v", hasKeys, err)
	}
}
