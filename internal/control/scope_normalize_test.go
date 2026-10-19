package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestScopeHostIsNormalizedOrRejected(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	send := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if e != nil {
				break
			}
		}
		return resp.StatusCode, sb.String()
	}
	cases := []struct {
		name, body string
		want       store.ScopeRule
	}{
		{"url", `{"enabled":true,"action":"include","host":"https://Example.com/err"}`, store.ScopeRule{Host: "example.com", Scheme: "https", Path: "/err"}},
		{"url port", `{"enabled":true,"action":"include","host":"HTTP://a.example.com:18741"}`, store.ScopeRule{Host: "a.example.com", Scheme: "http", Port: 18741}},
		{"host port", `{"enabled":true,"action":"include","host":"b.example.com:8443"}`, store.ScopeRule{Host: "b.example.com", Port: 8443}},
		{"trim", `{"enabled":true,"action":"include","host":" C.Example.com "}`, store.ScopeRule{Host: "c.example.com"}},
		{"wildcard case", `{"enabled":true,"action":"include","host":"*.D.Example.com"}`, store.ScopeRule{Host: "*.d.example.com"}},
	}
	for _, c := range cases {
		code, body := send("POST", "/api/scope", c.body)
		if code != 201 {
			t.Fatalf("%s: %d %s", c.name, code, body)
		}
		var got store.ScopeRule
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Host != c.want.Host || got.Scheme != c.want.Scheme || got.Port != c.want.Port || got.Path != c.want.Path {
			t.Fatalf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	for _, bad := range []string{
		`{"enabled":true,"action":"include","host":"x y"}`,
		`{"enabled":true,"action":"include","host":"a.example.com:abc"}`,
		`{"enabled":true,"action":"include","host":"a.example.com:99999"}`,
		`{"enabled":true,"action":"include","host":"a.example.com:80","port":81}`,
		`{"enabled":true,"action":"include","host":"ftp://a.example.com"}`,
	} {
		if code, body := send("POST", "/api/scope", bad); code != 400 {
			t.Fatalf("%s = %d %s, want 400", bad, code, body)
		}
	}
	// PUT lowercases the same way.
	code, body := send("POST", "/api/scope", `{"enabled":true,"action":"include","host":"put.example.com"}`)
	if code != 201 {
		t.Fatal(code, body)
	}
	var made store.ScopeRule
	json.Unmarshal([]byte(body), &made)
	code, body = send("PUT", "/api/scope/"+itoa64(made.ID), `{"enabled":true,"action":"include","host":"http://PUT2.Example.com:81"}`)
	if code != 200 || !strings.Contains(body, `"host":"put2.example.com"`) || !strings.Contains(body, `"port":81`) {
		t.Fatalf("PUT = %d %s", code, body)
	}
}
