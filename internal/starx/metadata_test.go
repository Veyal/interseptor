package starx

import "testing"

func TestParseMetadata(t *testing.T) {
	src := `# name: JWT in response
# description: Flags a JWT returned in a response body or header.
# author: Priya
# version: 1.0.0
# severity: medium
# homepage: https://example.com/checks
# custom-field: anything

def check(flow):
    return []
`
	m := ParseMetadata(src)
	if m.Name != "JWT in response" || m.Author != "Priya" || m.Version != "1.0.0" || m.Severity != "medium" {
		t.Fatalf("parsed fields wrong: %+v", m)
	}
	if m.Extra["custom-field"] != "anything" {
		t.Fatalf("extra key not captured: %+v", m.Extra)
	}
}

func TestParseMetadataStopsAtCode(t *testing.T) {
	// a `# key: value` AFTER code must NOT be treated as metadata
	src := `def check(flow):
    # name: not metadata
    return []
`
	m := ParseMetadata(src)
	if m.Name != "" {
		t.Fatalf("in-code comment must not parse as metadata, got %+v", m)
	}
}

func TestParseMetadataEmpty(t *testing.T) {
	m := ParseMetadata("def check(flow):\n    return []\n")
	if m.Name != "" || m.Author != "" || m.Version != "" || len(m.Extra) != 0 {
		t.Fatalf("no front-matter should yield zero metadata, got %+v", m)
	}
}
