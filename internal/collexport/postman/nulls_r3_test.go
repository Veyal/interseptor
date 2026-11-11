package postman

import (
	"encoding/json"
	"strings"
	"testing"

	pmimport "github.com/Veyal/interseptor/internal/collimport/postman"
)

// The v2.1 schema allows null for auth and leaves request.method optional, and
// Postman itself writes "auth": null / "description": null in some exports. The
// lossless promise is export(import(x)) == x apart from blanked secrets, so an
// explicit null and an absent method must both come back as written.
func roundTripR3(t *testing.T, doc string) string {
	t.Helper()
	r, err := pmimport.Parse([]byte(doc), pmimport.Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExportCollection(r.Bundle(), r.Collection.UID, Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	return string(out.Data)
}

const headR3 = `{"info":{"name":"n","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[`

func TestRoundTripKeepsExplicitNullsAndAbsentMethod(t *testing.T) {
	for name, item := range map[string]string{
		"request auth null":        `{"name":"a","request":{"method":"POST","header":[],"url":"https://example.com/a","auth":null}}`,
		"request without method":   `{"name":"b","request":{"header":[],"url":"https://example.com/b"}}`,
		"request nulls":            `{"name":"c","request":{"method":"GET","header":null,"body":null,"url":"https://example.com/c","description":null}}`,
		"item nulls":               `{"name":"d","description":null,"event":null,"variable":null,"protocolProfileBehavior":null,"request":{"method":"GET","url":"https://example.com/d"},"response":null}`,
		"folder auth null":         `{"name":"f","item":[],"auth":null,"event":null}`,
		"request null method":      `{"name":"g","request":{"method":null,"url":"https://example.com/g"}}`,
		"request null":             `{"name":"h","request":null}`,
		"item id null":             `{"id":null,"name":"j","request":{"method":"GET","url":"https://example.com/j"}}`,
		"method kept when written": `{"name":"i","request":{"url":"https://example.com/i","method":"GET"}}`,
	} {
		doc := headR3 + item + `]}`
		if got := roundTripR3(t, doc); got != doc {
			t.Errorf("%s: round trip differs\n got: %s\nwant: %s", name, got, doc)
		}
	}
}

func TestRoundTripKeepsCollectionLevelNulls(t *testing.T) {
	for _, doc := range []string{
		headR3 + `],"event":null,"variable":null,"auth":null,"protocolProfileBehavior":null}`,
		`{"info":{"name":"n","description":null,"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[]}`,
	} {
		if got := roundTripR3(t, doc); got != doc {
			t.Errorf("round trip differs\n got: %s\nwant: %s", got, doc)
		}
	}
}

// An edit made after the import wins over the remembered null, and a request
// that never came from Postman still exports its method.
func TestRememberedNullYieldsToAnEdit(t *testing.T) {
	doc := headR3 + `{"name":"a","request":{"header":[],"url":"https://example.com/a","auth":null}}]}`
	r, err := pmimport.Parse([]byte(doc), pmimport.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := r.Bundle()
	for i := range b.Items {
		if b.Items[i].Kind == "request" {
			b.Items[i].Method = "DELETE"
			b.Items[i].Auth = json.RawMessage(`{"type":"noauth"}`)
		}
	}
	out, err := ExportCollection(b, r.Collection.UID, Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out.Data)
	if !strings.Contains(got, `"method":"DELETE"`) || !strings.Contains(got, `"auth":{"type":"noauth"}`) || strings.Contains(got, `"auth":null`) {
		t.Fatalf("edit lost: %s", got)
	}
	b = r.Bundle()
	for i := range b.Items {
		b.Items[i].Sidecar = nil
	}
	out, err = ExportCollection(b, r.Collection.UID, Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Data), `"method":"GET"`) {
		t.Fatalf("a request without a Postman sidecar lost its method: %s", out.Data)
	}
}
