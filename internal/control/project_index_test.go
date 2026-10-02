package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeProjectCategory(t *testing.T) {
	got, err := normalizeProjectCategory("  Clients / Acme  ")
	if err != nil || got != "Clients/Acme" {
		t.Fatalf("category = %q, %v", got, err)
	}
	if _, err := normalizeProjectCategory("a/b/c/d"); err == nil {
		t.Fatal("four folder levels accepted")
	}
	if _, err := normalizeProjectCategory("../secret"); err == nil {
		t.Fatal("parent segment accepted")
	}
}

func TestProjectIndexRecordsCreatedAndOpened(t *testing.T) {
	global := t.TempDir()
	dir := filepath.Join(global, "projects", "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	if err := touchProjectMeta(global, projectMetaKey("acme", ""), dir, true); err != nil {
		t.Fatal(err)
	}
	meta := readProjectIndex(global)[projectMetaKey("acme", "")]
	if meta.CreatedAt < before-2 || meta.OpenedAt < before-2 {
		t.Fatalf("meta = %+v", meta)
	}
	created := meta.CreatedAt
	time.Sleep(1100 * time.Millisecond)
	if err := touchProjectMeta(global, projectMetaKey("acme", ""), dir, true); err != nil {
		t.Fatal(err)
	}
	again := readProjectIndex(global)[projectMetaKey("acme", "")]
	if again.CreatedAt != created || again.OpenedAt <= meta.OpenedAt {
		t.Fatalf("second touch = %+v, first opened %d created %d", again, meta.OpenedAt, created)
	}
}

func TestProjectFolderAPIGroupsSavedProject(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	h.ProjectName = "default"
	h.ProjectDir = h.GlobalDir
	if err := os.MkdirAll(filepath.Join(h.GlobalDir, "projects", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"target": "acme", "category": "Clients/Acme"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/project/folder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, err := http.Get(ts.URL + "/api/project")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	var info struct {
		Projects []projectEntry `json:"projects"`
	}
	if err := json.NewDecoder(got.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	var acme projectEntry
	for _, p := range info.Projects {
		if p.Name == "acme" {
			acme = p
		}
	}
	if acme.Category != "Clients/Acme" || acme.CreatedAt == 0 {
		t.Fatalf("acme = %+v", acme)
	}
}
