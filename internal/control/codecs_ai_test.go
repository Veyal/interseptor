package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodecsGeneratePrompt(t *testing.T) {
	p := codecsGeneratePrompt("unwrap JSON content field with AES-ECB", "", nil)
	if !strings.Contains(p, "AES-ECB") {
		t.Fatalf("missing description: %s", p)
	}
}

func TestCodecsGenerateSystemAPI(t *testing.T) {
	for _, sub := range []string{"def match(flow, side)", "def decode(flow, side, raw)", "aes_ecb_decrypt", "apply_on_send", "suggested-id"} {
		if !strings.Contains(codecsGenerateSystem, sub) {
			t.Fatalf("codecsGenerateSystem missing %q", sub)
		}
	}
}

func TestCodecsReferenceEmbedded(t *testing.T) {
	if len(codecsReferenceMD) < 100 {
		t.Fatal("codecs reference markdown not embedded")
	}
	if !strings.Contains(string(codecsReferenceMD), "def decode(flow, side, raw)") {
		t.Fatal("codecs_reference.md missing decode contract")
	}
}

func TestCodecsReferenceEndpoint(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/codecs/reference")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestCodecsGenerateRejectedWhenDisabled(t *testing.T) {
	h, s, _ := newHub(t)
	if err := s.SetSetting("ai.disabled", "1"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/ai/codecs/generate", "application/json",
		strings.NewReader(`{"description":"AES unwrap content field"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}
