package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestPassiveCheckRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	source := "def check(flow):\n    return []\n"
	body := `{"source":` + strconv.Quote(source) + `,"flowId":` + strconv.FormatInt(id, 10) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/checks/test", strings.NewReader(body))
	rec := httptest.NewRecorder()
	(&checksAPI{Hub: h}).testCheck(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestLegacyActiveCheckFilesRemainInert(t *testing.T) {
	h, _, _ := newHub(t)
	h.ChecksDir = filepath.Join(t.TempDir(), "checks")
	legacyDir := filepath.Join(filepath.Dir(h.ChecksDir), "active-checks")
	legacyPath := filepath.Join(legacyDir, "legacy.star")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("def check(point, baseline, probe):\n    return []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&checksAPI{Hub: h}).listChecks(rec, httptest.NewRequest(http.MethodGet, "/api/checks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var out struct {
		Checks []struct {
			ID string `json:"id"`
		} `json:"checks"`
		Active any `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Active != nil {
		t.Fatalf("legacy active-check field is still exposed: %v", out.Active)
	}
	for _, check := range out.Checks {
		if check.ID == "legacy" {
			t.Fatal("legacy active check was loaded as a passive check")
		}
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy active-check file was not preserved: %v", err)
	}
}
