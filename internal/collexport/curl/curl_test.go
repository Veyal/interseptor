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

// An imported collection must not make the exported script read local files:
// curl -F treats a value starting with @ or < (and ;headers=@file params) as
// file references, and an argument starting with - as an option.
func TestFormAndURLCannotReferenceLocalFiles(t *testing.T) {
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
		{UID: "r", CollectionUID: "c", Kind: "request", Rank: "a", Name: "x", Method: "POST",
			URL: json.RawMessage(`"-o/tmp/pwn https://example.com/"`),
			Body: json.RawMessage(`{"mode":"formdata","formdata":[
				{"key":"k1","value":"@/home/victim/.ssh/id_rsa"},
				{"key":"k2","value":"</etc/passwd"},
				{"key":"k3","value":"x;headers=@/etc/passwd"},
				{"key":"k4","value":"@/etc/passwd\"q\\","contentType":"text/plain"},
				{"key":"k5","value":"v","contentType":"text/plain;headers=@/etc/passwd"},
				{"key":"k6=</etc/passwd;x","value":"v","contentType":"text/plain"},
				{"key":"k7=</etc/passwd;x","type":"file"}
			]}`)},
	}}
	out, err := ExportCollection(b, "c", Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Data)
	for _, want := range []string{
		`--url '-o/tmp/pwn https://example.com/'`,
		`--form-string 'k1=@/home/victim/.ssh/id_rsa'`,
		`--form-string 'k2=</etc/passwd'`,
		`--form-string 'k3=x;headers=@/etc/passwd'`,
		`-F 'k4="@/etc/passwd\"q\\";type=text/plain'`,
		`-F 'k5="v";type=text/plain'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "-F 'k1") || strings.Contains(s, "headers=@/etc/passwd'\n") || strings.Contains(s, "k6=") || strings.Contains(s, "k7=") {
		t.Errorf("unsafe form field emitted:\n%s", s)
	}
	res, err := curlimp.Parse(out.Data, curlimp.Options{NewID: counter()})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("re-import: %v\n%s", err, s)
	}
	if body := string(res.Items[0].Body); !strings.Contains(body, `@/home/victim/.ssh/id_rsa`) || strings.Contains(body, `"type":"file"`) {
		t.Errorf("re-imported body: %s", body)
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

// A raw body always goes out with the content type the pipeline derives from
// its language (text/plain when there is none). curl's --data-raw would
// otherwise send "application/x-www-form-urlencoded", so the exporter must
// spell the header out or the command does not reproduce the send.
func TestRawBodyExportsItsImplicitContentType(t *testing.T) {
	cases := []struct {
		body json.RawMessage
		want string
	}{
		{json.RawMessage(`{"mode":"raw","raw":"plain text"}`), "text/plain"},
		{json.RawMessage(`{"mode":"raw","raw":"<p>x</p>","options":{"raw":{"language":"html"}}}`), "text/html"},
		{json.RawMessage(`{"mode":"raw","raw":"{}","options":{"raw":{"language":"json"}}}`), "application/json"},
		{json.RawMessage(`{"mode":"raw","raw":"<a/>","options":{"raw":{"language":"xml"}}}`), "application/xml"},
	}
	for _, c := range cases {
		b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
			{UID: "r", CollectionUID: "c", Kind: "request", Rank: "a", Name: "x", Method: "POST",
				URL: json.RawMessage(`"https://example.com/x"`), Body: c.body},
		}}
		out, err := ExportCollection(b, "c", Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(out.Data); !strings.Contains(got, "-H 'Content-Type: "+c.want+"'") {
			t.Fatalf("body %s: missing Content-Type %s:\n%s", c.body, c.want, got)
		}
	}
}

// An explicit header always wins over the implicit default.
func TestRawBodyKeepsExplicitContentType(t *testing.T) {
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
		{UID: "r", CollectionUID: "c", Kind: "request", Rank: "a", Name: "x", Method: "POST",
			URL:     json.RawMessage(`"https://example.com/x"`),
			Headers: json.RawMessage(`[{"key":"Content-Type","value":"application/vnd.example+json"}]`),
			Body:    json.RawMessage(`{"mode":"raw","raw":"{}","options":{"raw":{"language":"json"}}}`)},
	}}
	out, err := ExportCollection(b, "c", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out.Data); strings.Count(s, "Content-Type:") != 1 || !strings.Contains(s, "application/vnd.example+json") {
		t.Fatalf("explicit content type not preserved:\n%s", s)
	}
}

// The URL column can be a structured object without a "raw" string (the store's
// EffectiveURL contract rebuilds it from protocol/host/port/path/query). The
// exporter only looked at "raw" and emitted an empty URL, losing the request.
func TestURLObjectWithoutRawIsRebuilt(t *testing.T) {
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c", Name: "c"}}, Items: []store.Item{
		{UID: "r", CollectionUID: "c", Kind: "request", Rank: "a", Name: "x", Method: "GET",
			URL: json.RawMessage(`{"protocol":"https","host":["example.com"],"path":["api","users"],"query":[{"key":"a","value":"1"},{"key":"off","value":"1","disabled":true}]}`)},
	}}
	out, err := ExportCollection(b, "c", Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Data)
	if !strings.Contains(s, "'https://example.com/api/users?a=1'") {
		t.Fatalf("structured URL not rebuilt:\n%s", s)
	}
	if strings.Contains(s, "off=1") {
		t.Fatalf("disabled query row exported:\n%s", s)
	}
	for _, w := range out.Warnings {
		if w.Feature == "no-url" {
			t.Fatalf("reported no-url for a structured URL: %+v", out.Warnings)
		}
	}
}
