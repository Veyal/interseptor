package har

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	exp "github.com/Veyal/interseptor/internal/collexport/postman"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, _ := json.MarshalIndent(v, "", "  ")
	got = append(got, '\n')
	p := "testdata/" + name
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s", name)
	}
}

func TestHARGolden(t *testing.T) {
	data, _ := os.ReadFile("testdata/sample.har")
	res, err := ParseHAR(data, Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.golden.json", res)
	if res.Report.Stats.Requests != 4 || res.Report.Stats.Folders != 2 {
		t.Fatalf("stats: %+v", res.Report.Stats)
	}
	b, _ := json.Marshal(res.Report)
	if strings.Contains(string(b), "canary-har-token") {
		t.Fatal("credential in report")
	}
	if res.Report.Stats.EmbeddedCredentials != 1 || !res.Report.Has(impkit.NeedsReview, "binary-body") {
		t.Fatalf("report: %+v", res.Report)
	}
	if strings.Contains(string(res.Items[1].Headers), ":authority") || strings.Contains(string(res.Items[1].Headers), "Content-Length") {
		t.Fatalf("pseudo/length headers kept: %s", res.Items[1].Headers)
	}
}

func TestDedupeAndFlat(t *testing.T) {
	data, _ := os.ReadFile("testdata/sample.har")
	res, err := ParseHAR(data, Options{NewID: counter(), Dedupe: true, NoGroupHost: true})
	if err != nil || res.Report.Stats.Requests != 3 || res.Report.Stats.Folders != 0 {
		t.Fatalf("dedupe: %v %+v", err, res.Report.Stats)
	}
}

func TestBurp(t *testing.T) {
	req := "GET /a?b=1 HTTP/1.1\r\nHost: example.com\r\nX-T: 1\r\n\r\n"
	doc := `<?xml version="1.0"?><items burpVersion="2026.7"><item><time>Tue Aug 18 09:30:00 UTC 2026</time>
<url><![CDATA[https://example.com/a?b=1]]></url><host ip="203.0.113.10">example.com</host><port>443</port><protocol>https</protocol>
<method>GET</method><path><![CDATA[/a?b=1]]></path><request base64="true"><![CDATA[` + base64.StdEncoding.EncodeToString([]byte(req)) + `]]></request>
<status>200</status><responselength>0</responselength><mimetype>text</mimetype><response base64="true"><![CDATA[` + base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 200 OK\r\n\r\n")) + `]]></response></item></items>`
	res, err := ParseBurp(strings.NewReader(doc), Options{NewID: counter()})
	if err != nil || res.Report.Stats.Requests != 1 {
		t.Fatalf("burp: %v %+v", err, res)
	}
	out, err := exp.ExportCollection(res.Bundle(), res.Collection.UID, exp.Options{})
	if err != nil || !json.Valid(out.Data) {
		t.Fatalf("export: %v", err)
	}
}

func TestHostile(t *testing.T) {
	for _, in := range []string{``, `{}`, `{"log":{"version":"1.2"}}`, `[1,2]`} {
		if _, err := ParseHAR([]byte(in), Options{}); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
	if _, err := ParseHAR(bytes.Repeat([]byte("a"), MaxInputBytes+1), Options{}); err != ErrTooLarge {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := ParseBurp(strings.NewReader("not xml"), Options{}); err == nil {
		t.Fatal("non-XML burp accepted")
	}
	empty := `{"log":{"version":"1.2","entries":[]}}`
	if _, err := ParseHAR([]byte(empty), Options{}); err != ErrEmpty {
		t.Fatalf("empty: %v", err)
	}
	// entries over the cap are truncated with a blocked entry
	var sb strings.Builder
	sb.WriteString(`{"log":{"version":"1.2","entries":[`)
	for i := 0; i < MaxEntries+5; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"request":{"method":"GET","url":"https://example.com/x","headers":[]},"response":{"status":200,"headers":[],"content":{}}}`)
	}
	sb.WriteString(`]}}`)
	res, err := ParseHAR([]byte(sb.String()), Options{NewID: counter()})
	if err != nil || res.Report.Stats.Requests != MaxEntries || !res.Report.Has(impkit.Blocked, "too-many-requests") {
		t.Fatalf("cap: %v", err)
	}
}

func FuzzParseHAR(f *testing.F) {
	f.Add([]byte(`{"log":{"version":"1.2","entries":[]}}`))
	f.Fuzz(func(t *testing.T, in []byte) { _, _ = ParseHAR(in, Options{NewID: counter()}) })
}
