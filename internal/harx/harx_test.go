package harx

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestBuildParseRoundTrip(t *testing.T) {
	flows := []*store.Flow{{
		ID: 1, TS: time.UnixMilli(1_700_000_000_000).UTC(), Method: "POST", Scheme: "https",
		Host: "api.example.com", Port: 443, Path: "/login?next=/", HTTPVersion: "HTTP/1.1", Status: 200,
		ReqHeaders:  map[string][]string{"Host": {"api.example.com"}, "Content-Type": {"application/json"}},
		ResHeaders:  map[string][]string{"Content-Type": {"application/json"}},
		ReqBodyHash: "reqh", ResBodyHash: "resh", Mime: "application/json", DurationMs: 42,
	}}
	bodies := map[string][]byte{"reqh": []byte(`{"u":"a"}`), "resh": []byte(`{"ok":true}`)}
	body := func(h string) []byte { return bodies[h] }

	har := Build(flows, body)
	if len(har) == 0 {
		t.Fatal("empty HAR")
	}

	entries, err := Parse(har)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Method != "POST" || e.URL != "https://api.example.com/login?next=/" || e.Status != 200 {
		t.Fatalf("round-trip mismatch: %+v", e)
	}
	if string(e.ReqBody) != `{"u":"a"}` || string(e.ResBody) != `{"ok":true}` {
		t.Fatalf("body round-trip mismatch: req=%q res=%q", e.ReqBody, e.ResBody)
	}
	if e.ReqHeaders["Content-Type"][0] != "application/json" {
		t.Fatalf("header round-trip mismatch: %+v", e.ReqHeaders)
	}
}

// A binary body (invalid UTF-8) must survive a Build→Parse round-trip. Without
// base64 encoding, json.Marshal replaces invalid UTF-8 bytes with U+FFFD and the
// body is silently corrupted on export.
func TestBuildParseBinaryBodyRoundTrip(t *testing.T) {
	bin := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe, 0x00, 0x01}
	flows := []*store.Flow{{
		ID: 1, TS: time.UnixMilli(1).UTC(), Method: "GET", Scheme: "https", Host: "x.com", Port: 443,
		Path: "/img.png", HTTPVersion: "HTTP/1.1", Status: 200, ResBodyHash: "r", Mime: "image/png",
	}}
	body := func(h string) []byte {
		if h == "r" {
			return bin
		}
		return nil
	}
	har := Build(flows, body)
	if !strings.Contains(string(har), `"encoding": "base64"`) {
		t.Fatalf("binary body should be base64-encoded in the HAR:\n%s", har)
	}
	entries, err := Parse(har)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(entries[0].ResBody, bin) {
		t.Fatalf("binary body corrupted on round-trip:\n got %v\nwant %v", entries[0].ResBody, bin)
	}
}

// A HAR produced elsewhere (Chrome/Burp/Firefox) base64-encodes binary content
// and flags it with encoding:"base64"; Parse must decode it, not import the
// literal base64 string as the body.
func TestParseBase64EncodedBody(t *testing.T) {
	raw := []byte{0x00, 0x01, 0x02, 0xff, 0xfe}
	doc := `{"log":{"version":"1.2","entries":[{"startedDateTime":"2020-01-01T00:00:00Z","time":0,` +
		`"request":{"method":"GET","url":"https://x.com/","httpVersion":"HTTP/1.1","headers":[]},` +
		`"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[],"content":{"size":5,` +
		`"mimeType":"application/octet-stream","text":"` + base64.StdEncoding.EncodeToString(raw) + `","encoding":"base64"}}}]}}`
	entries, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(entries[0].ResBody, raw) {
		t.Fatalf("base64 body not decoded:\n got %v\nwant %v", entries[0].ResBody, raw)
	}
}

// A flow that errored before its scheme was known (Scheme == "") must still
// produce a valid absolute URL, not "://host".
func TestBuildDefaultsEmptyScheme(t *testing.T) {
	flows := []*store.Flow{{Method: "GET", Scheme: "", Host: "x.com", Port: 0, Path: "/a", HTTPVersion: "HTTP/1.1", Status: 502}}
	entries, err := Parse(Build(flows, func(string) []byte { return nil }))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if entries[0].URL != "http://x.com/a" {
		t.Fatalf("empty scheme should default to http, got %q", entries[0].URL)
	}
}

func TestBuildOmitsDefaultPort(t *testing.T) {
	flows := []*store.Flow{{Method: "GET", Scheme: "http", Host: "x.com", Port: 80, Path: "/", HTTPVersion: "HTTP/1.1", Status: 200}}
	entries, err := Parse(Build(flows, func(string) []byte { return nil }))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if entries[0].URL != "http://x.com/" {
		t.Fatalf("default port should be omitted, got %q", entries[0].URL)
	}
}
