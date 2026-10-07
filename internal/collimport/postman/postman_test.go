package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func counterID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("ID%04d", n) }
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, name string) *Result {
	t.Helper()
	r, err := Parse(fixture(t, name), Options{NewID: counterID()})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

func itemByName(r *Result, name string) store.Item {
	for _, it := range r.Items {
		if it.Name == name {
			return it
		}
	}
	return store.Item{}
}

func TestImportTreeAndOrder(t *testing.T) {
	r := parse(t, "collection_v21.json")
	if r.Kind != KindCollection || r.Collection.Name != "Example API" {
		t.Fatalf("kind/name: %v %q", r.Kind, r.Collection.Name)
	}
	if r.Report.Stats.Folders != 1 || r.Report.Stats.Requests != 8 {
		t.Fatalf("stats %+v", r.Report.Stats)
	}
	users := itemByName(r, "Users")
	get := itemByName(r, "Get user")
	if get.ParentUID != users.UID || users.ParentUID != "" || get.Kind != "request" || users.Kind != "folder" {
		t.Fatalf("tree wrong: %+v / %+v", users, get)
	}
	if !(itemByName(r, "Get user").Rank < itemByName(r, "Create user").Rank) {
		t.Fatal("sibling order lost")
	}
	if r.Collection.ScopePolicy != store.ScopePolicyBlock {
		t.Fatal("imported collection must default to block scope policy")
	}
}

func TestImportAuthInheritanceAndNoauth(t *testing.T) {
	r := parse(t, "collection_v21.json")
	if !strings.Contains(string(r.Collection.Auth), `"bearer"`) {
		t.Fatal("collection auth lost")
	}
	if string(itemByName(r, "Users").Auth) != `{"type": "noauth"}` {
		t.Fatalf("folder noauth lost: %s", itemByName(r, "Users").Auth)
	}
	if len(itemByName(r, "Get user").Auth) != 0 {
		t.Fatal("request without auth must inherit (nil auth)")
	}
	if !strings.Contains(string(itemByName(r, "Create user").Auth), `"basic"`) {
		t.Fatal("request auth lost")
	}
}

func TestImportRowsBodiesAndExamples(t *testing.T) {
	r := parse(t, "collection_v21.json")
	get := itemByName(r, "Get user")
	var hs []map[string]any
	if err := json.Unmarshal(get.Headers, &hs); err != nil || len(hs) != 4 {
		t.Fatalf("headers (dups/order) %v %s", err, get.Headers)
	}
	if hs[1]["key"] != "X-Dup" || hs[2]["key"] != "X-Dup" || hs[2]["disabled"] != true {
		t.Fatalf("duplicate/disabled header lost: %v", hs)
	}
	if !strings.Contains(string(get.URL), `"variable"`) || !strings.Contains(string(get.URL), `"disabled": true`) {
		t.Fatalf("structured url lost: %s", get.URL)
	}
	if !strings.Contains(string(get.Settings), "disableBodyPruning") {
		t.Fatal("protocolProfileBehavior lost")
	}
	if !strings.Contains(string(get.Examples), "200 OK") || r.Report.Stats.Examples != 1 {
		t.Fatal("response example lost")
	}
	if !strings.Contains(string(itemByName(r, "Create user").Body), `"language": "json"`) {
		t.Fatal("raw language lost")
	}
	if !strings.Contains(string(itemByName(r, "Login form").Body), "urlencoded") || !strings.Contains(string(itemByName(r, "GraphQL").Body), "graphql") {
		t.Fatal("bodies lost")
	}
	if get.DescriptionMD != "Fetch one user" || !strings.Contains(string(get.Sidecar), "descInRequest") {
		t.Fatalf("request description not kept: %q", get.DescriptionMD)
	}
}

func TestImportVariablesSecretsAndDisabled(t *testing.T) {
	r := parse(t, "collection_v21.json")
	vars := map[string]store.Variable{}
	for _, v := range r.Variables {
		if v.OwnerKind == store.VarOwnerCollection {
			vars[v.Key] = v
		}
	}
	if vars["baseUrl"].InitialValue != "https://example.com" || !vars["baseUrl"].Enabled {
		t.Fatalf("baseUrl %+v", vars["baseUrl"])
	}
	if vars["legacy"].Enabled {
		t.Fatal("disabled variable became enabled")
	}
	if vars["retries"].InitialValue != "3" {
		t.Fatalf("numeric value: %+v", vars["retries"])
	}
	if s := vars["apiKey"]; s.Type != store.VarTypeSecret || s.InitialValue != "" {
		t.Fatalf("secret not blanked: %+v", s)
	}
	if len(r.SecretValues) != 1 || r.SecretValues[0].Key != "apiKey" || r.SecretValues[0].Value != "CANARY-COLL-SECRET" {
		t.Fatalf("secret value should be offered as current value only: %+v", r.SecretValues)
	}
	if !r.Report.Has(NeedsReview, "secret-variable") {
		t.Fatal("secret variable not flagged")
	}
	folderVars := 0
	for _, v := range r.Variables {
		if v.OwnerKind == store.VarOwnerFolder && v.Key == "folderVar" {
			folderVars++
		}
	}
	if folderVars != 1 {
		t.Fatal("folder variable missing")
	}
}

func TestImportScriptsQuarantinedAndAnalysed(t *testing.T) {
	r := parse(t, "collection_v21.json")
	if !strings.Contains(string(r.Collection.Events), "pm.collectionVariables.set('ts', Date.now());") {
		t.Fatal("exec lines must be kept verbatim")
	}
	if !strings.Contains(string(itemByName(r, "Get user").Events), `"exec": "pm.expect(1).to.eql(1)"`) {
		t.Fatal("string exec form must be preserved as-is")
	}
	s := r.Report.Scripts
	if s.Total != 4 || s.Unsupported != 1 || s.Partial != 1 || s.Supported != 2 {
		t.Fatalf("script summary %+v", s)
	}
	for _, sc := range r.Report.ScriptList {
		if !sc.Quarantine || len(sc.SourceHash) != 64 {
			t.Fatalf("script not quarantined: %+v", sc)
		}
	}
	for _, f := range []string{"api:pm.visualizer", "api:fetch", "api:require('lodash')"} {
		found := false
		for _, e := range r.Report.Entries {
			if e.Feature == f && e.Line > 0 && e.Path != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("report lacks %s with path and line", f)
		}
	}
	if !r.Report.Has(Unsupported, "api:fetch") || !r.Report.Has(Degraded, "api:require('lodash')") || !r.Report.Has(Blocked, "script-quarantined") {
		t.Fatal("levels wrong")
	}
	hosts := strings.Join(r.Report.ScriptList[2].Hosts, ",")
	if !strings.Contains(hosts, "example.org") {
		t.Fatalf("literal host not listed: %v", r.Report.ScriptList)
	}
}

func TestImportReportContent(t *testing.T) {
	r := parse(t, "collection_v21.json")
	rep := r.Report
	for _, c := range []struct {
		l Level
		f string
	}{
		{NeedsReview, "needs-asset"},
		{NeedsReview, "embedded-credential"},
		{NeedsReview, "dynamic-variable:$randomCity"},
		{PreservedInert, "auth:awsv4"},
		{PreservedInert, "key:x-vendor"},
		{PreservedInert, "key:_postman_vendor"},
		{PreservedInert, "request.certificate"},
		{Converted, "protocolProfileBehavior"},
		{Converted, "response-examples"},
		{Converted, "body.raw.language"},
	} {
		if !rep.Has(c.l, c.f) {
			t.Errorf("report missing %s %s", c.l, c.f)
		}
	}
	if rep.Has(NeedsReview, "dynamic-variable:$randomFirstName") {
		t.Error("known dynamic variable flagged")
	}
	if rep.Stats.NeedsAsset != 2 || rep.Stats.EmbeddedCredentials != 2 || rep.Stats.DisabledRows < 5 {
		t.Fatalf("stats %+v", rep.Stats)
	}
	if !strings.Contains(rep.Headline, "4 scripts") || !strings.Contains(rep.Headline, "quarantined") {
		t.Fatalf("headline %q", rep.Headline)
	}
	if rep.Entries[0].Level != Unsupported {
		t.Fatal("most severe entries must come first")
	}
	// the stored column carries the same report
	var stored Report
	if err := json.Unmarshal(r.Collection.ImportReport, &stored); err != nil || stored.Headline != rep.Headline {
		t.Fatalf("stored report: %v", err)
	}
}

func TestNoSecretLiteralsInReportOrJSON(t *testing.T) {
	r := parse(t, "collection_v21.json")
	blob, _ := json.Marshal(r)
	rep, _ := json.Marshal(r.Report)
	for _, canary := range []string{"CANARY-COLL-SECRET"} {
		if bytes.Contains(blob, []byte(canary)) {
			t.Errorf("secret variable value %s leaked into marshalled result", canary)
		}
	}
	for _, canary := range []string{"CANARY-COLL-SECRET", "CANARY-BASIC-LITERAL", "CANARY-HEADER-LITERAL"} {
		if bytes.Contains(rep, []byte(canary)) {
			t.Errorf("%s leaked into the report", canary)
		}
	}
	// env secrets too
	e := parse(t, "environment.json")
	eb, _ := json.Marshal(e)
	if bytes.Contains(eb, []byte("CANARY-ENV-SECRET")) {
		t.Error("env secret leaked into marshalled result")
	}
}

func TestImportEnvironmentAndGlobals(t *testing.T) {
	e := parse(t, "environment.json")
	if e.Kind != KindEnvironment || len(e.Environments) != 1 {
		t.Fatalf("env kind %v", e.Kind)
	}
	env := e.Environments[0]
	if env.Environment.Name != "Staging" || env.Environment.Kind != "env" {
		t.Fatalf("%+v", env.Environment)
	}
	byKey := map[string]store.Variable{}
	for _, v := range env.Variables {
		byKey[v.Key] = v
	}
	if byKey["token"].Type != store.VarTypeSecret || byKey["token"].InitialValue != "" {
		t.Fatal("env secret not blanked")
	}
	if byKey["off"].Enabled || byKey["off"].Type != store.VarTypeAny || byKey["host"].InitialValue != "staging.example.com" {
		t.Fatalf("env vars %+v", byKey)
	}
	if len(e.SecretValues) != 1 || e.SecretValues[0].OwnerUID != env.Environment.UID {
		t.Fatal("env secret value not offered as current")
	}
	g := parse(t, "globals.json")
	if g.Kind != KindGlobals || g.Environments[0].Environment.Kind != "globals" {
		t.Fatal("globals not detected")
	}
}

func TestImportWrapperWithEnvironment(t *testing.T) {
	col := fixture(t, "collection_v21.json")
	env := fixture(t, "environment.json")
	wrapped := []byte(`{"collection":` + string(col) + `,"environment":` + string(env) + `}`)
	r, err := Parse(wrapped, Options{NewID: counterID()})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindCollection || len(r.Environments) != 1 || r.Environments[0].Environment.Name != "Staging" {
		t.Fatalf("wrapper: %v %d", r.Kind, len(r.Environments))
	}
	var side CollectionSidecar
	_ = json.Unmarshal(r.Collection.Sidecar, &side)
	if !side.Wrapped {
		t.Fatal("wrapper flag lost")
	}
	if len(r.Bundle().Environments) != 1 || len(r.Bundle().Collections) != 1 {
		t.Fatal("bundle")
	}
}

func TestImportV20HeaderString(t *testing.T) {
	r := parse(t, "collection_v20.json")
	old := itemByName(r, "Old")
	if !strings.Contains(string(old.Headers), `"X-A"`) || !r.Report.Has(Degraded, "header-string") {
		t.Fatalf("v2.0 header string: %s", old.Headers)
	}
}

func TestImportShorthandRequest(t *testing.T) {
	r := parse(t, "collection_v21.json")
	sh := itemByName(r, "Shorthand")
	if sh.Method != "GET" || string(sh.URL) != `"https://example.com/ping"` {
		t.Fatalf("shorthand: %+v", sh)
	}
}

func TestUnsupportedFormatsReported(t *testing.T) {
	r, err := Parse(fixture(t, "collection_v3.json"), Options{})
	if !errors.Is(err, ErrUnsupported) || r == nil || !r.Report.Has(Unsupported, "postman-v3") {
		t.Fatalf("v3: %v", err)
	}
	r, err = Parse([]byte("$kind: collection\nname: x\n"), Options{})
	if !errors.Is(err, ErrUnsupported) || r == nil || !r.Report.Has(Unsupported, "postman-v3") {
		t.Fatalf("v3 yaml: %v", err)
	}
	if _, err = Parse([]byte(`{"info":{"name":"x"},"requests":[],"order":[]}`), Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("v1: %v", err)
	}
	if _, err = Parse([]byte(`{"hello":1}`), Options{}); !errors.Is(err, ErrNotPostman) {
		t.Fatalf("not postman: %v", err)
	}
	if _, err = Parse([]byte(`not json`), Options{}); !errors.Is(err, ErrNotPostman) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestUnsupportedProtocolAndScriptTypes(t *testing.T) {
	doc := `{"info":{"name":"p","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
	 "item":[{"name":"g","grpc":{"url":"x"}},{"name":"s","event":[{"listen":"test","script":{"type":"application/starlark","exec":["x"]}}],"request":{"method":"GET","url":"https://example.com"}}]}`
	r, err := Parse([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Report.Has(Unsupported, "protocol:grpc") || !r.Report.Has(Unsupported, "script.type:application/starlark") {
		t.Fatalf("%+v", r.Report.Entries)
	}
}

func TestHostileInputBounds(t *testing.T) {
	if _, err := Parse(make([]byte, MaxInputBytes+1), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
	nested := `{"name":"leaf","request":"https://example.com"}`
	for i := 0; i < MaxFolderDepth+5; i++ {
		nested = `{"name":"f","item":[` + nested + `]}`
	}
	doc := `{"info":{"name":"deep","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[` + nested + `]}`
	if _, err := Parse([]byte(doc), Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("depth cap: %v", err)
	}
}

func TestScriptAnalysisFlags(t *testing.T) {
	a := analyzeScript("eval('1')\nvar _0xabcd = 1;\nconst x = require('fs');\nprocess.env.X")
	if a.status() != "unsupported" || len(a.flags) != 2 {
		t.Fatalf("%+v", a)
	}
	b := analyzeScript("pm.environment.set('a', CryptoJS.MD5('x').toString());\nconst c = require('crypto-js');")
	if b.status() != "supported" {
		t.Fatalf("%+v", b)
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"collection_v21.json", "environment.json", "collection_v20.json"} {
		b, _ := os.ReadFile("testdata/" + n)
		f.Add(b)
	}
	f.Add([]byte(`{"info":{},"item":[{"item":[null]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		_, _ = Parse(data, Options{})
	})
}
