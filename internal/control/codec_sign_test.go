package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const signingCodec = `meta = {"id": "signed", "apply_on_send": True}

def match(flow, side):
    return side == "req"

def decode(flow, side, raw):
    return raw

def encode(flow, side, plaintext):
    return "enc(" + plaintext + ")"

def sign(flow, side, raw):
    return {"X-Pos-Timestamp": str(now_ms()), "X-Pos-Signature": hmac_sha256_hex("secret", raw)}
`

// A signed API accepts an edited body because the codec's sign() recomputed the
// signature over the outgoing wire body; without it every edit is a 401.
func TestRepeaterSendAppliesCodecSignature(t *testing.T) {
	var gotBody, gotSig, gotTS string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		gotSig = r.Header.Get("X-Pos-Signature")
		gotTS = r.Header.Get("X-Pos-Timestamp")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	hub, st, _ := newHub(t)
	hub.ProjectDir = t.TempDir()
	if err := os.MkdirAll(hub.CodecsDir(), 0o755); err != nil {
		t.Fatalf("mkdir codecs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hub.CodecsDir(), "signed.star"), []byte(signingCodec), 0o644); err != nil {
		t.Fatalf("write codec: %v", err)
	}
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"method": "POST", "url": target.URL + "/pay", "headers": "Content-Type: application/json",
		"body": `{"qty":2}`, "bodyMode": "decoded", "codecId": "signed",
	})
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, msg)
	}
	if gotBody != `enc({"qty":2})` {
		t.Fatalf("body = %q (the codec must re-encode)", gotBody)
	}
	if gotSig == "" {
		t.Fatal("signature header missing: sign() was not applied")
	}
	if gotTS == "" {
		t.Fatal("timestamp header missing")
	}
	_ = st
}

// A codec without sign() leaves the captured headers alone.
func TestRepeaterSendWithoutSignLeavesHeaders(t *testing.T) {
	var gotSig string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Pos-Signature")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()

	hub, _, _ := newHub(t)
	hub.ProjectDir = t.TempDir()
	if err := os.MkdirAll(hub.CodecsDir(), 0o755); err != nil {
		t.Fatalf("mkdir codecs: %v", err)
	}
	src := `meta = {"id": "plain", "apply_on_send": True}

def match(flow, side):
    return True

def decode(flow, side, raw):
    return raw

def encode(flow, side, plaintext):
    return plaintext
`
	if err := os.WriteFile(filepath.Join(hub.CodecsDir(), "plain.star"), []byte(src), 0o644); err != nil {
		t.Fatalf("write codec: %v", err)
	}
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"method": "POST", "url": target.URL + "/pay",
		"headers": "Content-Type: application/json\r\nX-Pos-Signature: captured-value",
		"body":    `{"qty":2}`, "bodyMode": "decoded", "codecId": "plain",
	})
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	resp.Body.Close()
	if gotSig != "captured-value" {
		t.Fatalf("signature header = %q, want the captured value untouched", gotSig)
	}
}
