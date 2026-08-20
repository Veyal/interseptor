package postman

import (
	"strings"
	"testing"
)

func TestParseCollectionV21FlattensFoldersAndResolvesEnvironment(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "Example API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "baseUrl", "value": "https://example.com"}],
  "item": [{
    "name": "Accounts",
    "item": [{
      "name": "List accounts",
      "request": {
        "method": "GET",
        "header": [
          {"key": "Accept", "value": "application/json"},
          "X-Mixed: supported",
          {"key": "X-Disabled", "value": "ignore", "disabled": true}
        ],
        "url": {"raw": "{{baseUrl}}/api/accounts?role={{role}}"}
      }
    }, {
      "name": "Create account",
      "request": {
        "method": "POST",
        "url": "{{baseUrl}}/api/accounts",
        "body": {"mode": "raw", "raw": "{\"name\":\"alice\"}"}
      }
    }]
  }]
}`)
	environment := []byte(`{
  "name": "Example test",
  "values": [{"key": "role", "value": "auditor", "enabled": true}]
}`)

	got, err := ParseWithEnvironment(collection, environment)
	if err != nil {
		t.Fatalf("ParseWithEnvironment: %v", err)
	}
	if got.Name != "Example API" {
		t.Fatalf("collection name = %q, want Example API", got.Name)
	}
	if len(got.Requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(got.Requests))
	}

	first := got.Requests[0]
	if first.Name != "List accounts" || first.Folder != "Accounts" {
		t.Fatalf("first request identity = %#v", first)
	}
	if first.Method != "GET" || first.URL != "https://example.com/api/accounts?role=auditor" {
		t.Fatalf("first request = %#v", first)
	}
	if first.Headers != "Accept: application/json\nX-Mixed: supported" {
		t.Fatalf("first headers = %q", first.Headers)
	}
	if got.Unresolved != nil {
		t.Fatalf("unexpected unresolved variables: %#v", got.Unresolved)
	}

	second := got.Requests[1]
	if second.Method != "POST" || second.Body != `{"name":"alice"}` {
		t.Fatalf("second request = %#v", second)
	}
}

func TestParseCollectionSupportsInheritedBearerAuthAndStringURL(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "Auth API", "schema": "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"},
  "variable": [{"key": "token", "value": "example-token"}],
  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}"}]},
  "item": [{"name": "Health", "request": "https://example.com/health"}]
}`)

	got, err := Parse(collection)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(got.Requests))
	}
	request := got.Requests[0]
	if request.Method != "GET" || request.URL != "https://example.com/health" {
		t.Fatalf("request = %#v", request)
	}
	if request.Headers != "Authorization: Bearer example-token" {
		t.Fatalf("headers = %q", request.Headers)
	}
}

func TestParseCollectionReportsUnresolvedVariablesAndUnsupportedBodies(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "Mixed API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [{
    "name": "Upload",
    "request": {
      "method": "POST",
      "url": "https://example.com/upload/{{missing}}",
      "body": {"mode": "formdata", "formdata": [{"key": "file", "type": "file", "src": "fixture.txt"}]}
    }
  }]
}`)

	got, err := Parse(collection)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(got.Requests))
	}
	if got.Requests[0].URL != "https://example.com/upload/{{missing}}" {
		t.Fatalf("unresolved URL = %q", got.Requests[0].URL)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0] != "missing" {
		t.Fatalf("unresolved = %#v", got.Unresolved)
	}
	if len(got.Requests[0].Warnings) != 1 || !strings.Contains(got.Requests[0].Warnings[0], "file") {
		t.Fatalf("request warnings = %#v", got.Requests[0].Warnings)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "Upload") {
		t.Fatalf("collection warnings = %#v", got.Warnings)
	}
}

func TestParseRejectsNonCollection(t *testing.T) {
	if _, err := Parse([]byte(`{"name":"not a collection"}`)); err == nil {
		t.Fatal("Parse accepted a non-collection document")
	}
}
