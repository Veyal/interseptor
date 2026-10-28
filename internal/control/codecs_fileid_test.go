package control

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/starx"
	"github.com/Veyal/interseptor/internal/store"
)

// A codec copied to myapp.star keeps the original meta id. Decode must report
// the file id so the encode endpoint (which loads <id>.star) finds the same
// codec instead of failing or picking up a different file.
func TestCodecCopyWithStaleMetaIDDecodesAndEncodesByFileID(t *testing.T) {
	h, st, _ := newHub(t)
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "codecs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "codecs", "myapp.star"), []byte(testCodecSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ProjectDir = proj
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	prefix := strings.Repeat("b", 32)
	sum := sha512.Sum512([]byte(prefix + "engagement-secret-do-not-ship"))
	key, _ := hex.DecodeString(hex.EncodeToString(sum[:])[:32])
	ct, err := starx.AESECBEncrypt(key, []byte(`{"n":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	wire := `{"content":"` + prefix + base64.StdEncoding.EncodeToString(ct) + `"}`
	id, err := st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "POST", Scheme: "https", Host: "api.example.com", Port: 443, Path: "/x", Status: 200,
		ReqBodyHash: putTestBody(t, st, []byte(wire)), ReqLen: int64(len(wire)),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/flows/" + strconv.FormatInt(id, 10) + "/decoded?side=req")
	if err != nil {
		t.Fatal(err)
	}
	var dec map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&dec)
	resp.Body.Close()
	if dec["matched"] != true || dec["codecId"] != "myapp" || dec["applyOnSend"] != true {
		t.Fatalf("decoded=%v, want codecId myapp", dec)
	}

	body, _ := json.Marshal(map[string]any{"id": dec["codecId"], "flowId": id, "side": "req", "plaintext": `{"n":"2"}`, "rawBody": wire})
	eresp, err := http.Post(ts.URL+"/api/codecs/encode", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer eresp.Body.Close()
	var enc map[string]any
	_ = json.NewDecoder(eresp.Body).Decode(&enc)
	if eresp.StatusCode != http.StatusOK || enc["codecId"] != "myapp" {
		t.Fatalf("encode status=%d body=%v", eresp.StatusCode, enc)
	}
}
