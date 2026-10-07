package native

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collimport/insomnia"
	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func sampleBundle(t *testing.T) (store.CollectionsBundle, string) {
	t.Helper()
	data, err := os.ReadFile("../../collimport/insomnia/testdata/v4.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := insomnia.Parse(data, insomnia.Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	return r.Bundle(), r.Collection.UID
}

func TestGoldenAndRoundTrip(t *testing.T) {
	b, uid := sampleBundle(t)
	out, err := Export(b, uid, Options{})
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.ixcol.json"
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, out.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(out.Data, want) {
		t.Fatal("golden mismatch; rerun with -update and review the diff")
	}
	back, err := Decode(out.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != len(b.Items) || len(back.Environments) != len(b.Environments) || len(back.Variables) != len(b.Variables) {
		t.Fatalf("counts differ: %d/%d items, %d/%d envs, %d/%d vars", len(back.Items), len(b.Items), len(back.Environments), len(b.Environments), len(back.Variables), len(b.Variables))
	}
	again, err := Export(back, uid, Options{})
	if err != nil || !bytes.Equal(again.Data, out.Data) {
		t.Fatalf("export>decode>export is not stable: %v", err)
	}
}

func TestSecretVariableBlankedWithoutOptIn(t *testing.T) {
	b, uid := sampleBundle(t)
	for i := range b.Variables {
		if b.Variables[i].Type == store.VarTypeSecret {
			b.Variables[i].InitialValue = "canary-var-value"
		}
	}
	out, _ := Export(b, uid, Options{})
	if strings.Contains(string(out.Data), "canary-var-value") {
		t.Fatal("secret variable value exported without opt-in")
	}
	out, _ = Export(b, uid, Options{IncludeSecrets: true})
	if !strings.Contains(string(out.Data), "canary-var-value") {
		t.Fatal("opt-in should keep the value")
	}
	// but Decode blanks it again: a file never smuggles secret values in
	back, _ := Decode(out.Data)
	for _, v := range back.Variables {
		if v.Type == store.VarTypeSecret && v.InitialValue != "" {
			t.Fatal("decode kept a secret value")
		}
	}
}

func TestScrubbedStoreBundleHasNoCanaries(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.CreateCollection(store.Collection{Name: "c", ScopePolicy: store.ScopePolicyWarn, Caps: json.RawMessage(`["net.outOfScope"]`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateItem(store.Item{CollectionUID: c.UID, Kind: "request", Rank: store.RankBetween("", ""), Name: "r", Method: "GET",
		URL:     json.RawMessage(`{"raw":"https://example.com"}`),
		Headers: json.RawMessage(`[{"key":"Authorization","value":"Bearer canary-header-token"}]`),
		Auth:    json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"canary-auth-token","type":"string"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ExportCollectionsBundle(store.ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Export(b, c.UID, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"canary-header-token", "canary-auth-token"} {
		if strings.Contains(string(out.Data), canary) {
			t.Fatalf("%s leaked into the native export", canary)
		}
	}
	back, err := Decode(out.Data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Collections[0].ScopePolicy != store.ScopePolicyBlock || len(back.Collections[0].Caps) != 0 {
		t.Fatalf("decode must reset policy and caps: %+v", back.Collections[0])
	}
}

func TestDecodeHostile(t *testing.T) {
	good := `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n","scopePolicy":"off","caps":["net.outOfScope"]},"items":[],"environments":[],"variables":[]}`
	b, err := Decode([]byte(good))
	if err != nil || b.Collections[0].ScopePolicy != "block" || len(b.Collections[0].Caps) != 0 {
		t.Fatalf("good: %v %+v", err, b.Collections)
	}
	bad := map[string]string{
		"empty":      ``,
		"format":     `{"format":"x","version":1}`,
		"version":    `{"format":"interseptor.collection","version":9,"collection":{"uid":"c","name":"n"}}`,
		"noname":     `{"format":"interseptor.collection","version":1,"collection":{"uid":"c"}}`,
		"dupuid":     `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"items":[{"uid":"a","kind":"folder"},{"uid":"a","kind":"folder"}]}`,
		"kind":       `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"items":[{"uid":"a","kind":"weird"}]}`,
		"cycle":      `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"items":[{"uid":"a","kind":"folder","parentUid":"b"},{"uid":"b","kind":"folder","parentUid":"a"}]}`,
		"noparent":   `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"items":[{"uid":"a","kind":"folder","parentUid":"zz"}]}`,
		"reqparent":  `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"items":[{"uid":"a","kind":"request"},{"uid":"b","kind":"request","parentUid":"a"}]}`,
		"varowner":   `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"variables":[{"ownerKind":"global","ownerUid":"x","key":"k"}]}`,
		"foreignvar": `{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"},"variables":[{"ownerKind":"collection","ownerUid":"other","key":"k"}]}`,
	}
	for name, in := range bad {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Decode(bytes.Repeat([]byte("a"), MaxBytes+1)); err == nil {
		t.Error("oversize accepted")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"format":"interseptor.collection","version":1,"collection":{"uid":"c","name":"n"}}`))
	f.Fuzz(func(t *testing.T, in []byte) { _, _ = Decode(in) })
}
