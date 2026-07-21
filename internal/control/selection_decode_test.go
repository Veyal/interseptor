package control

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/starx"
	"github.com/Veyal/interseptor/internal/store"
)

const bareCodecSrc = `
meta = {"id": "bare-enc", "title": "Bare ENC"}
def match(flow, side):
    raw = flow.req_body if side == "req" else flow.res_body
    return raw.startswith("ENC:")
def decode(flow, side, raw):
    return raw[4:]
`

func postSelectionDecode(t *testing.T, ts *httptest.Server, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/selection-decode", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, got
}

func TestSelectionDecodeSmartOnly(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	code, got := postSelectionDecode(t, ts, map[string]any{
		"input": "aGVsbG8gd29ybGQ=",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if got["matched"] != true || got["output"] != "hello world" || got["kind"] != "Base64" {
		t.Fatalf("got=%v", got)
	}
}

func TestSelectionDecodeCodecFieldAndWholeBody(t *testing.T) {
	h, st, _ := newHub(t)
	proj := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proj, "codecs"), 0o755)
	h.ProjectDir = proj
	if err := os.WriteFile(filepath.Join(proj, "codecs", "aes-content-field.star"), []byte(testCodecSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	prefix := strings.Repeat("a", 32)
	sum := sha512.Sum512([]byte(prefix + "engagement-secret-do-not-ship"))
	keyHex := hex.EncodeToString(sum[:])[:32]
	key, _ := hex.DecodeString(keyHex)
	ct, err := starx.AESECBEncrypt(key, []byte(`{"recNumb":"7"}`))
	if err != nil {
		t.Fatal(err)
	}
	blob := prefix + base64.StdEncoding.EncodeToString(ct)
	wire := `{"content":"` + blob + `"}`
	id, err := st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "POST", Scheme: "https", Host: "api.example.com", Port: 443, Path: "/x", Status: 200,
		ReqBodyHash: putTestBody(t, st, []byte(wire)), ReqLen: int64(len(wire)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Selecting the encrypted field value → fields["content"].
	code, got := postSelectionDecode(t, ts, map[string]any{
		"input": blob, "flowId": id, "side": "req",
	})
	if code != http.StatusOK {
		t.Fatalf("field status=%d", code)
	}
	if got["matched"] != true || got["output"] != `{"recNumb":"7"}` {
		t.Fatalf("field got=%v", got)
	}
	if got["kind"] != "AES content" || got["codecId"] != "aes-content-field" {
		t.Fatalf("field kind/id=%v", got)
	}

	// Selecting the whole body → plaintext.
	code, got = postSelectionDecode(t, ts, map[string]any{
		"input": wire, "flowId": id, "side": "req",
	})
	if code != http.StatusOK || got["matched"] != true || got["output"] != `{"recNumb":"7"}` {
		t.Fatalf("body got=%v status=%d", got, code)
	}

	// Unrelated substring must not invent a codec preview.
	code, got = postSelectionDecode(t, ts, map[string]any{
		"input": "content", "flowId": id, "side": "req",
	})
	if code != http.StatusOK {
		t.Fatalf("unrelated status=%d", code)
	}
	if got["matched"] == true && got["codecId"] != nil {
		t.Fatalf("unrelated should not match codec: %v", got)
	}
}

func TestSelectionDecodeBareCodecPrefersOverSmart(t *testing.T) {
	h, st, _ := newHub(t)
	proj := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proj, "codecs"), 0o755)
	h.ProjectDir = proj
	if err := os.WriteFile(filepath.Join(proj, "codecs", "bare-enc.star"), []byte(bareCodecSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	wire := "ENC:secret-payload"
	id, err := st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "POST", Scheme: "https", Host: "api.example.com", Port: 443, Path: "/x", Status: 200,
		ReqBodyHash: putTestBody(t, st, []byte(wire)), ReqLen: int64(len(wire)),
	})
	if err != nil {
		t.Fatal(err)
	}

	code, got := postSelectionDecode(t, ts, map[string]any{
		"input": wire, "flowId": id, "side": "req",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if got["matched"] != true || got["output"] != "secret-payload" || got["kind"] != "Bare ENC" {
		t.Fatalf("got=%v", got)
	}
}

func TestSelectionDecodeTooShort(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	code, got := postSelectionDecode(t, ts, map[string]any{"input": "ab"})
	if code != http.StatusOK || got["matched"] != false {
		t.Fatalf("got=%v status=%d", got, code)
	}
}
