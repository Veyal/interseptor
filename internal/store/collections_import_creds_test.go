package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// userImportBundle is what an importer hands over for a file the user chose:
// literal credentials in every shape the scrub knows about.
func userImportBundle() CollectionsBundle {
	return CollectionsBundle{
		Version:     CollectionsBundleVersion,
		Collections: []Collection{{UID: "c1", Name: "imp", Auth: json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"BT123"}]}`)}},
		Items: []Item{{UID: "i1", CollectionUID: "c1", Kind: "request", Name: "r", Method: "POST",
			URL:     json.RawMessage(`{"raw":"https://example.com/a?token=T1&page=2"}`),
			Headers: json.RawMessage(`[{"key":"X-Api-Key","value":"abc"}]`),
			Body:    json.RawMessage(`{"mode":"raw","raw":"{\"password\":\"hunter2\"}"}`),
			Auth:    json.RawMessage(`{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":"p4ss"}]}`)}},
	}
}

func storedItemJSON(t *testing.T, s *Store) string {
	t.Helper()
	b, err := s.ExportCollectionsBundle(ScrubOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}

// A user deliberately importing their own file must keep its credentials:
// scrubbing on import silently turns every authenticated request into a 401.
func TestUserImportKeepsLiteralCredentials(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.ImportUserCollectionsBundle(userImportBundle()); err != nil {
		t.Fatal(err)
	}
	got := storedItemJSON(t, s)
	for _, want := range []string{"BT123", "T1", "abc", "hunter2", "p4ss"} {
		if !strings.Contains(got, want) {
			t.Errorf("credential %q was destroyed by user import: %s", want, got)
		}
	}
}

// Keeping credentials at rest must not weaken any export.
func TestUserImportedCredentialsStillScrubbedOnExport(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.ImportUserCollectionsBundle(userImportBundle()); err != nil {
		t.Fatal(err)
	}
	b, err := s.ExportCollectionsBundle(ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	assertNoCanary(t, "export after user import", raw, []string{"BT123", "T1\"", "hunter2", "p4ss", "\"abc\""})
}

// Peer/project bundles stay untrusted: ImportCollectionsBundle still scrubs.
func TestPeerImportStillScrubs(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ImportCollectionsBundle(userImportBundle()); err != nil {
		t.Fatal(err)
	}
	got := storedItemJSON(t, s)
	for _, bad := range []string{"BT123", "hunter2", "p4ss"} {
		if strings.Contains(got, bad) {
			t.Errorf("untrusted import kept %q", bad)
		}
	}
}
