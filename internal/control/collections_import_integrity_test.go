package control

import (
	"strings"
	"testing"
)

const bearerCollectionFixture = `{"info":{"name":"Bearer demo","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},` +
	`"item":[{"name":"Orders","request":{"method":"GET","header":[{"key":"Authorization","value":"Bearer {{token}}"}],` +
	`"url":{"raw":"https://api.example.com/orders","protocol":"https","host":["api","example","com"],"path":["orders"]}}}]}`

// "Authorization: Bearer {{token}}" holds no literal secret, so importing it
// must keep it; before the fix the import scrub blanked the whole header and
// every imported bearer-auth request was sent unauthenticated.
func TestImportKeepsBearerTemplateHeader(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", bearerCollectionFixture, asUI, 201, &out)
	items, _ := f.st.ListItems(out.CollectionUID)
	for _, it := range items {
		if it.Kind == "request" && !strings.Contains(string(it.Headers), "Bearer {{token}}") {
			t.Fatalf("imported header lost its template: %s", it.Headers)
		}
	}
}

// Re-importing a collection that already exists merges into it; the response
// must name the stored collection, not a uid that was never written.
func TestReimportReturnsTheStoredCollectionUID(t *testing.T) {
	f := newCollFixture(t)
	var first, second struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", bearerCollectionFixture, asUI, 201, &first)
	f.must("POST", "/api/import/collection/commit", bearerCollectionFixture, asUI, 201, &second)
	if second.CollectionUID != first.CollectionUID {
		t.Fatalf("re-import returned %q, the stored collection is %q", second.CollectionUID, first.CollectionUID)
	}
	if _, err := f.st.GetCollection(second.CollectionUID); err != nil {
		t.Fatalf("returned collection does not exist: %v", err)
	}
}

// A variable declared "default" but named like a credential (the common
// Postman `token` that a login script fills) must not reach the AI channel in
// clear text; the owner's own UI session still sees it.
func TestAIChannelMasksSecretNamedDefaultVariables(t *testing.T) {
	f := newCollFixture(t)
	const tokenValue = "CANARY-DEFAULT-TYPED-TOKEN"
	var env struct {
		UID string `json:"uid"`
	}
	f.must("POST", "/api/environments", map[string]any{"name": "dev", "kind": "env",
		"variables": []map[string]any{{"key": "token", "type": "default", "enabled": true}, {"key": "baseUrl", "type": "default", "enabled": true, "initialValue": "https://api.example.com"}}}, asUI, 201, &env)
	f.must("PUT", "/api/variables/environment/"+env.UID+"/current", map[string]any{"key": "token", "value": tokenValue}, asUI, 200, nil)

	for _, path := range []string{"/api/environments", "/api/environments/" + env.UID, "/api/variables/environment/" + env.UID} {
		if body := f.must("GET", path, nil, asAI, 200, nil); strings.Contains(body, tokenValue) {
			t.Fatalf("AI channel read of %s leaked a credential-named variable", path)
		} else if !strings.Contains(body, "api.example.com") {
			t.Fatalf("AI channel read of %s lost the ordinary variable: %s", path, body)
		}
		if body := f.must("GET", path, nil, asUI, 200, nil); !strings.Contains(body, tokenValue) {
			t.Fatalf("the owner's UI session must still see its own value in %s", path)
		}
	}
}
