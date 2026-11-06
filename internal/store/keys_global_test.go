package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// A default project created before the api_keys table grew its scope/expires
// columns is still a valid migration source: the key that authenticated a remote
// client must keep working after the switch to global keys. Reading the modern
// column list unconditionally failed on such a project, and the caller swallowed
// the error, so the key (and the armed-auth flag) silently never migrated.
func TestAttachGlobalKeysMigratesFromLegacyDefaultSchema(t *testing.T) {
	global := t.TempDir()
	legacyPath := filepath.Join(global, currentDBName)
	legacy, err := sql.Open("sqlite", "file:"+legacyPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT, prefix TEXT NOT NULL,
		hash TEXT NOT NULL, created INTEGER NOT NULL);
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	token := "ick_0123456789abcdef0123456789abcdef0123456789abcdef"
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if _, err := legacy.Exec(`INSERT INTO api_keys (label, prefix, hash, created) VALUES (?,?,?,?)`,
		"laptop", token[:12], hash, int64(1)); err != nil {
		t.Fatalf("insert legacy key: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO settings (key, value) VALUES (?, '1')`, apiKeyAuthArmedSetting); err != nil {
		t.Fatalf("arm legacy auth: %v", err)
	}
	legacy.Close()

	named, err := Open(filepath.Join(global, "projects", "eng"))
	if err != nil {
		t.Fatalf("Open named: %v", err)
	}
	defer named.Close()
	if err := named.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys: %v", err)
	}
	ok, scope, err := named.VerifyAPIKeyScope(token)
	if err != nil || !ok || scope != ScopeFull {
		t.Fatalf("legacy key did not migrate: ok=%v scope=%q err=%v", ok, scope, err)
	}
	required, err := named.APIKeyAuthRequired()
	if err != nil || !required {
		t.Fatalf("legacy armed state did not migrate: required=%v err=%v", required, err)
	}
}
