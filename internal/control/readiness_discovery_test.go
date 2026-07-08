package control

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestReadinessEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/readiness")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPromoteFlowToAuthz(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	id, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "app.test", Port: 443, Path: "/api/me",
		ReqHeaders: map[string][]string{"Cookie": {"session=abc"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/authz/from-flow/"+strconv.FormatInt(id, 10),
		"application/json", strings.NewReader(`{"name":"Admin","merge":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
