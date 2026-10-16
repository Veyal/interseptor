package control

import (
	"strings"
	"testing"
)

const credCollectionFixture = `{"info":{"name":"Creds","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"auth":{"type":"bearer","bearer":[{"key":"token","value":"BT123","type":"string"}]},
"item":[{"name":"Login","request":{"method":"POST","header":[{"key":"X-Api-Key","value":"abc"}],
"auth":{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":"p4ss"}]},
"body":{"mode":"raw","raw":"{\"password\":\"hunter2\"}"},
"url":{"raw":"https://api.example.com/login?token=T1","protocol":"https","host":["api","example","com"],"path":["login"],"query":[{"key":"token","value":"T1"}]}}}]}`

// A user importing their own file keeps every literal credential: the import
// is the user's deliberate act, and exports still scrub.
func TestUserImportKeepsCredentialsEndToEnd(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", credCollectionFixture, asUI, 201, &out)
	items, _ := f.st.ListItems(out.CollectionUID)
	var got string
	for _, it := range items {
		got += string(it.Headers) + string(it.Auth) + string(it.Body) + string(it.URL)
	}
	coll, _ := f.st.GetCollection(out.CollectionUID)
	got += string(coll.Auth)
	for _, want := range []string{"BT123", "abc", "p4ss", "hunter2", "T1"} {
		if !strings.Contains(got, want) {
			t.Errorf("credential %q lost on import: %s", want, got)
		}
	}
}
