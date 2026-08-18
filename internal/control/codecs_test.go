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

const testCodecSrc = `
meta = {"id": "aes-content-field", "title": "AES content", "apply_on_send": True}
SECRET = "engagement-secret-do-not-ship"
def _key(prefix):
    return hash("sha512", prefix + SECRET)[:32]
def match(flow, side):
    raw = flow.req_body if side == "req" else flow.res_body
    return flow.host.endswith("example.com") and '"content"' in raw
def decode(flow, side, raw):
    obj = json_decode(raw)
    blob = obj["content"]
    prefix = blob[:32]
    pt = aes_ecb_decrypt(_key(prefix), blob[32:])
    return {"plaintext": pt, "fields": {"content": pt}}
def encode(flow, side, plaintext):
    obj = json_decode(flow.req_body if side == "req" else flow.res_body)
    blob = obj["content"]
    prefix = blob[:32]
    obj["content"] = prefix + aes_ecb_encrypt(_key(prefix), plaintext)
    return json_encode(obj)
`

func TestFlowDecodedRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/encoded",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/flows/1/decoded?side=req", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	(&flowAPI{h}).getFlowDecoded(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestCodecsCRUDAndDecoded(t *testing.T) {
	h, st, _ := newHub(t)
	proj := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proj, "codecs"), 0o755)
	h.ProjectDir = proj
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]string{"source": testCodecSrc})
	saveReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/codecs/aes-content-field", strings.NewReader(string(body)))
	saveReq.Header.Set("Content-Type", "application/json")
	saveResp, err := http.DefaultClient.Do(saveReq)
	if err != nil {
		t.Fatal(err)
	}
	defer saveResp.Body.Close()
	if saveResp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d", saveResp.StatusCode)
	}

	prefix := strings.Repeat("a", 32)
	sum := sha512.Sum512([]byte(prefix + "engagement-secret-do-not-ship"))
	keyHex := hex.EncodeToString(sum[:])[:32]
	key, _ := hex.DecodeString(keyHex)
	ct, err := starx.AESECBEncrypt(key, []byte(`{"recNumb":"7"}`))
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decoded: %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["matched"] != true || got["plaintext"] != `{"recNumb":"7"}` {
		t.Fatalf("decoded=%v", got)
	}
	if got["applyOnSend"] != true {
		t.Fatalf("applyOnSend=%v", got["applyOnSend"])
	}

	listResp, err := http.Get(ts.URL + "/api/codecs")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var list struct {
		Codecs []map[string]any `json:"codecs"`
		Dir    string           `json:"dir"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&list)
	if len(list.Codecs) != 1 || list.Dir == "" {
		t.Fatalf("list=%+v", list)
	}
}
