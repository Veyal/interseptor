package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func exportBundleBytes(t *testing.T, h *Hub) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(rec, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d", rec.Code)
	}
	return rec.Body.Bytes()
}

func importBundleResult(t *testing.T, h *Hub, data []byte) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(data)))
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A project's own export imports back into it as all duplicates even when the
// stored lengths (wire sizes, HEAD/304 content-length) do not survive HAR.
func TestProjectOwnExportReimportsWithoutDuplicates(t *testing.T) {
	h, st, _ := newHub(t)
	base := time.UnixMilli(1700000000123)
	flows := []*store.Flow{
		{TS: base, Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/a?x=1", Status: 200, ReqLen: 77, ResLen: 4096},
		{TS: base.Add(time.Second), Method: "HEAD", Scheme: "http", Host: "example.com", Port: 8080, Path: "/b", Status: 200, ResLen: 1234},
		{TS: base.Add(2 * time.Second), Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/c", Status: 304, ResLen: 900},
	}
	for _, f := range flows {
		if _, err := st.InsertFlow(f); err != nil {
			t.Fatal(err)
		}
	}
	bundle := exportBundleBytes(t, h)
	res := importBundleResult(t, h, bundle)
	if res["importedFlows"].(float64) != 0 || res["skippedFlows"].(float64) != 3 {
		t.Fatalf("own export re-import: %v, want 0 imported / 3 skipped", res)
	}
	// A genuinely different flow (other timestamp) is still imported.
	if _, err := st.InsertFlow(&store.Flow{TS: base.Add(time.Hour), Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/a?x=1", Status: 200}); err != nil {
		t.Fatal(err)
	}
	h2, _, _ := newHub(t)
	if res := importBundleResult(t, h2, exportBundleBytes(t, h)); res["importedFlows"].(float64) != 4 {
		t.Fatalf("fresh project import: %v, want 4", res)
	}
}

func TestProjectImportNeverLosesLocalNotes(t *testing.T) {
	notesOf := func(st *store.Store) string {
		n, err := st.LoadNotes()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	bundleWith := func(notes string) []byte {
		b, _ := json.Marshal(projectBundle{Version: "2", Notes: notes})
		return b
	}
	cases := []struct {
		name, local, incoming, outcome string
	}{
		{"empty local takes bundle", "", "bundle notes", "imported"},
		{"identical keeps local", "same", "same", "kept"},
		{"contained keeps local", "alpha\n\nbundle notes\n\nomega", "bundle notes", "kept"},
		{"empty bundle keeps local", "mine", "", ""},
		{"different merges", "mine", "theirs", "merged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newHub(t)
			if tc.local != "" {
				if _, err := st.PersistNotes(tc.local); err != nil {
					t.Fatal(err)
				}
			}
			res := importBundleResult(t, h, bundleWith(tc.incoming))
			got := notesOf(st)
			if s, _ := res["notes"].(string); s != tc.outcome {
				t.Fatalf("notes outcome %q, want %q", s, tc.outcome)
			}
			switch tc.outcome {
			case "imported":
				if got != tc.incoming {
					t.Fatalf("notes = %q", got)
				}
			case "merged":
				if !strings.HasPrefix(got, tc.local) || !strings.Contains(got, "## Imported from") || !strings.Contains(got, tc.incoming) {
					t.Fatalf("notes = %q", got)
				}
				// Idempotent: the same bundle again appends nothing.
				before := got
				if res := importBundleResult(t, h, bundleWith(tc.incoming)); res["notes"] != "kept" {
					t.Fatalf("re-import outcome %v", res["notes"])
				}
				if notesOf(st) != before {
					t.Fatal("re-import appended the block twice")
				}
			default:
				if got != tc.local && !(tc.local == "" && got == "") {
					t.Fatalf("notes = %q, want local %q", got, tc.local)
				}
			}
		})
	}
}
