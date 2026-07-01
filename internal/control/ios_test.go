package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/tlsca"
)

func TestIOSProfileEndpoint(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsca.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), ca, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ios/profile.mobileconfig?host=127.0.0.1&port=8080")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "apple-aspen-config") {
		t.Fatalf("content-type %q", ct)
	}
}

func TestIOSStatusEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ios/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
