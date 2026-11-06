package msgcodec

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/starx"
)

const prefixAESCodec = `
meta = {
    "id": "aes-content-field",
    "title": "JSON content prefix+AES-ECB",
    "apply_on_send": True,
}

SECRET = "engagement-secret-do-not-ship"

def _key(prefix):
    digest = hash("sha512", prefix + SECRET)
    # first 16 bytes of SHA512 hex digest as raw key material via hex decode path
    return digest[:32]  # 16 bytes as hex

def match(flow, side):
    raw = flow.req_body if side == "req" else flow.res_body
    return flow.host.endswith("example.com") and '"content"' in raw

def decode(flow, side, raw):
    obj = json_decode(raw)
    blob = obj["content"]
    prefix = blob[:32]
    ct = blob[32:]
    pt = aes_ecb_decrypt(_key(prefix), ct)
    return {"plaintext": pt, "fields": {"content": pt}, "note": "prefix=" + prefix}

def encode(flow, side, plaintext):
    obj = json_decode(flow.req_body if side == "req" else flow.res_body)
    blob = obj["content"]
    prefix = blob[:32]
    obj["content"] = prefix + aes_ecb_encrypt(_key(prefix), plaintext)
    return json_encode(obj)
`

func TestCompileRequiresMatchDecode(t *testing.T) {
	if _, err := Compile("x", `def encode(f,s,p): return p`); err == nil {
		t.Fatal("expected error for missing match/decode")
	}
}

func TestRoundTripPrefixAES(t *testing.T) {
	c, err := Compile("aes-content-field", prefixAESCodec)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Meta.ApplyOnSend {
		t.Fatal("expected apply_on_send")
	}
	prefix := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 32 chars
	keyHex := mustHashHex(prefix + "engagement-secret-do-not-ship")[:32]
	ct, err := starx.AESECBEncrypt(mustHex(keyHex), []byte(`{"recNumb":"42"}`))
	if err != nil {
		t.Fatal(err)
	}
	wire := `{"timestamp":1,"content":"` + prefix + base64.StdEncoding.EncodeToString(ct) + `"}`
	flow := Flow{
		Method:  "POST",
		Host:    "api.example.com",
		Path:    "/v1/x",
		ReqBody: wire,
	}
	ok, err := c.Match(flow, "req")
	if err != nil || !ok {
		t.Fatalf("match: ok=%v err=%v", ok, err)
	}
	r, err := c.Decode(flow, "req")
	if err != nil {
		t.Fatal(err)
	}
	if r.Plaintext != `{"recNumb":"42"}` {
		t.Fatalf("plaintext=%q", r.Plaintext)
	}
	if r.Fields["content"] != r.Plaintext {
		t.Fatalf("fields=%v", r.Fields)
	}
	edited := `{"recNumb":"99"}`
	out, err := c.Encode(flow, "req", edited)
	if err != nil {
		t.Fatal(err)
	}
	flow2 := flow
	flow2.ReqBody = out
	r2, err := c.Decode(flow2, "req")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Plaintext != edited {
		t.Fatalf("round-trip plaintext=%q want %q; wire=%s", r2.Plaintext, edited, out)
	}
}

func TestTryDecodeSkipsNonMatching(t *testing.T) {
	c, err := Compile("aes-content-field", prefixAESCodec)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := TryDecode([]*Codec{c}, Flow{Host: "other.test", ReqBody: `{"content":"x"}`}, "req")
	if ok {
		t.Fatal("should not match")
	}
}

func TestSaveLoadDir(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "aes-content-field", prefixAESCodec); err != nil {
		t.Fatal(err)
	}
	list := List(dir)
	if len(list) != 1 || list[0].ID != "aes-content-field" || list[0].Error != "" {
		t.Fatalf("list=%+v", list)
	}
	codecs, errs := LoadDir(dir)
	if len(errs) != 0 || len(codecs) != 1 {
		t.Fatalf("load codecs=%d errs=%v", len(codecs), errs)
	}
	if !Exists(dir, "aes-content-field") {
		t.Fatal("exists")
	}
	if err := Delete(dir, "aes-content-field"); err != nil {
		t.Fatal(err)
	}
	if Exists(dir, "aes-content-field") {
		t.Fatal("deleted")
	}
}

func TestApplyOnSendRequiresEncode(t *testing.T) {
	src := `
meta = {"apply_on_send": True}
def match(flow, side): return True
def decode(flow, side, raw): return raw
`
	if _, err := Compile("bad", src); err == nil || !strings.Contains(err.Error(), "encode") {
		t.Fatalf("err=%v", err)
	}
}

func mustHashHex(s string) string {
	sum := sha512.Sum512([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// side aliases must mean the same thing everywhere: TryDecode already accepted
// "resp"/"response", but Decode looked up the request body for "resp" (so the
// response side decoded the wrong payload) and Match handed the raw alias to the
// script, which documents side as "req"|"res".
func TestSideAliasesResolveConsistently(t *testing.T) {
	c, err := Compile("sides", `
def match(flow, side):
    return side in ("req", "res")
def decode(flow, side, raw):
    return side + ":" + raw
`)
	if err != nil {
		t.Fatal(err)
	}
	flow := Flow{ReqBody: "REQ", ResBody: "RES"}
	for _, side := range []string{"res", "resp", "response", "RES", " Response "} {
		ok, err := c.Match(flow, side)
		if err != nil || !ok {
			t.Fatalf("Match(%q) = %v, %v; want a canonical side", side, ok, err)
		}
		r, err := c.Decode(flow, side)
		if err != nil {
			t.Fatalf("Decode(%q): %v", side, err)
		}
		if r.Plaintext != "res:RES" {
			t.Fatalf("Decode(%q) = %q; want the response body", side, r.Plaintext)
		}
		if _, matched := TryDecode([]*Codec{c}, flow, side); !matched {
			t.Fatalf("TryDecode(%q) did not match", side)
		}
	}
	for _, side := range []string{"req", "request", ""} {
		r, err := c.Decode(flow, side)
		if err != nil || r.Plaintext != "req:REQ" {
			t.Fatalf("Decode(%q) = %q, %v; want the request body", side, r.Plaintext, err)
		}
	}
}
