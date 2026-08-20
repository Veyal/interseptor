package postman

import (
	"encoding/json"
	"mime/multipart"
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

func TestParseHonorsDisabledBodyAndPreservesMultipartContentType(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "Body API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [{"name": "Disabled", "request": {"method": "POST", "url": "https://example.com/disabled", "body": {"mode": "raw", "raw": "ignored", "disabled": true}}},
    {"name": "Multipart", "request": {"method": "POST", "url": "https://example.com/upload", "body": {"mode": "formdata", "formdata": [{"key": "metadata", "value": "{}", "type": "text", "contentType": "application/json"}]}}}]
}`)

	got, err := Parse(collection)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Requests[0].Body != "" {
		t.Fatalf("disabled body = %q, want empty", got.Requests[0].Body)
	}
	contentType := ""
	for _, line := range strings.Split(got.Requests[1].Headers, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "content-type:") {
			contentType = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	boundary := strings.SplitN(contentType, "boundary=", 2)
	if len(boundary) != 2 {
		t.Fatalf("multipart content type = %q", contentType)
	}
	reader := multipart.NewReader(strings.NewReader(got.Requests[1].Body), boundary[1])
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read multipart part: %v", err)
	}
	if part.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("part content type = %q", part.Header.Get("Content-Type"))
	}
}

func TestParsePreservesOAuth2PlacementAndPrefix(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "OAuth API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [
    {"name": "Header token", "request": {"method": "GET", "url": "https://example.com/header", "auth": {"type": "oauth2", "oauth2": [{"key": "accessToken", "value": "example-token"}, {"key": "headerPrefix", "value": "Token"}, {"key": "addTokenTo", "value": "header"}]}}},
    {"name": "URL token", "request": {"method": "GET", "url": "https://example.com/items?limit=1#top", "auth": {"type": "oauth2", "oauth2": [{"key": "accessToken", "value": "example-token"}, {"key": "addTokenTo", "value": "queryParams"}, {"key": "tokenName", "value": "oauth_token"}]}}}
  ]
}`)

	got, err := Parse(collection)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Requests[0].Headers != "Authorization: Token example-token" {
		t.Fatalf("header auth = %q", got.Requests[0].Headers)
	}
	if got.Requests[1].URL != "https://example.com/items?limit=1&oauth_token=example-token#top" {
		t.Fatalf("URL auth = %q", got.Requests[1].URL)
	}
}

func TestParsePreservesGraphQLOperationAndExtensions(t *testing.T) {
	collection := []byte(`{
  "info": {"name": "GraphQL API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "requestId", "value": "example-request"}],
  "item": [{"name": "Get user", "request": {"method": "POST", "url": "https://example.com/graphql", "body": {"mode": "graphql", "graphql": {
    "query": "query GetUser { user { id } }",
    "variables": "{\"requestId\":\"{{requestId}}\"}",
    "operationName": "GetUser",
    "extensions": {"persistedQuery": {"version": 1, "sha256Hash": "{{requestId}}"}}
  }}}}]
}`)

	got, err := Parse(collection)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.Requests[0].Body), &body); err != nil {
		t.Fatalf("decode GraphQL body: %v", err)
	}
	if body["operationName"] != "GetUser" {
		t.Fatalf("operationName = %#v", body["operationName"])
	}
	extensions, ok := body["extensions"].(map[string]any)
	if !ok || extensions["persistedQuery"] == nil {
		t.Fatalf("extensions = %#v", body["extensions"])
	}
	variables, ok := body["variables"].(map[string]any)
	if !ok || variables["requestId"] != "example-request" {
		t.Fatalf("variables = %#v", body["variables"])
	}
}
