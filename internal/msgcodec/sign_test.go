package msgcodec

import (
	"strings"
	"testing"
)

const signingCodecSrc = `
meta = {"id": "signed", "apply_on_send": True}

def match(flow, side):
    return side == "req" and "x-pos-signature" in str(flow.req_header("X-Pos-Signature") or "")

def decode(flow, side, raw):
    return raw

def encode(flow, side, plaintext):
    return plaintext

def sign(flow, side, raw):
    return {
        "X-Pos-Timestamp": str(now_ms()),
        "X-Pos-Nonce": random_hex(8),
        "X-Pos-Signature": hmac_sha256_hex("secret", raw),
    }
`

func TestSignReturnsHeaderOverrides(t *testing.T) {
	c, err := Compile("signed", signingCodecSrc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !c.HasSign() {
		t.Fatal("HasSign should be true")
	}
	headers, err := c.Sign(Flow{Host: "api.example", Method: "POST"}, "req", `{"qty":2}`)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(headers) != 3 {
		t.Fatalf("headers = %+v", headers)
	}
	if headers["X-Pos-Signature"] != hmacHex("secret", `{"qty":2}`) {
		t.Fatalf("signature = %q", headers["X-Pos-Signature"])
	}
	if len(headers["X-Pos-Nonce"]) != 16 {
		t.Fatalf("nonce = %q", headers["X-Pos-Nonce"])
	}
	// The signature follows the body: an edited body yields a different value,
	// which is the whole point of recomputing it.
	other, err := c.Sign(Flow{Host: "api.example", Method: "POST"}, "req", `{"qty":999}`)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if other["X-Pos-Signature"] == headers["X-Pos-Signature"] {
		t.Fatal("signature did not change with the body")
	}
}

func TestSignRequiresApplyOnSend(t *testing.T) {
	src := `
meta = {"id": "unsigned"}

def match(flow, side):
    return True

def decode(flow, side, raw):
    return raw

def sign(flow, side, raw):
    return {}
`
	_, err := Compile("unsigned", src)
	if err == nil || !strings.Contains(err.Error(), "apply_on_send") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignMustReturnStringHeaders(t *testing.T) {
	src := `
meta = {"id": "bad", "apply_on_send": True}

def match(flow, side):
    return True

def decode(flow, side, raw):
    return raw

def encode(flow, side, plaintext):
    return plaintext

def sign(flow, side, raw):
    return {"X-Bad": 5}
`
	c, err := Compile("bad", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := c.Sign(Flow{}, "req", "x"); err == nil || !strings.Contains(err.Error(), "string header") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignIsOptional(t *testing.T) {
	c, err := Compile("plain", `
meta = {"id": "plain"}

def match(flow, side):
    return True

def decode(flow, side, raw):
    return raw
`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if c.HasSign() {
		t.Fatal("a codec without sign() must report HasSign false")
	}
	if headers, err := c.Sign(Flow{}, "req", "x"); err != nil || headers != nil {
		t.Fatalf("headers=%v err=%v", headers, err)
	}
}
