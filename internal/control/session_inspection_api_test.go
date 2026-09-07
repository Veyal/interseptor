package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestSessionInspectionEndpointIsPassiveAndRedacted(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	first, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 200,
		ReqHeaders: map[string][]string{"Cookie": {"sid=secret"}}, ResHeaders: map[string][]string{"Set-Cookie": {"sid=next; Path=/; Secure"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com", Path: "/account", Status: 401})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/flows/session-inspect?ids=" + itoa(first) + "," + itoa(second) + "&roles=anonymous,user")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "secret") || !strings.Contains(body, "anonymous") || !strings.Contains(body, "unknown") {
		t.Fatalf("unsafe/incomplete inspection response: %s", body)
	}
}

func TestSessionInspectionEndpointRejectsEmptyAndMissingSelection(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, path := range []string{"/api/flows/session-inspect", "/api/flows/session-inspect?ids=999999"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("path=%s status=%d body=%s", path, resp.StatusCode, body)
		}
	}
}

func TestSessionInspectionEndpointRejectsDuplicateIDsWhenRolesAreSupplied(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/flows/session-inspect?ids=" + itoa(id) + "," + itoa(id) + "&roles=anonymous,user")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(readAll(resp.Body), "duplicate flow ids") {
		t.Fatalf("status=%d body should explain duplicate ids", resp.StatusCode)
	}
}
