package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactEndpointReturnsDescriptionOnly(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	secret := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJlZXhhbXBsZQ"
	code, body := postJSON(t, ts.URL+"/api/redact", `{"value":"`+secret+`"}`)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if strings.Contains(body, secret) {
		t.Fatalf("response echoed the value: %s", body)
	}
	var got struct {
		Len          int    `json:"len"`
		SHA256Prefix string `json:"sha256_prefix"`
		Kind         string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Len != len(secret) || len(got.SHA256Prefix) != 12 || got.Kind != "jwt" {
		t.Fatalf("body=%s err=%v", body, err)
	}
	if code, _ := postJSON(t, ts.URL+"/api/redact", `{"value":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty value status=%d, want 400", code)
	}
}

func TestRedactEndpointIsCatalogued(t *testing.T) {
	for _, route := range apiRoutes {
		if route.Method == "POST" && route.Path == "/api/redact" {
			return
		}
	}
	t.Fatal("POST /api/redact missing from the route catalog")
}
