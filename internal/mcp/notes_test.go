package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The set_note tool lets the AI annotate a flow; it PUTs the note to the control
// plane's /api/flows/{id}/note endpoint.
func TestSetNoteTool(t *testing.T) {
	var gotMethod, gotPath, gotNote string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		var b struct {
			Note string `json:"note"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		gotNote = b.Note
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mock.Close()

	s := New(mock.URL)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_note","arguments":{"id":42,"note":"IDOR here"}}}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/flows/42/note" {
		t.Fatalf("got %s %s, want PUT /api/flows/42/note", gotMethod, gotPath)
	}
	if gotNote != "IDOR here" {
		t.Fatalf("note = %q, want %q", gotNote, "IDOR here")
	}
}

// set_note documents that note "" clears the note, so schema validation must
// let an empty required string through to PUT /api/flows/{id}/note.
func TestSetNoteEmptyClearsNote(t *testing.T) {
	gotPath, gotNote := "", "unset"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		var b struct {
			Note string `json:"note"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		gotNote = b.Note
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mock.Close()

	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_note","arguments":{"id":42,"note":""}}}` + "\n")
	if err := New(mock.URL).Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotPath != "PUT /api/flows/42/note" || gotNote != "" {
		t.Fatalf("got %q note=%q, want PUT /api/flows/42/note with an empty note; out=%s", gotPath, gotNote, out.String())
	}
}

func TestSetNotesEmptyIsAccepted(t *testing.T) {
	if err := validateArgs(obj(map[string]any{"notes": pt("string")}, "notes"), map[string]any{"notes": ""}); err != nil {
		t.Fatalf("set_notes {notes:\"\"} rejected: %v", err)
	}
}
