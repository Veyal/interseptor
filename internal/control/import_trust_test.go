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

	"github.com/Veyal/interseptor/internal/store"
)

// craftedTrustedArchive builds a project archive whose database already
// carries trusted scripts, granted capabilities and scope policy "off".
func craftedTrustedArchive(t *testing.T) (zipPath string, uid string) {
	t.Helper()
	src := t.TempDir()
	st, err := store.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCollection(store.Collection{Name: "Evil", ScopePolicy: store.ScopePolicyOff,
		Caps: []byte(`["net.send","secrets.read","vars.read"]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TrustScript(c.UID, "deadbeef"); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := st.BackupTo(snap); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	zipPath = filepath.Join(t.TempDir(), "evil.zip")
	writeProjectZip(t, zipPath, []zipMember{{name: archiveDBName, data: db}})
	return zipPath, c.UID
}

func assertQuarantined(t *testing.T, projDir, uid string) {
	t.Helper()
	st, err := store.Open(projDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if ok, _ := st.IsScriptTrusted(uid, "deadbeef"); ok {
		t.Fatal("script trust survived import")
	}
	c, err := st.GetCollection(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Caps) > 0 && string(c.Caps) != "null" {
		t.Fatalf("caps survived import: %s", c.Caps)
	}
	if c.ScopePolicy == store.ScopePolicyOff {
		t.Fatal("scope policy off survived import")
	}
}

func TestInstallFullArchiveQuarantinesTrustAndCaps(t *testing.T) {
	zipPath, uid := craftedTrustedArchive(t)
	dest := filepath.Join(t.TempDir(), "projects", "evil")
	if err := installFullArchive(zipPath, dest, false); err != nil {
		t.Fatal(err)
	}
	assertQuarantined(t, dest, uid)
}

func TestImportFullRouteQuarantinesTrustAndCaps(t *testing.T) {
	zipPath, uid := craftedTrustedArchive(t)
	data, _ := os.ReadFile(zipPath)
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	r, err := http.Post(ts.URL+"/api/import/full?name=evil", "application/zip", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d: %s", r.StatusCode, b)
	}
	assertQuarantined(t, filepath.Join(h.GlobalDir, "projects", "evil"), uid)
}

func TestVaultImportQuarantinesTrustAndCaps(t *testing.T) {
	vaultURL, token := startTestVault(t)
	zipPath, uid := craftedTrustedArchive(t)
	data, _ := os.ReadFile(zipPath)
	req, _ := http.NewRequest(http.MethodPut, vaultURL+"/api/vault/projects/evil", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	pr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := io.ReadAll(pr.Body)
	pr.Body.Close()
	if pr.StatusCode/100 != 2 {
		t.Fatalf("vault put: %d %s", pr.StatusCode, pb)
	}
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	cfg, _ := json.Marshal(map[string]string{"url": vaultURL, "key": token})
	cr, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/vault/config", bytes.NewReader(cfg))
	cr.Header.Set("Content-Type", "application/json")
	if r, err := http.DefaultClient.Do(cr); err != nil || r.StatusCode != 200 {
		t.Fatalf("config: %v", err)
	}
	ir, err := http.Post(ts.URL+"/api/vault/import", "application/json", bytes.NewReader([]byte(`{"id":"evil","name":"from-vault"}`)))
	if err != nil {
		t.Fatal(err)
	}
	ib, _ := io.ReadAll(ir.Body)
	ir.Body.Close()
	if ir.StatusCode != 200 {
		t.Fatalf("import: %d %s", ir.StatusCode, ib)
	}
	assertQuarantined(t, filepath.Join(h.GlobalDir, "projects", "from-vault"), uid)
}
