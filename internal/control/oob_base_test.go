package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/oob"
)

func oobTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, s, _ := newHub(t)
	if err := s.SetSetting("oob.enabled", "1"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func getOOBState(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/oob/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// A base URL without an http(s) scheme and host makes every payload URL
// unusable, so the catcher refuses it instead of minting broken payloads.
func TestOOBBaseURLMustBeAbsoluteHTTP(t *testing.T) {
	ts := oobTestServer(t)
	post := func(base string) int {
		body, _ := json.Marshal(map[string]string{"baseUrl": base})
		resp, err := http.Post(ts.URL+"/api/oob/base", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, bad := range []string{"example.com/oob", "javascript:alert(1)", "ftp://example.com/oob", "https://", "http://exa mple.com/oob", "https://example.com/oob?x=1", "https://example.com/oob#f"} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Fatalf("base %q status = %d, want 400", bad, code)
		}
	}
	if st := getOOBState(t, ts); st["configured"] != false {
		t.Fatalf("configured before save = %v, want false", st["configured"])
	}
	if code := post(" https://cb.example.com/oob/ "); code != http.StatusOK {
		t.Fatalf("valid base status = %d", code)
	}
	st := getOOBState(t, ts)
	if st["baseUrl"] != "https://cb.example.com/oob" || st["configured"] != true {
		t.Fatalf("state after save = %v", st)
	}
	if code := post(""); code != http.StatusOK {
		t.Fatalf("clearing base status = %d", code)
	}
	if st := getOOBState(t, ts); st["configured"] != false {
		t.Fatalf("configured after clear = %v", st["configured"])
	}
}

// Interactions live only in memory; the state says so, with the cap and how
// many older hits were evicted, so the UI can tell the operator.
func TestOOBStateReportsRetentionLimit(t *testing.T) {
	ts := oobTestServer(t)
	st := getOOBState(t, ts)
	if st["limit"] != float64(oob.MaxInteractions) || st["retention"] != "memory" || st["dropped"] != float64(0) {
		t.Fatalf("state = %v, want limit/retention/dropped", st)
	}
}
