package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScopePutRejectsDuplicateAndHostIsCaseInsensitive(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return resp.StatusCode, b.String()
	}
	if c, b := do("POST", "/api/scope", `{"enabled":true,"action":"include","host":"example.com"}`); c != 201 {
		t.Fatalf("create A: %d %s", c, b)
	}
	if c, b := do("POST", "/api/scope", `{"enabled":true,"action":"include","host":"EXAMPLE.com"}`); c != 409 {
		t.Fatalf("POST case-variant = %d %s, want 409", c, b)
	}
	c, b := do("POST", "/api/scope", `{"enabled":true,"action":"include","host":"other.example.com"}`)
	if c != 201 {
		t.Fatalf("create B: %d %s", c, b)
	}
	id := b[strings.Index(b, `"id":`)+5:]
	id = id[:strings.IndexAny(id, ",}")]
	if c, b := do("PUT", "/api/scope/"+id, `{"enabled":true,"action":"include","host":"EXAMPLE.com"}`); c != 409 {
		t.Fatalf("PUT duplicate = %d %s, want 409", c, b)
	}
	if c, b := do("PUT", "/api/scope/"+id, `{"enabled":true,"action":"include","host":"third.example.com"}`); c != 200 {
		t.Fatalf("PUT unique = %d %s, want 200", c, b)
	}
	// Paths keep their case.
	if c, b := do("POST", "/api/scope", `{"enabled":true,"action":"include","host":"p.example.com","path":"/A"}`); c != 201 {
		t.Fatalf("path A: %d %s", c, b)
	}
	if c, b := do("POST", "/api/scope", `{"enabled":true,"action":"include","host":"p.example.com","path":"/a"}`); c != 201 {
		t.Fatalf("path a: %d %s", c, b)
	}
}
