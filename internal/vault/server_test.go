package vault

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := OpenAuth(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := auth.EnsureBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(st, auth)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	z := writeMiniZip(t)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/vault/projects/demo?label=from-test", bytes.NewReader(z))
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put %d %s", resp.StatusCode, b)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/vault/projects", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Projects []ProjectInfo `json:"projects"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Projects) != 1 || out.Projects[0].ID != "demo" {
		t.Fatalf("list=%+v", out)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/vault/projects/demo/latest", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(body) < 10 {
		t.Fatalf("download %d len=%d", resp.StatusCode, len(body))
	}

	// read-only token cannot PUT
	ro, _, err := auth.Create(ScopeRead, "ro")
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/vault/projects/demo", bytes.NewReader(z))
	req.Header.Set("Authorization", "Bearer "+ro)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 got %d", resp.StatusCode)
	}
}
