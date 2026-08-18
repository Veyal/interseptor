package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The project notebook round-trips through PUT/GET /api/notes (per-project markdown
// for creds, findings, scratch). Inline data-URL images migrate to SQLite refs.
func TestProjectNotesEndpoint(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Empty by default.
	resp, err := http.Get(ts.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Notes != "" {
		t.Fatalf("default notes = %q, want empty", out.Notes)
	}

	// Save.
	body, _ := json.Marshal(map[string]string{"notes": "# Creds\nadmin:hunter2\n\n## Findings\n- IDOR on /api/users"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/notes", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	wresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	wresp.Body.Close()
	if wresp.StatusCode/100 != 2 {
		t.Fatalf("PUT notes status %d", wresp.StatusCode)
	}

	// Read back.
	resp2, err := http.Get(ts.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	json.NewDecoder(resp2.Body).Decode(&out)
	if !strings.Contains(out.Notes, "admin:hunter2") || !strings.Contains(out.Notes, "IDOR") {
		t.Fatalf("notes not persisted: %q", out.Notes)
	}
}

func TestProjectNotesImageUploadAndServe(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	up, _ := json.Marshal(map[string]string{
		"mime": "image/png",
		"data": "aGVsbG8=", // "hello"
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/notes/images", strings.NewReader(string(up)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST image status %d", resp.StatusCode)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID <= 0 {
		t.Fatalf("create image: id=%d err=%v", created.ID, err)
	}

	imgResp, err := http.Get(ts.URL + "/api/notes/images/" + strconv.FormatInt(created.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusOK {
		t.Fatalf("GET image status %d", imgResp.StatusCode)
	}
	if ct := imgResp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestProjectNotesStorageFailuresReturnScrubbed500(t *testing.T) {
	tests := []struct {
		name string
		call func(*projectAPI, http.ResponseWriter, *http.Request)
		body string
	}{
		{"replace", (*projectAPI).putNotes, `{"notes":"replacement"}`},
		{"append", (*projectAPI).patchNotes, `{"appendText":"addition"}`},
		{"image", (*projectAPI).postNotesImage, `{"mime":"image/png","data":"aGVsbG8="}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := newHub(t)
			if err := st.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			tt.call(&projectAPI{h}, rec, req)
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal server error") {
				t.Fatalf("status=%d body=%q, want scrubbed 500", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProjectNotesInvalidInlineImageRemainsClientError(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPut, "/api/notes", strings.NewReader(`{"notes":"![bad](data:image/png;base64,A)"}`))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).putNotes(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid pasted image") {
		t.Fatalf("status=%d body=%q, want descriptive 400", rec.Code, rec.Body.String())
	}
}

func TestProjectNotesMutationsRejectTrailingJSONBeforeChangingState(t *testing.T) {
	request := func(t *testing.T, method, url, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, url, strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return resp
	}

	t.Run("replace", func(t *testing.T) {
		h, st, _ := newHub(t)
		if _, err := st.PersistNotes("old notes"); err != nil {
			t.Fatalf("PersistNotes: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := request(t, http.MethodPut, ts.URL+"/api/notes", `{"notes":"new notes"}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		notes, err := st.LoadNotes()
		if err != nil {
			t.Fatalf("LoadNotes: %v", err)
		}
		if notes != "old notes" {
			t.Fatalf("notes = %q, want old notes", notes)
		}
	})

	t.Run("append", func(t *testing.T) {
		h, st, _ := newHub(t)
		if _, err := st.PersistNotes("old notes"); err != nil {
			t.Fatalf("PersistNotes: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := request(t, http.MethodPatch, ts.URL+"/api/notes", `{"appendText":"new notes"}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		notes, err := st.LoadNotes()
		if err != nil {
			t.Fatalf("LoadNotes: %v", err)
		}
		if notes != "old notes" {
			t.Fatalf("notes = %q, want old notes", notes)
		}
	})

	t.Run("image", func(t *testing.T) {
		h, st, _ := newHub(t)
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := request(t, http.MethodPost, ts.URL+"/api/notes/images", `{"mime":"image/png","data":"aGVsbG8="}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		exists, err := st.NotesImageExists(1)
		if err != nil {
			t.Fatalf("NotesImageExists: %v", err)
		}
		if exists {
			t.Fatal("image was stored after rejected upload")
		}
	})
}

func TestProjectNotesMigratesInlineDataURL(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	inline := "![shot](data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7)"
	body, _ := json.Marshal(map[string]string{"notes": inline})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/notes", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	wresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	wresp.Body.Close()
	if wresp.StatusCode/100 != 2 {
		t.Fatalf("PUT notes status %d", wresp.StatusCode)
	}

	resp, err := http.Get(ts.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if strings.Contains(out.Notes, "data:image/") {
		t.Fatalf("expected migrated notes, got %q", out.Notes)
	}
	if !strings.Contains(out.Notes, "/api/notes/images/") {
		t.Fatalf("expected image ref, got %q", out.Notes)
	}
}

// PATCH /api/notes appends atomically server-side. This is the fix for the
// append_notes lost-update race: the old MCP tool did a client-side
// GET-then-PUT which could clobber a concurrent writer's append. Firing N
// concurrent PATCH requests must never lose an entry.
func TestPatchNotesAppendConcurrentNoLoss(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{"appendText": fmt.Sprintf("entry-%d", i)})
			req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/notes", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("PATCH notes: %v", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				t.Errorf("PATCH notes status %d", resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	resp, err := http.Get(ts.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("entry-%d", i)
		if !strings.Contains(out.Notes, want) {
			t.Fatalf("lost update: missing %q in final notes", want)
		}
	}
}

// PATCH /api/notes with an empty appendText is rejected — nothing meaningful
// to append.
func TestPatchNotesRequiresAppendText(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"appendText": ""})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/notes", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
