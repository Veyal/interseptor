package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func burpSavedItemsXML() []byte {
	req := "POST /submit?q=1 HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/octet-stream\r\nX-Test: one\r\n\r\nrequest-body"
	res := "HTTP/1.1 201 Created\r\nContent-Type: application/octet-stream\r\nX-Result: ok\r\n\r\nresponse-body"
	doc := `<items burpVersion="2026.7"><item>` +
		`<time>Tue Aug 18 09:30:00 UTC 2026</time>` +
		`<url>https://example.com/submit?q=1</url><host>example.com</host><port>443</port><protocol>https</protocol>` +
		`<request base64="true">` + base64.StdEncoding.EncodeToString([]byte(req)) + `</request>` +
		`<response base64="true">` + base64.StdEncoding.EncodeToString([]byte(res)) + `</response>` +
		`<comment>Imported evidence note</comment></item></items>`
	return []byte(doc)
}

func TestImportBurpSavedItemsXML(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/import/burp", "application/xml", bytes.NewReader(burpSavedItemsXML()))
	if err != nil {
		t.Fatalf("import Burp XML: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var result struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Imported != 1 || result.Skipped != 0 {
		t.Fatalf("result = %+v", result)
	}

	flows, err := s.QueryFlows(10)
	if err != nil || len(flows) != 1 {
		t.Fatalf("flows = %d, err = %v", len(flows), err)
	}
	f := flows[0]
	if f.Method != "POST" || f.Scheme != "https" || f.Host != "example.com" || f.Port != 443 ||
		f.Path != "/submit?q=1" || f.Status != 201 || f.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("flow metadata = %+v", f)
	}
	if f.Flags&store.FlagImported == 0 || f.Note != "Imported evidence note" {
		t.Fatalf("flags/note = %d / %q", f.Flags, f.Note)
	}
	if f.ReqHeaders["X-Test"][0] != "one" || f.ResHeaders["X-Result"][0] != "ok" {
		t.Fatalf("headers = req %#v res %#v", f.ReqHeaders, f.ResHeaders)
	}
	if got := string(h.bodyBytes(f.ReqBodyHash)); got != "request-body" {
		t.Fatalf("request body = %q", got)
	}
	if got := string(h.bodyBytes(f.ResBodyHash)); got != "response-body" {
		t.Fatalf("response body = %q", got)
	}
}

func TestImportBurpRejectsNativeProjectWithGuidance(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/import/burp", "application/octet-stream", strings.NewReader("Burp project file binary data"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "native .burp") || !strings.Contains(string(body), "Save items") {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
}

func TestImportBurpInvalidatesEndpointsCache(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	endpointCount := func() int {
		resp, err := http.Get(ts.URL + "/api/endpoints")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Endpoints []json.RawMessage `json:"endpoints"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return len(out.Endpoints)
	}
	if got := endpointCount(); got != 0 {
		t.Fatalf("initial endpoints = %d", got)
	}
	resp, err := http.Post(ts.URL+"/api/import/burp", "application/xml", bytes.NewReader(burpSavedItemsXML()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := endpointCount(); got != 1 {
		t.Fatalf("endpoints after import = %d", got)
	}
}
