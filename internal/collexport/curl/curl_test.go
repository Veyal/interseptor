package curl

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	curlimp "github.com/Veyal/interseptor/internal/collimport/curl"
	"github.com/Veyal/interseptor/internal/collimport/insomnia"
	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func TestGolden(t *testing.T) {
	data, err := os.ReadFile("../../collimport/insomnia/testdata/v4.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := insomnia.Parse(data, insomnia.Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	b := r.Bundle()
	// emulate the scrub the store applies to literal credentials
	for i := range b.Items {
		if strings.Contains(string(b.Items[i].Auth), "literal-secret") {
			b.Items[i].Auth = json.RawMessage(strings.ReplaceAll(string(b.Items[i].Auth), "literal-secret", ""))
		}
	}
	out, err := ExportCollection(b, r.Collection.UID, Options{Insecure: true, PathAsIs: true, Shebang: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Commands != 4 {
		t.Fatalf("commands = %d", out.Commands)
	}
	const golden = "testdata/v4.golden.sh"
	if *update {
		if err := os.WriteFile(golden, out.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(out.Data, want) {
		t.Fatalf("golden mismatch; rerun with -update and review the diff\n%s", out.Data)
	}
	s := string(out.Data)
	if strings.Contains(s, "literal-secret") || !strings.Contains(s, Placeholder) {
		t.Fatalf("credential handling:\n%s", s)
	}
}

// export then re-import with the curl importer: the commands we write must be
// understood by the curl importer.
func TestRoundTripThroughCurlImporter(t *testing.T) {
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
		{UID: "f", CollectionUID: "c", Kind: "folder", Rank: "a", Name: "F\nevil"},
		{UID: "r1", CollectionUID: "c", ParentUID: "f", Kind: "request", Rank: "a", Name: "post", Method: "POST",
			URL:     json.RawMessage(`{"raw":"https://example.com/a?x=1"}`),
			Headers: json.RawMessage(`[{"key":"X-It's","value":"a'b"},{"key":"Off","value":"1","disabled":true}]`),
			Body:    json.RawMessage(`{"mode":"raw","raw":"{\"a\":\"it's\"}","options":{"raw":{"language":"json"}}}`)},
		{UID: "r2", CollectionUID: "c", Kind: "request", Rank: "b", Name: "form", Method: "POST",
			URL:  json.RawMessage(`"https://example.com/f"`),
			Body: json.RawMessage(`{"mode":"urlencoded","urlencoded":[{"key":"a","value":"1 2"},{"key":"b","value":"x","disabled":true}]}`),
			Auth: json.RawMessage(`{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":""}]}`)},
	}}
	out, err := ExportCollection(b, "c", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Data), "\nevil") {
		t.Fatalf("newline injection in comment:\n%s", out.Data)
	}
	res, err := curlimp.Parse(out.Data, curlimp.Options{NewID: counter()})
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("re-import: %v %d\n%s", err, len(res.Items), out.Data)
	}
	if !strings.Contains(string(res.Items[0].Body), `it's`) || !strings.Contains(string(res.Items[0].Headers), "a'b") {
		t.Fatalf("quoting lost: %s / %s", res.Items[0].Body, res.Items[0].Headers)
	}
	if strings.Contains(string(res.Items[0].Headers), "Off") {
		t.Fatal("disabled header exported")
	}
}

func TestNoCollection(t *testing.T) {
	if _, err := ExportCollection(store.CollectionsBundle{}, "x", Options{}); err != ErrNoCollection {
		t.Fatalf("err = %v", err)
	}
}

func TestShellInjectionInValues(t *testing.T) {
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
		{UID: "r", CollectionUID: "c", Kind: "request", Rank: "a", Name: "x $(id)", Method: "GET",
			URL: json.RawMessage(`{"raw":"https://example.com/$(touch /tmp/x);'` + "`id`" + `"}`)},
	}}
	out, _ := ExportCollection(b, "c", Options{})
	if !strings.Contains(string(out.Data), `'https://example.com/$(touch /tmp/x);'\''`+"`id`"+`'`) {
		t.Fatalf("URL not single-quoted safely:\n%s", out.Data)
	}
}
