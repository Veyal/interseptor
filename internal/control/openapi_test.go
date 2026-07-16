package control

import (
	"testing"
)

func TestBuildOpenAPI(t *testing.T) {
	doc := buildOpenAPI("http://127.0.0.1:9966")
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi version wrong: %v", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)
	// The checks surface must appear with its methods grouped under one path.
	checks, ok := paths["/api/checks/{id}"].(map[string]any)
	if !ok {
		t.Fatalf("expected /api/checks/{id} in paths, got %v", paths)
	}
	for _, m := range []string{"get", "put", "delete"} {
		if _, ok := checks[m].(map[string]any); !ok {
			t.Errorf("expected %s on /api/checks/{id}, missing", m)
		}
	}
	if _, ok := paths["/"]; ok {
		t.Fatal("the SPA catch-all / leaked into openapi paths")
	}
}

func TestOpID(t *testing.T) {
	got := opID("GET", "/api/checks/{id}")
	if got != "get_checks_id" {
		t.Fatalf("opID = %q want get_checks_id", got)
	}
}
