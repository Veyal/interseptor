package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/checkscript"
)

func TestChecksGeneratePrompt(t *testing.T) {
	p := checksGeneratePrompt("flag missing HSTS on HTTPS", "", nil)
	if !strings.Contains(p, "missing HSTS") {
		t.Fatalf("missing description: %s", p)
	}
}

func TestChecksGenerateSystemAPI(t *testing.T) {
	for _, sub := range []string{"def check(flow)", "finding(", "re_search", "req_header", "res_header", "suggested-id"} {
		if !strings.Contains(checksGenerateSystem, sub) {
			t.Fatalf("checksGenerateSystem missing %q", sub)
		}
	}
}

func TestExtractCheckSource(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"```python\ndef check(flow):\n    return []\n```",
			"def check(flow):\n    return []",
		},
		{
			"# suggested-id: missing-hsts\ndef check(flow):\n    return []",
			"def check(flow):\n    return []",
		},
		{"def check(flow):\n    return []", "def check(flow):\n    return []"},
	}
	for _, c := range cases {
		got := extractCheckSource(c.in)
		if got != c.want {
			t.Fatalf("extractCheckSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseSuggestedID(t *testing.T) {
	if got := parseSuggestedID("# suggested-id: jwt-leak\n\ndef check(flow):\n    return []"); got != "jwt-leak" {
		t.Fatalf("got %q", got)
	}
}

func TestChecksReferenceEmbedded(t *testing.T) {
	if len(checksReferenceMD) < 100 {
		t.Fatal("checks reference markdown not embedded")
	}
	if !strings.Contains(string(checksReferenceMD), "def check(flow)") {
		t.Fatal("reference missing check contract")
	}
}

func TestChecksReferenceEndpoint(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/checks/reference")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestChecksGenerateRejectedWhenDisabled(t *testing.T) {
	h, s, _ := newHub(t)
	if err := s.SetSetting("ai.disabled", "1"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/ai/checks/generate", "application/json",
		strings.NewReader(`{"description":"flag missing HSTS"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

func TestExtractCheckSourceCompiles(t *testing.T) {
	src := extractCheckSource(`# suggested-id: sample
def check(flow):
    if flow.scheme == "https" and not flow.res_header("Strict-Transport-Security"):
        return [finding("medium", "Missing HSTS")]
    return []`)
	if _, err := checkscript.Compile("test", src); err != nil {
		t.Fatal(err)
	}
}
