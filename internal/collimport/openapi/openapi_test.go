package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func idGen() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parseFile(t *testing.T, name string) *Result {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data, Options{NewID: idGen()})
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return res
}

func golden(t *testing.T, name string) {
	t.Helper()
	res := parseFile(t, name)
	got, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	g := "testdata/" + strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".yaml") + ".golden.json"
	if *update {
		if err := os.WriteFile(g, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(g)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", g, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s; rerun with -update and review the diff", name)
	}
}

func TestGoldenPetstoreJSON(t *testing.T) { golden(t, "petstore.json") }
func TestGoldenSwagger2YAML(t *testing.T) { golden(t, "swagger2.yaml") }
func TestGoldenOAS31Cycles(t *testing.T)  { golden(t, "oas31.yaml") }

func items(res *Result) map[string]store.Item {
	m := map[string]store.Item{}
	for _, it := range res.Items {
		m[it.Name] = it
	}
	return m
}

func TestFoldersByTagInTagOrder(t *testing.T) {
	res := parseFile(t, "petstore.json")
	var folders []string
	for _, it := range res.Items {
		if it.Kind == "folder" {
			folders = append(folders, it.Name)
		}
	}
	if strings.Join(folders, ",") != "pets,store" {
		t.Fatalf("folders = %v", folders)
	}
	it := items(res)["List pets"]
	if it.ParentUID == "" || it.Method != "GET" {
		t.Fatalf("List pets not in folder: %+v", it)
	}
	if h := items(res)["Health"]; h.ParentUID != "" {
		t.Fatalf("untagged op should be at root: %+v", h)
	}
}

func TestServersBecomeEnvironments(t *testing.T) {
	res := parseFile(t, "petstore.json")
	if len(res.Environments) != 2 || res.Environments[0].Environment.Name != "Production" {
		t.Fatalf("envs = %+v", res.Environments)
	}
	if v := res.Environments[0].Variables[0]; v.Key != "baseUrl" || v.InitialValue != "https://api.example.com/v1" {
		t.Fatalf("env var = %+v", v)
	}
	if !strings.Contains(string(items(res)["List pets"].URL), "{{baseUrl}}/pets") {
		t.Fatalf("url = %s", items(res)["List pets"].URL)
	}
}

func TestAuthSchemesBecomeAuthAndSecretVars(t *testing.T) {
	res := parseFile(t, "petstore.json")
	if !strings.Contains(string(res.Collection.Auth), `"bearer"`) || !strings.Contains(string(res.Collection.Auth), "{{bearerAuth_token}}") {
		t.Fatalf("collection auth = %s", res.Collection.Auth)
	}
	get := items(res)["getPet"]
	if !strings.Contains(string(get.Auth), `"apikey"`) || !strings.Contains(string(get.Auth), "X-API-Key") {
		t.Fatalf("op auth = %s", get.Auth)
	}
	if del := items(res)["DELETE /pets/{petId}"]; !strings.Contains(string(del.Auth), "noauth") {
		t.Fatalf("security [] should be noauth: %s", del.Auth)
	}
	secrets := map[string]bool{}
	for _, v := range res.Variables {
		if v.Type == store.VarTypeSecret {
			secrets[v.Key] = true
			if v.InitialValue != "" {
				t.Fatalf("secret %s has an initial value", v.Key)
			}
		}
	}
	if !secrets["bearerAuth_token"] || !secrets["apiKey"] {
		t.Fatalf("secret vars = %v", secrets)
	}
}

func TestExamplesBecomeSavedExamples(t *testing.T) {
	res := parseFile(t, "petstore.json")
	list := items(res)["List pets"]
	var ex []struct {
		Name string
		Code int
		Body string
	}
	if err := json.Unmarshal(list.Examples, &ex); err != nil || len(ex) != 2 {
		t.Fatalf("examples = %s err=%v", list.Examples, err)
	}
	if ex[0].Code != 200 || !strings.Contains(ex[0].Body, `"Rex"`) || !strings.Contains(ex[1].Body, "boom") {
		t.Fatalf("examples = %+v", ex)
	}
	if res.Report.Stats.Examples < 3 {
		t.Fatalf("stats = %+v", res.Report.Stats)
	}
}

func TestRequestBodyFromSchemaOmitsReadOnlyAndCutsCycle(t *testing.T) {
	res := parseFile(t, "petstore.json")
	post := items(res)["Create pet"]
	var b struct{ Raw string }
	_ = json.Unmarshal(post.Body, &b)
	if strings.Contains(b.Raw, `"id"`) {
		t.Fatalf("readOnly id leaked: %s", b.Raw)
	}
	if !strings.Contains(b.Raw, `"name": "Rex"`) || !strings.Contains(b.Raw, "user@example.com") {
		t.Fatalf("body = %s", b.Raw)
	}
	if !res.Report.Has(Degraded, "ref-cycle") {
		t.Fatalf("cycle not reported: %+v", res.Report.Entries)
	}
}

func TestFormAndMultipartBodies(t *testing.T) {
	res := parseFile(t, "petstore.json")
	orders := items(res)["POST /orders"]
	if !strings.Contains(string(orders.Body), `"urlencoded"`) || !strings.Contains(string(orders.Body), `"qty"`) {
		t.Fatalf("form body = %s", orders.Body)
	}
	up := items(res)["POST /upload"]
	if !strings.Contains(string(up.Body), `"type":"file"`) || !strings.Contains(string(up.Body), `"note"`) {
		t.Fatalf("multipart body = %s", up.Body)
	}
}

func TestParamsHeadersAndOptionalQuery(t *testing.T) {
	res := parseFile(t, "petstore.json")
	list := items(res)["List pets"]
	s := string(list.URL)
	if !strings.Contains(s, `"raw":"{{baseUrl}}/pets?status=available"`) || !strings.Contains(s, `"disabled":true`) {
		t.Fatalf("url = %s", s)
	}
	if !strings.Contains(string(list.Headers), `"X-Trace"`) {
		t.Fatalf("path-level header param missing: %s", list.Headers)
	}
	get := items(res)["getPet"]
	if !strings.Contains(string(get.URL), `"variable":[{"key":"petId","value":"7"}]`) || !strings.Contains(string(get.URL), "/pets/:petId") {
		t.Fatalf("path var url = %s", get.URL)
	}
}

func TestSwagger2(t *testing.T) {
	res := parseFile(t, "swagger2.yaml")
	if len(res.Environments) != 2 || res.Environments[0].Variables[0].InitialValue != "https://api.example.com/v2" {
		t.Fatalf("envs = %+v", res.Environments)
	}
	put := items(res)["PUT /users/{id}"]
	if !strings.Contains(string(put.Body), "friends") && !strings.Contains(string(put.Body), `\"id\"`) {
		t.Fatalf("body param = %s", put.Body)
	}
	login := items(res)["POST /login"]
	if !strings.Contains(string(login.Body), `"urlencoded"`) || !strings.Contains(string(login.Auth), "api_key") {
		t.Fatalf("login = %s / %s", login.Body, login.Auth)
	}
	if !strings.Contains(string(res.Collection.Auth), `"basic"`) {
		t.Fatalf("auth = %s", res.Collection.Auth)
	}
}

func TestRefCyclesExternalAndLoopsAreReportedNotFatal(t *testing.T) {
	res := parseFile(t, "oas31.yaml")
	for _, f := range []string{"ref-cycle", "path-ref", "webhooks", "param-ref"} {
		if !hasFeature(res, f) {
			t.Errorf("missing %s: %+v", f, res.Report.Entries)
		}
	}
	if res.Report.Stats.Requests < 2 {
		t.Fatalf("requests = %d", res.Report.Stats.Requests)
	}
}

func hasFeature(res *Result, f string) bool {
	for _, e := range res.Report.Entries {
		if e.Feature == f {
			return true
		}
	}
	return false
}

func TestDerefCycleGuard(t *testing.T) {
	doc := `{"a":{"$ref":"#/b"},"b":{"$ref":"#/a"},"c":{"$ref":"#/c"},"d":{"$ref":"https://x.example.com/y#/z"},"e":{"$ref":"#/missing"}}`
	tree, err := decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	root := asMap(tree)
	for _, k := range []string{"a", "c"} {
		if _, _, err := deref(root, root.get(k)); !errors.Is(err, ErrRefLoop) {
			t.Errorf("%s: err = %v", k, err)
		}
	}
	if _, _, err := deref(root, root.get("d")); !errors.Is(err, ErrExternalRef) {
		t.Errorf("d: %v", err)
	}
	if _, _, err := deref(root, root.get("e")); !errors.Is(err, ErrBadRef) {
		t.Errorf("e: %v", err)
	}
	// long non-cyclic chain hits the cap
	var sb strings.Builder
	sb.WriteString(`{"r0":{"$ref":"#/r1"}`)
	for i := 1; i < 40; i++ {
		fmt.Fprintf(&sb, `,"r%d":{"$ref":"#/r%d"}`, i, i+1)
	}
	sb.WriteString(`,"r40":{"x":1}}`)
	tree, _ = decode([]byte(sb.String()))
	if _, _, err := deref(asMap(tree), asMap(tree).get("r0")); !errors.Is(err, ErrRefLoop) {
		t.Errorf("chain cap: %v", err)
	}
}

func TestPointerEscapes(t *testing.T) {
	tree, _ := decode([]byte(`{"paths":{"/a~b/{x}":{"k":1}},"l":[10,20]}`))
	v, err := lookupRef(tree, "#/paths/~1a~0b~1%7Bx%7D/k")
	if err != nil || asString(v) != "1" {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if v, err := lookupRef(tree, "#/l/1"); err != nil || asString(v) != "20" {
		t.Fatalf("v=%v err=%v", v, err)
	}
}

func TestYAMLAndJSONAgree(t *testing.T) {
	y := "openapi: 3.0.0\ninfo: {title: T, version: '1'}\npaths:\n  /x:\n    get:\n      responses: {'200': {description: ok}}\n"
	j := `{"openapi":"3.0.0","info":{"title":"T","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	a, err1 := Parse([]byte(y), Options{NewID: idGen()})
	b, err2 := Parse([]byte(j), Options{NewID: idGen()})
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("differs:\n%s\n%s", ja, jb)
	}
}

func TestRejectsNonOpenAPI(t *testing.T) {
	for _, in := range []string{`{"foo":1}`, `[1,2]`, `just text: [`, ``, `swagger: "1.2"`, `openapi: "2.5"`} {
		if _, err := Parse([]byte(in), Options{}); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestHostileInputBounds(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte(" "), MaxInputBytes+1), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
	deep := strings.Repeat(`{"a":`, MaxDepth+10) + "1" + strings.Repeat("}", MaxDepth+10)
	if _, err := Parse([]byte(deep), Options{}); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("depth: %v", err)
	}
	if _, err := Parse([]byte("openapi: 3.0.0\nx: "+"["+strings.Repeat("[", MaxDepth+10)+strings.Repeat("]", MaxDepth+10)+"]"), Options{}); err == nil {
		t.Fatal("deep yaml accepted")
	}

	// YAML alias bomb ("billion laughs") must be bounded in time and nodes.
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\nbomb:\n  l0: &a0 [x,x,x,x,x,x,x,x,x,x]\n")
	for i := 1; i < 12; i++ {
		fmt.Fprintf(&sb, "  l%d: &a%d [", i, i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, "*a%d", i-1)
		}
		sb.WriteString("]\n")
	}
	start := time.Now()
	_, err := Parse([]byte(sb.String()), Options{})
	if !errors.Is(err, ErrTooManyN) {
		t.Fatalf("alias bomb err = %v, want ErrTooManyN", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("alias bomb took %v", time.Since(start))
	}
}

func TestExampleFanOutIsBudgeted(t *testing.T) {
	// 12 properties each referencing the next level 12 times: exponential if unbounded.
	var sb strings.Builder
	sb.WriteString(`{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{"/p":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/S0"}}}},"responses":{"200":{"description":"ok"}}}}},"components":{"schemas":{`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"S%d":{"type":"object","properties":{`, i)
		for j := 0; j < 12; j++ {
			if j > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `"p%d":{"$ref":"#/components/schemas/S%d"}`, j, i+1)
		}
		sb.WriteString("}}")
	}
	sb.WriteString(`,"S12":{"type":"string"}}}}`)
	start := time.Now()
	res, err := Parse([]byte(sb.String()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !res.Report.Has(Degraded, "example-truncated") && !res.Report.Has(Degraded, "example-depth") {
		t.Fatalf("truncation not reported: %+v", res.Report.Entries)
	}
}

func TestOperationCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{`)
	for i := 0; i < MaxOperations+5; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"/p%d":{"get":{"responses":{}}}`, i)
	}
	sb.WriteString("}}")
	res, err := Parse([]byte(sb.String()), Options{})
	if err != nil || res.Report.Stats.Requests != MaxOperations || !res.Report.Has(Blocked, "too-many-operations") {
		t.Fatalf("err=%v requests=%d", err, res.Report.Stats.Requests)
	}
}

func TestNoSecretsInReportAndCanary(t *testing.T) {
	spec := `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"security":[{"k":[]}],
	"components":{"securitySchemes":{"k":{"type":"apiKey","in":"header","name":"X-K","x-value":"CANARY-SPEC-SECRET"}}},
	"paths":{"/a":{"get":{"responses":{}}}}}`
	res, err := Parse([]byte(spec), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), "CANARY-SPEC-SECRET") {
		t.Fatalf("scheme extension value leaked into the result")
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"petstore.json", "swagger2.yaml", "oas31.yaml"} {
		d, _ := os.ReadFile("testdata/" + n)
		f.Add(d)
	}
	f.Add([]byte("openapi: 3.0.0\nx: &a [*a]"))
	f.Fuzz(func(t *testing.T, data []byte) {
		res, err := Parse(data, Options{NewID: idGen()})
		if err != nil || res == nil {
			return
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("not marshalable: %v", err)
		}
		for _, it := range res.Items {
			for _, raw := range []json.RawMessage{it.URL, it.Headers, it.Body, it.Auth, it.Examples, it.Tags, it.Sidecar} {
				if len(raw) > 0 && !json.Valid(raw) {
					t.Fatalf("invalid JSON column %q", raw)
				}
			}
		}
	})
}
