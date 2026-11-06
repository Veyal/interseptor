package starx

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

func runCodec(t *testing.T, src string) (starlark.Value, error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	globals, err := starlark.ExecFile(thread, "t.star", src, Predeclared())
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	return globals["out"], nil
}

// The envelope from the field report: hex(IV(16) || AES-256-CBC-PKCS7(body)),
// key = SHA-256(timestamp || secret). Ten lines, no ECB emulation.
func TestCBCDecodesTheMobileEnvelopeInACodec(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	iv := []byte("iviviviviviviviv")
	plaintext := `{"rc":"0","data":{"token":"abcdef"}}`

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	wire := hex.EncodeToString(append(append([]byte{}, iv...), ct...))

	src := `
raw = ` + starlarkString(wire) + `
iv = raw[:32]
ct = raw[32:]
out = aes_cbc_decrypt(` + starlarkString(hex.EncodeToString(key)) + `, ct, iv, data_format="hex", iv_format="hex")
`
	v, err := runCodec(t, src)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	if got := string(v.(starlark.String)); got != plaintext {
		t.Fatalf("plaintext = %q, want %q", got, plaintext)
	}
}

func TestCBCRoundTripsThroughTheBuiltins(t *testing.T) {
	key := "00112233445566778899aabbccddeeff"
	iv := "000102030405060708090a0b0c0d0e0f"
	src := `
ct = aes_cbc_encrypt(` + starlarkString(key) + `, "hello cbc", ` + starlarkString(iv) + `)
out = aes_cbc_decrypt(` + starlarkString(key) + `, ct, ` + starlarkString(iv) + `)
`
	v, err := runCodec(t, src)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	if got := string(v.(starlark.String)); got != "hello cbc" {
		t.Fatalf("round trip = %q", got)
	}
}

func TestCTRIsItsOwnInverse(t *testing.T) {
	key := "0123456789abcdef"
	iv := "abcdef0123456789"
	src := `
c = aes_ctr(` + starlarkString(key) + `, "stream me", ` + starlarkString(iv) + `)
out = b64decode(aes_ctr(` + starlarkString(key) + `, c, ` + starlarkString(iv) + `))
`
	v, err := runCodec(t, src)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	if got := string(v.(starlark.String)); got != "stream me" {
		t.Fatalf("ctr round trip = %q", got)
	}
}

func TestAESModeErrorsAreActionable(t *testing.T) {
	_, err := AESCBCDecrypt([]byte("short"), make([]byte, 16), make([]byte, 16))
	if err == nil || !strings.Contains(err.Error(), "16, 24, or 32") {
		t.Fatalf("short key error = %v", err)
	}
	_, err = AESCBCDecrypt([]byte("0123456789abcdef"), make([]byte, 16), make([]byte, 8))
	if err == nil || !strings.Contains(err.Error(), "IV must be 16 bytes") {
		t.Fatalf("bad IV error = %v", err)
	}
	_, err = AESCBCDecrypt([]byte("0123456789abcdef"), make([]byte, 15), make([]byte, 16))
	if err == nil || !strings.Contains(err.Error(), "multiple of 16") {
		t.Fatalf("bad ciphertext error = %v", err)
	}
}

// A large body decodes in a handful of steps, which is the point of the native
// builtin: the ECB emulation hit the step budget around 24 KB.
func TestCBCDecodesAWideBodyCheaply(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	iv := bytes.Repeat([]byte{7}, 16)
	plaintext := bytes.Repeat([]byte("{\"k\":\"v\"},"), 12000) // ~120 KB

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	b64 := base64.StdEncoding.EncodeToString(ct)

	src := `
out = aes_cbc_decrypt(` + starlarkString(hex.EncodeToString(key)) + `, ` + starlarkString(b64) + `, ` + starlarkString(hex.EncodeToString(iv)) + `)
`
	thread := &starlark.Thread{Name: "wide"}
	thread.SetMaxExecutionSteps(50_000) // far below the codec budget
	globals, err := starlark.ExecFile(thread, "wide.star", src, Predeclared())
	if err != nil {
		t.Fatalf("wide decode: %v", err)
	}
	got := string(globals["out"].(starlark.String))
	if len(got) != len(plaintext) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(plaintext))
	}
}

// starlarkString renders a Go string as a Starlark literal, escaping it safely.
func starlarkString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
