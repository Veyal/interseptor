package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A UI autosave carries the revision it loaded; when an agent appended in the
// meantime the PUT is refused with 409 instead of silently dropping the append.
func TestPutNotesWithStaleBaseRevConflicts(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.PersistNotes("# base"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Notes string `json:"notes"`
		Rev   string `json:"rev"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.Rev != store.NotesRev("# base") {
		t.Fatalf("GET rev = %q, want rev of the stored notes", got.Rev)
	}
	if err := st.AppendNote("agent line"); err != nil {
		t.Fatal(err)
	}

	put := func(body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/notes", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	stale := put(`{"notes":"# base\nlocal","baseRev":"` + got.Rev + `"}`)
	stale.Body.Close()
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale PUT status = %d, want 409", stale.StatusCode)
	}
	if notes, _ := st.LoadNotes(); notes != "# base\n\nagent line" {
		t.Fatalf("agent append lost: %q", notes)
	}

	fresh := put(`{"notes":"# base\n\nagent line\nlocal","baseRev":"` + store.NotesRev("# base\n\nagent line") + `"}`)
	var saved struct {
		Rev string `json:"rev"`
	}
	json.NewDecoder(fresh.Body).Decode(&saved)
	fresh.Body.Close()
	if fresh.StatusCode != http.StatusOK || saved.Rev != store.NotesRev("# base\n\nagent line\nlocal") {
		t.Fatalf("fresh PUT status = %d rev = %q", fresh.StatusCode, saved.Rev)
	}

	// Callers without a revision (MCP set_notes, older clients) still replace.
	legacy := put(`{"notes":"replaced"}`)
	legacy.Body.Close()
	if legacy.StatusCode/100 != 2 {
		t.Fatalf("legacy PUT status = %d", legacy.StatusCode)
	}
	if notes, _ := st.LoadNotes(); notes != "replaced" {
		t.Fatalf("legacy PUT notes = %q", notes)
	}
}
