package bruno

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	exp "github.com/Veyal/interseptor/internal/collexport/postman"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func loadDir(t *testing.T, dir string) []File {
	t.Helper()
	var files []File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, File{Path: filepath.ToSlash(rel), Data: data})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func parseDir(t *testing.T) *Result {
	t.Helper()
	res, err := ParseFiles(loadDir(t, "testdata/collection"), Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGolden(t *testing.T) {
	res := parseDir(t)
	got, _ := json.MarshalIndent(res, "", "  ")
	got = append(got, '\n')
	const golden = "testdata/collection.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("golden mismatch; rerun with -update and review the diff")
	}
}

func find(res *Result, name string) *store.Item {
	for i := range res.Items {
		if res.Items[i].Name == name {
			return &res.Items[i]
		}
	}
	return nil
}

func TestSemantics(t *testing.T) {
	res := parseDir(t)
	if res.Collection.Name != "Bruno Example" || res.Report.Stats.Requests != 3 || res.Report.Stats.Folders != 1 {
		t.Fatalf("stats: %s %+v", res.Collection.Name, res.Report.Stats)
	}
	login, get := find(res, "Login"), find(res, "Get user")
	if login == nil || get == nil {
		t.Fatal("missing items")
	}
	if !strings.Contains(string(login.Headers), "X-Client") || !strings.Contains(string(get.Headers), "X-Folder") {
		t.Fatalf("inherited headers missing: %s / %s", login.Headers, get.Headers)
	}
	if !strings.Contains(string(get.URL), `"variable"`) || !strings.Contains(string(get.URL), `"disabled":true`) {
		t.Fatalf("url object: %s", get.URL)
	}
	if !strings.Contains(string(login.Assertions), `"$.token"`) || !strings.Contains(string(login.Assertions), `"unsupported"`) {
		t.Fatalf("assertions: %s", login.Assertions)
	}
	var as []map[string]any
	_ = json.Unmarshal(login.Assertions, &as)
	if len(as) != 5 || as[2]["type"] != "unsupported" {
		t.Fatalf("length assertion must be unsupported: %v", as)
	}
	if res.Report.Scripts.Total != 3 || res.Report.Scripts.Unsupported != 3 {
		t.Fatalf("scripts: %+v", res.Report.Scripts)
	}
	for _, s := range res.Report.ScriptList {
		if !s.Quarantine {
			t.Fatal("not quarantined")
		}
	}
	if !res.Report.Has(impkit.Unsupported, "assert:isNumber") {
		t.Fatal("isNumber should be reported unsupported")
	}
	if len(res.Environments) != 1 || res.Environments[0].Environment.Name != "dev" {
		t.Fatalf("envs: %+v", res.Environments)
	}
	if res.Collection.Description == "" {
		t.Fatal("docs missing")
	}
}

func TestSecretsAndDotenv(t *testing.T) {
	res := parseDir(t)
	b, _ := json.Marshal(res)
	for _, canary := range []string{"canary-env-password", "canary-bru-secret", "literal-bru-password", "do-not-read"} {
		// credentials in auth blocks stay in the item (they are the user's own
		// request config) but must never be in the report
		if canary == "literal-bru-password" {
			rb, _ := json.Marshal(res.Report)
			if strings.Contains(string(rb), canary) {
				t.Fatalf("%s leaked into report", canary)
			}
			continue
		}
		if strings.Contains(string(b), canary) {
			t.Fatalf("%s leaked into result JSON", canary)
		}
	}
	if len(res.SecretValues) < 2 {
		t.Fatalf("secret values: %+v", res.SecretValues)
	}
}

func TestRoundTripExport(t *testing.T) {
	res := parseDir(t)
	out, err := exp.ExportCollection(res.Bundle(), res.Collection.UID, exp.Options{})
	if err != nil || !json.Valid(out.Data) {
		t.Fatalf("export: %v", err)
	}
}

func TestSingleBru(t *testing.T) {
	res, err := Parse([]byte("get {\n  url: https://example.com/a\n}\n"), Options{NewID: counter()})
	if err != nil || res.Report.Stats.Requests != 1 {
		t.Fatalf("single: %v %+v", err, res)
	}
}

func TestHostile(t *testing.T) {
	files := []File{
		{Path: "../evil.bru", Data: []byte("get {\n  url: https://example.com\n}\n")},
		{Path: "/abs.bru", Data: []byte("get {\n  url: https://example.com\n}\n")},
		{Path: "ok.bru", Data: []byte("get {\n  url: https://example.com\n}\n")},
		{Path: "garbage.bru", Data: []byte("\x00\x01 not bru")},
		{Path: "node_modules/x.bru", Data: []byte("get {\n  url: https://example.com\n}\n")},
	}
	res, err := ParseFiles(files, Options{NewID: counter()})
	if err != nil || res.Report.Stats.Requests != 1 || !res.Report.Has(impkit.Blocked, "unsafe-path") {
		t.Fatalf("hostile paths: %v %+v", err, res.Report.Entries)
	}
	if _, err := ParseFiles([]File{{Path: "a.bru", Data: bytes.Repeat([]byte("a"), 10)}}, Options{}); err != ErrNoBru {
		t.Fatalf("garbage only: %v", err)
	}
	big := []File{{Path: "a.bru", Data: bytes.Repeat([]byte("a"), MaxTotalBytes+1)}}
	if _, err := ParseFiles(big, Options{}); err != ErrTooLarge {
		t.Fatalf("oversize: %v", err)
	}
	// unterminated block and a huge number of blocks stay bounded
	src := "get {\n  url: x\n" + strings.Repeat("a {\n}\n", MaxBlocks+10)
	if _, err := parseBru(src); err == nil {
		t.Fatal("block cap not enforced")
	}
	// deep folder nesting is bounded
	deep := strings.Repeat("d/", MaxDepth+10) + "r.bru"
	res, err = ParseFiles([]File{{Path: deep, Data: []byte("get {\n  url: https://example.com\n}\n")}}, Options{NewID: counter()})
	if res == nil || !res.Report.Has(impkit.Blocked, "too-deep") {
		t.Fatalf("deep: %v", err)
	}
}

func FuzzParseBru(f *testing.F) {
	f.Add("get {\n  url: x\n}\nbody:json {\n  {}\n}\nassert {\n  res.status: eq 200\n}\n")
	f.Add("vars:secret [\n a, b\n]\n")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseFiles([]File{{Path: "x.bru", Data: []byte(s)}}, Options{NewID: counter()})
	})
}
