package postman

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pmimport "github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

const fx = "../../collimport/postman/testdata/"

func counterID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("ID%04d", n) }
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fx + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compact(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func importFx(t *testing.T, name string) *pmimport.Result {
	t.Helper()
	r, err := pmimport.Parse(read(t, name), pmimport.Options{NewID: counterID()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Golden: export(import(x)) is byte-identical (compact) to x, so every key,
// its order, exec line arrays and unknown vendor keys survive.
func TestGoldenRoundTripBytes(t *testing.T) {
	for _, name := range []string{"collection_v21.json", "collection_v20.json"} {
		r := importFx(t, name)
		out, err := ExportCollection(r.Bundle(), r.Collection.UID, Options{Compact: true})
		if err != nil {
			t.Fatal(err)
		}
		// secret variable values are removed at import by design
		want := strings.ReplaceAll(compact(t, read(t, name)), "CANARY-COLL-SECRET", "")
		if got := string(out.Data); got != want {
			t.Errorf("%s: round trip differs\n got: %s\nwant: %s", name, got, want)
		}
	}
}

func TestGoldenRoundTripEnvironmentAndGlobals(t *testing.T) {
	for _, name := range []string{"environment.json", "globals.json"} {
		r := importFx(t, name)
		e := r.Environments[0]
		out, err := ExportEnvironment(e.Environment, e.Variables, e.Sidecar, Options{Compact: true})
		if err != nil {
			t.Fatal(err)
		}
		// secret values were removed at import, so compare against the fixture
		// with the secret value blanked.
		want := strings.ReplaceAll(compact(t, read(t, name)), "CANARY-ENV-SECRET", "")
		if string(out.Data) != want {
			t.Errorf("%s:\n got: %s\nwant: %s", name, out.Data, want)
		}
	}
}

// normalise removes ids/ranks so two independent imports can be compared.
func normalise(r *pmimport.Result) ([]store.Item, []store.Variable, store.Collection) {
	items := append([]store.Item(nil), r.Items...)
	for i := range items {
		items[i].UID, items[i].CollectionUID, items[i].ParentUID, items[i].Rank, items[i].TS = "", "", "", "", 0
	}
	vars := append([]store.Variable(nil), r.Variables...)
	for i := range vars {
		vars[i].OwnerUID = ""
	}
	c := r.Collection
	c.UID, c.TS, c.ImportReport = "", 0, nil
	return items, vars, c
}

func TestImportExportImportEqual(t *testing.T) {
	a := importFx(t, "collection_v21.json")
	out, err := ExportCollection(a.Bundle(), a.Collection.UID, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := pmimport.Parse(out.Data, pmimport.Options{NewID: counterID()})
	if err != nil {
		t.Fatal(err)
	}
	ai, av, ac := normalise(a)
	bi, bv, bc := normalise(b)
	// compare as JSON: nil and empty RawMessage are the same stored value
	j := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if j(ai) != j(bi) || j(av) != j(bv) || j(ac) != j(bc) {
		t.Fatal("import>export>import differs")
	}
	if a.Report.Headline != b.Report.Headline {
		t.Fatalf("report differs: %q vs %q", a.Report.Headline, b.Report.Headline)
	}
}

func TestEditsSurviveExport(t *testing.T) {
	r := importFx(t, "collection_v21.json")
	b := r.Bundle()
	for i := range b.Items {
		if b.Items[i].Name == "Create user" {
			b.Items[i].Method = "PUT"
			b.Items[i].DescriptionMD = "edited"
		}
	}
	for i := range b.Variables {
		if b.Variables[i].Key == "baseUrl" {
			b.Variables[i].InitialValue = "https://example.net"
		}
		if b.Variables[i].Key == "legacy" {
			b.Variables = append(b.Variables[:i], b.Variables[i+1:]...)
			break
		}
	}
	b.Variables = append(b.Variables, store.Variable{OwnerKind: store.VarOwnerCollection, OwnerUID: r.Collection.UID, Key: "added", Type: store.VarTypeDefault, InitialValue: "v", Enabled: true})
	out, err := ExportCollection(b, r.Collection.UID, Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Data)
	for _, want := range []string{`"method":"PUT"`, `"description":"edited"`, `"value":"https://example.net"`, `"key":"added"`} {
		if !strings.Contains(s, want) {
			t.Errorf("edit missing: %s", want)
		}
	}
	if strings.Contains(s, `"key":"legacy"`) {
		t.Error("deleted variable still exported")
	}
}

func TestSecretVariableValueBlankedUnlessOptIn(t *testing.T) {
	r := importFx(t, "collection_v21.json")
	b := r.Bundle()
	for i := range b.Variables {
		if b.Variables[i].Key == "apiKey" {
			b.Variables[i].InitialValue = "CANARY-FORCED-SECRET"
		}
	}
	out, _ := ExportCollection(b, r.Collection.UID, Options{})
	if bytes.Contains(out.Data, []byte("CANARY-FORCED-SECRET")) || bytes.Contains(out.Data, []byte("CANARY-COLL-SECRET")) {
		t.Fatal("secret variable value leaked")
	}
	out, _ = ExportCollection(b, r.Collection.UID, Options{IncludeSecrets: true})
	if !bytes.Contains(out.Data, []byte("CANARY-FORCED-SECRET")) {
		t.Fatal("opt-in should keep the value")
	}
}

// Canary test through the real store: import, persist, export the scrubbed
// bundle (the single scrub function), render Postman JSON and grep it.
func TestCanaryThroughStoreScrub(t *testing.T) {
	r := importFx(t, "collection_v21.json")
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	persist(t, st, r)
	bundle, err := st.ExportCollectionsBundle(store.ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Collections) != 1 {
		t.Fatalf("collections: %d", len(bundle.Collections))
	}
	out, err := ExportCollection(bundle, bundle.Collections[0].UID, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"CANARY-COLL-SECRET", "CANARY-BASIC-LITERAL", "CANARY-HEADER-LITERAL"} {
		if bytes.Contains(out.Data, []byte(c)) {
			t.Errorf("canary %s leaked into the Postman export", c)
		}
	}
	// the scrub keeps shareable data
	for _, keep := range []string{"example.com", `"X-Dup"`} {
		if !bytes.Contains(out.Data, []byte(keep)) {
			t.Errorf("scrub removed shareable %s", keep)
		}
	}
	// the include-secrets path keeps literals (explicit opt-in)
	full, _ := st.ExportCollectionsBundle(store.ScrubOptions{IncludeSecrets: true})
	fo, _ := ExportCollection(full, full.Collections[0].UID, Options{IncludeSecrets: true})
	if !bytes.Contains(fo.Data, []byte("CANARY-BASIC-LITERAL")) {
		t.Fatal("include-secrets export should keep literal credentials")
	}
}

func persist(t *testing.T, st *store.Store, r *pmimport.Result) {
	t.Helper()
	c, err := st.CreateCollection(r.Collection)
	if err != nil {
		t.Fatal(err)
	}
	_ = c
	for _, it := range r.Items {
		if _, err := st.CreateItem(it); err != nil {
			t.Fatalf("item %s: %v", it.Name, err)
		}
	}
	by := map[string][]store.Variable{}
	for _, v := range r.Variables {
		k := v.OwnerKind + "/" + v.OwnerUID
		by[k] = append(by[k], v)
	}
	for k, vs := range by {
		kind, uid, _ := strings.Cut(k, "/")
		if err := st.SetVariables(kind, uid, vs); err != nil {
			t.Fatal(err)
		}
	}
}

func TestISPOnlyContentConvertedAndWarned(t *testing.T) {
	r := importFx(t, "collection_v21.json")
	b := r.Bundle()
	b.Collections[0].Events = json.RawMessage(`[{"listen":"test","script":{"type":"application/x-isp","exec":["isp.finding('x')"]}}]`)
	for i := range b.Items {
		if b.Items[i].Name == "Create user" {
			b.Items[i].URL = json.RawMessage(`"https://example.com/{{name|b64}}/{{$oob}}"`)
		}
	}
	out, err := ExportCollection(b, r.Collection.UID, Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Data)
	if !strings.Contains(s, `"disabled":true`) || !strings.Contains(s, `// isp.finding('x')`) {
		t.Fatalf("isp script not converted to disabled explanatory event: %s", s)
	}
	feats := map[string]bool{}
	for _, w := range out.Warnings {
		feats[w.Feature] = true
	}
	if !feats["isp-only-syntax"] || !feats["script:application/x-isp"] {
		t.Fatalf("warnings: %+v", out.Warnings)
	}
}

func TestNativeCollectionExports(t *testing.T) {
	c := store.Collection{UID: "C1", Name: "Native"}
	b := store.CollectionsBundle{Collections: []store.Collection{c},
		Items: []store.Item{{UID: "I1", CollectionUID: "C1", Kind: "request", Rank: "a", Name: "ping", Method: "GET", URL: json.RawMessage(`"https://example.com"`)}}}
	out, err := ExportCollection(b, "C1", Options{Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Data), SchemaV21) || !strings.Contains(string(out.Data), `"name":"ping"`) {
		t.Fatalf("%s", out.Data)
	}
	if _, err := ExportCollection(b, "nope", Options{}); err != ErrNoCollection {
		t.Fatal("missing collection should error")
	}
}

// Field coverage: every key present in the v2.1 fixture reaches the export
// (mapped to a column or re-emitted from a sidecar). Walks the fixture and
// the export and requires the set of JSON key paths to be equal.
func TestFieldCoverage(t *testing.T) {
	r := importFx(t, "collection_v21.json")
	out, _ := ExportCollection(r.Bundle(), r.Collection.UID, Options{})
	var a, b any
	_ = json.Unmarshal(read(t, "collection_v21.json"), &a)
	_ = json.Unmarshal(out.Data, &b)
	pa, pb := paths(a, ""), paths(b, "")
	for p := range pa {
		if !pb[p] {
			t.Errorf("v2.1 key %s lost in export", p)
		}
	}
	for p := range pb {
		if !pa[p] {
			t.Errorf("export invented key %s", p)
		}
	}
	if len(pa) < 120 {
		t.Fatalf("fixture too thin for coverage: %d paths", len(pa))
	}
}

func paths(v any, prefix string) map[string]bool {
	out := map[string]bool{}
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			p := prefix + "/" + k
			out[p] = true
			for pp := range paths(c, p) {
				out[pp] = true
			}
		}
	case []any:
		for _, c := range x {
			for pp := range paths(c, prefix+"[]") {
				out[pp] = true
			}
		}
	}
	return out
}
