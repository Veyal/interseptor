package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const archiveCanary = "CANARY-ARCHIVE-s3cr3t-77aa"

// seedArchiveSecrets plants one secret of every kind the collections scrub must remove.
func seedArchiveSecrets(t *testing.T, s *store.Store) {
	t.Helper()
	c, err := s.CreateCollection(store.Collection{Name: "c",
		Auth: json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"` + archiveCanary + `-bearer"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateItem(store.Item{CollectionUID: c.UID, Kind: "request", Name: "r", Method: "GET",
		URL:     json.RawMessage(`{"raw":"https://example.com/a"}`),
		Headers: json.RawMessage(`[{"key":"Authorization","value":"Bearer ` + archiveCanary + `-hdr"}]`)}); err != nil {
		t.Fatal(err)
	}
	e, err := s.CreateEnvironment(store.Environment{Name: "env"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetVariables(store.VarOwnerEnvironment, e.UID, []store.Variable{
		{Key: "tok", Type: "secret", InitialValue: "x", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentValue(store.VarOwnerEnvironment, e.UID, "tok", archiveCanary+"-cur", "script"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCookie(store.CollCookie{Domain: "example.com", Name: "sid", Value: archiveCanary + "-cookie"}); err != nil {
		t.Fatal(err)
	}
}

func dbFromArchive(t *testing.T, archive []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "interceptor.db" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return b
		}
	}
	t.Fatal("archive has no interceptor.db")
	return nil
}

func TestFullArchiveExportScrubsCollectionSecrets(t *testing.T) {
	h, s, _ := newHub(t)
	seedArchiveSecrets(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/export/full")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, archive)
	}
	db := dbFromArchive(t, archive)
	if bytes.Contains(db, []byte(archiveCanary)) {
		t.Fatal("secret canary leaked into /api/export/full archive")
	}
	if !bytes.Contains(db, []byte("example.com")) {
		t.Fatal("shareable collection data must survive the scrub")
	}
}

func TestFullArchiveFileExportScrubsCollectionSecrets(t *testing.T) {
	h, s, _ := newHub(t)
	seedArchiveSecrets(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	dest := filepath.Join(t.TempDir(), "out.zip")
	body, _ := json.Marshal(map[string]string{"path": dest})
	resp, err := http.Post(ts.URL+"/api/export/full/file", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	archive, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(dbFromArchive(t, archive), []byte(archiveCanary)) {
		t.Fatal("secret canary leaked into file archive")
	}
}

func TestVaultBackupScrubsCollectionSecrets(t *testing.T) {
	vaultURL, token := startTestVault(t)
	h, s, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	seedArchiveSecrets(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	cfg, _ := json.Marshal(map[string]string{"url": vaultURL, "key": token})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/vault/config", bytes.NewReader(cfg))
	req.Header.Set("Content-Type", "application/json")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("config: %v", err)
	}
	br, err := http.Post(ts.URL+"/api/vault/backup", "application/json", strings.NewReader(`{"id":"canary"}`))
	if err != nil {
		t.Fatal(err)
	}
	if br.StatusCode != 200 {
		b, _ := io.ReadAll(br.Body)
		t.Fatalf("backup %d %s", br.StatusCode, b)
	}
	br.Body.Close()
	// Restore the stored revision into a new project and inspect its DB.
	ir, err := http.Post(ts.URL+"/api/vault/import", "application/json", strings.NewReader(`{"id":"canary","name":"restored"}`))
	if err != nil || ir.StatusCode != 200 {
		t.Fatalf("import: %v", err)
	}
	ir.Body.Close()
	raw, err := os.ReadFile(filepath.Join(h.GlobalDir, "projects", "restored", "interceptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(archiveCanary)) {
		t.Fatal("secret canary leaked through the vault push")
	}
}
