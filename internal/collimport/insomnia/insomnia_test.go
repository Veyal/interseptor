package insomnia

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexport/postman"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parseFile(t *testing.T, name string) *Result {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data, Options{NewID: counter()})
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return res
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, _ := json.MarshalIndent(v, "", "  ")
	got = append(got, '\n')
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s; rerun with -update and review the diff", name)
	}
}

func TestGoldenV4(t *testing.T) {
	r := parseFile(t, "v4.json")
	golden(t, "v4.golden.json", r)
}

func TestGoldenV5(t *testing.T) {
	r := parseFile(t, "v5.yaml")
	golden(t, "v5.golden.json", r)
}

func TestV4Semantics(t *testing.T) {
	r := parseFile(t, "v4.json")
	if r.Collection.Name != "Example API" || r.Report.Stats.Requests != 4 || r.Report.Stats.Folders != 1 {
		t.Fatalf("collection/stats: %s %+v", r.Collection.Name, r.Report.Stats)
	}
	var login, get *store.Item
	for i := range r.Items {
		switch r.Items[i].Name {
		case "Login":
			login = &r.Items[i]
		case "Get user":
			get = &r.Items[i]
		}
	}
	if login == nil || get == nil {
		t.Fatal("missing items")
	}
	if !strings.Contains(string(login.URL), "{{base_url}}/login") {
		t.Fatalf("url not converted: %s", login.URL)
	}
	if !strings.Contains(string(login.Headers), "{{$guid}}") {
		t.Fatalf("uuid tag not converted: %s", login.Headers)
	}
	if get.ParentUID == "" {
		t.Fatal("get not under folder")
	}
	if !strings.Contains(string(get.Headers), "{{chain_1}}") || !strings.Contains(string(get.Vars), "$.token") {
		t.Fatalf("response chaining: %s / %s", get.Headers, get.Vars)
	}
	if !strings.Contains(string(get.Settings), `"followRedirects":false`) {
		t.Fatalf("settings: %s", get.Settings)
	}
	if !r.Report.Has(impkit.Unsupported, "resource:grpc_request") || !r.Report.Has(impkit.Degraded, "template-tag:faker") {
		t.Fatalf("report entries: %+v", r.Report.Entries)
	}
	if r.Report.Scripts.Total != 2 || r.Report.Scripts.Unsupported != 1 || r.Report.Scripts.Supported != 1 {
		t.Fatalf("scripts: %+v", r.Report.Scripts)
	}
	for _, s := range r.Report.ScriptList {
		if !s.Quarantine {
			t.Fatal("script not quarantined")
		}
	}
	if len(r.Environments) != 1 || r.Environments[0].Environment.Name != "Staging" {
		t.Fatalf("envs: %+v", r.Environments)
	}
}

func TestSecretsNeverInShareableColumns(t *testing.T) {
	r := parseFile(t, "v4.json")
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "canary-token-123") {
		t.Fatal("secret env value leaked into the result JSON")
	}
	found := false
	for _, s := range r.SecretValues {
		if s.Value == "canary-token-123" {
			found = true
		}
	}
	if !found {
		t.Fatal("secret value should be offered as local current value")
	}
	if r.Report.Stats.EmbeddedCredentials == 0 {
		t.Fatal("literal basic password not flagged")
	}
	rb, _ := json.Marshal(r.Report)
	if strings.Contains(string(rb), "literal-secret") {
		t.Fatal("credential value in report")
	}
}

func TestRoundTripPostmanExport(t *testing.T) {
	r := parseFile(t, "v4.json")
	out, err := postman.ExportCollection(r.Bundle(), r.Collection.UID, postman.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if json.Unmarshal(out.Data, &v) != nil {
		t.Fatalf("export is not JSON: %s", out.Data)
	}
}

func TestHostileInput(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte("a"), MaxInputBytes+1), Options{}); err != ErrTooLarge {
		t.Fatalf("oversize: %v", err)
	}
	for _, in := range []string{``, `[]`, `{}`, `{"resources": 5}`, "a: ["} {
		if _, err := Parse([]byte(in), Options{}); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
	// group cycle: A under B under A must not hang or lose requests
	cyc := `{"_type":"export","__export_format":4,"resources":[
	 {"_id":"a","_type":"request_group","parentId":"b","name":"A"},
	 {"_id":"b","_type":"request_group","parentId":"a","name":"B"},
	 {"_id":"r","_type":"request","parentId":"a","name":"R","url":"https://example.com","method":"GET"}]}`
	res, err := Parse([]byte(cyc), Options{NewID: counter()})
	if err != nil || res.Report.Stats.Requests != 1 || res.Report.Stats.Folders != 2 {
		t.Fatalf("cycle: %v %+v", err, res)
	}
	// deep nesting is bounded
	var sb strings.Builder
	sb.WriteString(`{"_type":"export","__export_format":4,"resources":[`)
	const depth = 200
	for i := 0; i < depth; i++ {
		parent := fmt.Sprintf("g%d", i-1)
		if i == 0 {
			parent = ""
		}
		fmt.Fprintf(&sb, `{"_id":"g%d","_type":"request_group","parentId":%q,"name":"g"},`, i, parent)
	}
	fmt.Fprintf(&sb, `{"_id":"r","_type":"request","parentId":"g%d","name":"R","url":"https://example.com"}]}`, depth-1)
	res, err = Parse([]byte(sb.String()), Options{NewID: counter()})
	if err != nil && err != ErrNothingFound {
		t.Fatal(err)
	}
	if res == nil || !res.Report.Has(impkit.Blocked, "too-deep") {
		t.Fatalf("deep nesting not blocked: %+v", res)
	}
	// YAML alias bomb
	bomb := "type: collection.insomnia.rest/5.0\na: &a [x,x,x,x,x,x,x,x,x,x]\n"
	prev := "a"
	for i := 0; i < 12; i++ {
		cur := fmt.Sprintf("b%d", i)
		bomb += fmt.Sprintf("%s: &%s [*%s,*%s,*%s,*%s,*%s,*%s,*%s,*%s,*%s,*%s]\n", cur, cur, prev, prev, prev, prev, prev, prev, prev, prev, prev, prev)
		prev = cur
	}
	if _, err := Parse([]byte(bomb), Options{}); err == nil {
		t.Fatal("alias bomb should fail or report nothing found")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"_type":"export","resources":[]}`))
	f.Add([]byte("type: collection.insomnia.rest/5.0\ncollection: []"))
	f.Fuzz(func(t *testing.T, in []byte) { _, _ = Parse(in, Options{NewID: counter()}) })
}

func TestFolderHeadersAreInheritedByRequests(t *testing.T) {
	in := `{"_type":"export","__export_format":4,"resources":[
	 {"_id":"w","_type":"workspace","name":"W"},
	 {"_id":"f","_type":"request_group","parentId":"w","name":"F","headers":[{"name":"X-Folder","value":"{{ _.v }}"}]},
	 {"_id":"r","_type":"request","parentId":"f","name":"R","url":"https://example.com","method":"GET","headers":[{"name":"X-Own","value":"1"}]}]}`
	res, err := Parse([]byte(in), Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	var req *store.Item
	for i := range res.Items {
		if res.Items[i].Name == "R" {
			req = &res.Items[i]
		}
	}
	if req == nil || !strings.Contains(string(req.Headers), "X-Folder") || !strings.Contains(string(req.Headers), "{{v}}") {
		t.Fatalf("headers: %s", req.Headers)
	}
	if !res.Report.Has(impkit.Degraded, "inherited-headers") {
		t.Fatal("inheritance not reported")
	}
}
