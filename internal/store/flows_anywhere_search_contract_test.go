package store

import (
	"testing"
	"time"
)

func Test_SearchFlowsAnywhere_matches_every_flow_surface_case_insensitively(t *testing.T) {
	// Given
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cases := []Flow{
		{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "url.example.com", Path: "/Needle-url", Status: 200},
		{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com", Path: "/request-header", Status: 200, ReqHeaders: map[string][]string{"X-Test": {"Needle-request-header"}}},
		{TS: time.UnixMilli(3), Method: "GET", Scheme: "https", Host: "example.com", Path: "/response-header", Status: 200, ResHeaders: map[string][]string{"X-Test": {"Needle-response-header"}}},
		{TS: time.UnixMilli(4), Method: "POST", Scheme: "https", Host: "example.com", Path: "/request-body", Status: 201, ReqBodyHash: writeAnywhereTestBody(t, s, "Needle-request-body")},
		{TS: time.UnixMilli(5), Method: "GET", Scheme: "https", Host: "example.com", Path: "/response-body", Status: 202, ResBodyHash: writeAnywhereTestBody(t, s, "Needle-response-body")},
		{TS: time.UnixMilli(6), Method: "PATCH", Scheme: "https", Host: "example.com", Path: "/metadata", Status: 203, Mime: "application/Needle-metadata", Note: "Needle-note"},
	}
	ids := make([]int64, 0, len(cases)+1)
	for i := range cases {
		id, insertErr := s.InsertFlow(&cases[i])
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		ids = append(ids, id)
	}
	taggedID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(7), Method: "GET", Scheme: "https", Host: "example.com", Path: "/tag", Status: 204})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetFlowTags(taggedID, []string{"Needle-tag"}); err != nil {
		t.Fatal(err)
	}
	ids = append(ids, taggedID)

	// When / Then
	for i, term := range []string{"needle-URL", "NEEDLE-REQUEST-HEADER", "needle-response-header", "NEEDLE-request-body", "needle-RESPONSE-body", "needle-METADATA", "NEEDLE-TAG"} {
		result, _, searchErr := s.FlowIDsAnywhereSearch(FlowFilter{Search: term}, 8000)
		if searchErr != nil {
			t.Fatalf("search %q: %v", term, searchErr)
		}
		if len(result) != 1 || result[0] != ids[i] {
			t.Fatalf("search %q ids = %v, want [%d]", term, result, ids[i])
		}
	}
}

func Test_FlowIDsBodySearch_matches_bodies_only(t *testing.T) {
	// Given
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	bodyID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/body", Status: 200, ReqBodyHash: writeAnywhereTestBody(t, s, "needle-body")})
	if err != nil {
		t.Fatal(err)
	}
	metadataID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "needle-metadata.example.com", Path: "/path", Status: 200})
	if err != nil {
		t.Fatal(err)
	}

	// When
	bodyIDs, err := flowIDsForBodySearch(t, s, "needle-body")
	metadataIDs, err := flowIDsForBodySearch(t, s, "needle-metadata")

	// Then
	if len(bodyIDs) != 1 || bodyIDs[0] != bodyID {
		t.Fatalf("body ids = %v, want [%d]", bodyIDs, bodyID)
	}
	if len(metadataIDs) != 0 {
		t.Fatalf("metadata ids = %v, want no match for non-body flow %d", metadataIDs, metadataID)
	}
}

func flowIDsForBodySearch(t *testing.T, s *Store, term string) ([]int64, error) {
	t.Helper()
	ids, _, err := s.FlowIDsBodySearch(FlowFilter{Search: term}, 8000)
	return ids, err
}

func Test_SearchFlowsAnywhere_applies_filters_before_deterministic_candidate_bound(t *testing.T) {
	// Given
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	bodyHash := writeAnywhereTestBody(t, s, "bounded-needle")
	for i := 0; i < 3; i++ {
		_, insertErr := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "POST", Scheme: "https", Host: "api.example.com", Path: "/match", Status: 200 + i, ResBodyHash: bodyHash})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	_, err = s.InsertFlow(&Flow{TS: time.UnixMilli(4), Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/excluded", Status: 200, ResBodyHash: bodyHash})
	if err != nil {
		t.Fatal(err)
	}

	// When
	firstIDs, firstNote, err := s.FlowIDsBodySearch(FlowFilter{Search: "BOUNDED-NEEDLE", Method: "POST", Host: "api.example.com", StatusClass: 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	secondIDs, secondNote, err := s.FlowIDsBodySearch(FlowFilter{Search: "bounded-needle", Method: "POST", Host: "api.example.com", StatusClass: 2}, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if len(firstIDs) != 2 || firstIDs[0] <= firstIDs[1] {
		t.Fatalf("bounded ids = %v, want two newest filtered ids", firstIDs)
	}
	if firstIDs[0] != secondIDs[0] || firstIDs[1] != secondIDs[1] || firstNote == "" || firstNote != secondNote {
		t.Fatalf("bounded result not deterministic: first=%v/%q second=%v/%q", firstIDs, firstNote, secondIDs, secondNote)
	}
}

func writeAnywhereTestBody(t *testing.T, s *Store, body string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
