package control

import (
	"encoding/json"
	"testing"
)

func TestBuildRouteIndex(t *testing.T) {
	doc := buildRouteIndex("http://127.0.0.1:9966")
	if doc["kind"] != "interseptor-route-index" {
		t.Fatalf("route-index kind wrong: %v", doc["kind"])
	}
	if _, misleading := doc["openapi"]; misleading {
		t.Fatal("summary-only route index must not claim to be an OpenAPI contract")
	}
	if _, err := json.Marshal(doc); err != nil {
		t.Fatalf("route index is not valid JSON: %v", err)
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
	scannerIssues, ok := paths["/api/scanner/issues"].(map[string]any)
	if !ok {
		t.Fatal("expected /api/scanner/issues in route-index paths")
	}
	for _, m := range []string{"get", "delete"} {
		if _, ok := scannerIssues[m].(map[string]any); !ok {
			t.Errorf("expected %s on /api/scanner/issues, missing", m)
		}
	}
	scannerTargets, ok := paths["/api/scanner/targets"].(map[string]any)
	if !ok {
		t.Fatal("expected /api/scanner/targets in OpenAPI paths")
	}
	if _, ok := scannerTargets["get"].(map[string]any); !ok {
		t.Error("expected GET on /api/scanner/targets")
	}
	if _, ok := paths["/"]; ok {
		t.Fatal("the SPA catch-all / leaked into route-index paths")
	}
	if len(paths) == 0 || len(doc["routes"].([]apiRoute)) != len(apiRoutes) {
		t.Fatalf("route index does not cover the full route catalog: paths=%d routes=%d catalog=%d",
			len(paths), len(doc["routes"].([]apiRoute)), len(apiRoutes))
	}
}

func TestOpID(t *testing.T) {
	got := opID("GET", "/api/checks/{id}")
	if got != "get_checks_id" {
		t.Fatalf("opID = %q want get_checks_id", got)
	}
}
