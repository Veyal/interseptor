package harx

import (
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
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
