package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/vault"
)

func startTestVault(t *testing.T) (baseURL, token string) {
	t.Helper()
	dir := t.TempDir()
	st, err := vault.Open(dir, 5)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	auth, err := vault.OpenAuth(dir)
	if err != nil {
		t.Fatalf("vault.OpenAuth: %v", err)
	}
	raw, _, err := auth.EnsureBootstrap()
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	srv := vault.NewServer(st, auth)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, raw
}

func TestVaultClientBackupListImportMerge(t *testing.T) {
	vaultURL, token := startTestVault(t)

	h, s, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	h.ProjectName = "acme-eng"
	bodyHash, _ := (&projectAPI{h}).storeBody([]byte(`vault-body`))
	if _, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com",
		Path: "/v", Status: 200, ResBodyHash: bodyHash, ResLen: 10,
	}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Config missing → list fails.
	r, err := http.Get(ts.URL + "/api/vault/remote")
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		t.Fatalf("remote without config: %d %s", r.StatusCode, b)
	}
	r.Body.Close()

	cfgBody, _ := json.Marshal(map[string]string{"url": vaultURL, "key": token})
	cr, err := http.NewRequest(http.MethodPut, ts.URL+"/api/vault/config", bytes.NewReader(cfgBody))
	if err != nil {
		t.Fatal(err)
	}
	cr.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(cr)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("put config: %d %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// Key must not echo back.
	gr, _ := http.Get(ts.URL + "/api/vault/config")
	var cfg map[string]any
	_ = json.NewDecoder(gr.Body).Decode(&cfg)
	gr.Body.Close()
	if cfg["hasKey"] != true || cfg["url"] != vaultURL {
		t.Fatalf("config: %+v", cfg)
	}
	if _, ok := cfg["key"]; ok {
		t.Fatal("config must not return key")
	}

	br, err := http.Post(ts.URL+"/api/vault/backup", "application/json", bytes.NewReader([]byte(`{"id":"acme","label":"unit"}`)))
	if err != nil {
		t.Fatal(err)
	}
	bbody, _ := io.ReadAll(br.Body)
	br.Body.Close()
	if br.StatusCode != http.StatusOK {
		t.Fatalf("backup: %d %s", br.StatusCode, bbody)
	}

	lr, err := http.Get(ts.URL + "/api/vault/remote")
	if err != nil {
		t.Fatal(err)
	}
	lbody, _ := io.ReadAll(lr.Body)
	lr.Body.Close()
	if lr.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", lr.StatusCode, lbody)
	}
	if !bytes.Contains(lbody, []byte(`"acme"`)) {
		t.Fatalf("list missing acme: %s", lbody)
	}

	// Import as new project on same hub.
	ir, err := http.Post(ts.URL+"/api/vault/import", "application/json",
		bytes.NewReader([]byte(`{"id":"acme","name":"from-vault"}`)))
	if err != nil {
		t.Fatal(err)
	}
	ibody, _ := io.ReadAll(ir.Body)
	ir.Body.Close()
	if ir.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", ir.StatusCode, ibody)
	}
	projDir := filepath.Join(h.GlobalDir, "projects", "from-vault")
	if _, err := os.Stat(filepath.Join(projDir, "interceptor.db")); err != nil {
		t.Fatalf("imported db: %v", err)
	}

	// Dry-run merge into current.
	mr, err := http.Post(ts.URL+"/api/vault/merge", "application/json",
		bytes.NewReader([]byte(`{"id":"acme","dryRun":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	mbody, _ := io.ReadAll(mr.Body)
	mr.Body.Close()
	if mr.StatusCode != http.StatusOK {
		t.Fatalf("merge dry-run: %d %s", mr.StatusCode, mbody)
	}
	if !bytes.Contains(mbody, []byte(`"dryRun":true`)) {
		t.Fatalf("expected dryRun: %s", mbody)
	}
}
