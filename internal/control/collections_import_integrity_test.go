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
