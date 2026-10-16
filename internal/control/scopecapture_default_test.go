package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveCaptureScopeOnly(t *testing.T) {
	cases := []struct {
		name        string
		stored      string
		ok          bool
		hasIncludes bool
		want        bool
	}{
		{"explicit on wins over no includes", "1", true, false, true},
		{"explicit off wins over includes", "0", true, true, false},
		{"default on when includes exist", "", false, true, true},
		{"default off when no includes (preserve capture-all)", "", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveCaptureScopeOnly(c.stored, c.ok, c.hasIncludes); got != c.want {
				t.Fatalf("ResolveCaptureScopeOnly(%q,%v,%v) = %v, want %v", c.stored, c.ok, c.hasIncludes, got, c.want)
			}
		})
	}
}

// With no explicit capture.scopeOnly stored, adding an include rule flips the
// effective default so GET /api/settings reports captureScopeOnly=true.
func TestCaptureScopeOnlyDefaultsOnWhenIncludesExist(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	getScopeOnly := func() any {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/settings")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var s map[string]any
		json.NewDecoder(resp.Body).Decode(&s)
		return s["captureScopeOnly"]
	}

	if v := getScopeOnly(); v != false {
		t.Fatalf("with no include rules, default captureScopeOnly = %v, want false", v)
	}

	resp, err := http.Post(ts.URL+"/api/scope", "application/json",
		strings.NewReader(`{"action":"include","host":"*.example.com","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST scope status = %d", resp.StatusCode)
	}

	if v := getScopeOnly(); v != true {
		t.Fatalf("after adding an include rule, default captureScopeOnly = %v, want true", v)
	}
}
