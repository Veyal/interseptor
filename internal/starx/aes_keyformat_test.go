package starx

import (
	"encoding/base64"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

func callAES(t *testing.T, name string, fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error),
	key, data, format string) (string, error) {
	t.Helper()
	var kw []starlark.Tuple
	if format != "" {
		kw = []starlark.Tuple{{starlark.String("key_format"), starlark.String(format)}}
	}
	v, err := starlark.Call(&starlark.Thread{Name: "t"}, starlark.NewBuiltin(name, fn),
		starlark.Tuple{starlark.String(key), starlark.String(data)}, kw)
	if err != nil {
		return "", err
	}
	return string(v.(starlark.String)), nil
}

// A 32-character ASCII key that happens to be valid hex is an AES-256 raw key
// for some apps; key_format="raw" must use it as 32 bytes, "hex" forces hex,
// and the default ("auto") keeps the historical hex-first behaviour.
func TestAESKeyFormat(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef" // 32 chars, valid hex
	pt := "hello"

	rawCT, err := AESECBEncrypt([]byte(key), []byte(pt))
	if err != nil {
		t.Fatal(err)
	}
	hexKey, _ := decodeHexLoose(key)
	hexCT, err := AESECBEncrypt(hexKey, []byte(pt))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		format string
		want   []byte
	}{{"raw", rawCT}, {"hex", hexCT}, {"auto", hexCT}, {"", hexCT}} {
		got, err := callAES(t, "aes_ecb_encrypt", AESECBEncryptBuiltin, key, pt, tc.format)
		if err != nil {
			t.Fatalf("format %q: %v", tc.format, err)
		}
		if got != base64.StdEncoding.EncodeToString(tc.want) {
			t.Fatalf("format %q: wrong key interpretation", tc.format)
		}
		back, err := callAES(t, "aes_ecb_decrypt", AESECBDecryptBuiltin, key, got, tc.format)
		if err != nil || back != pt {
			t.Fatalf("format %q decrypt: %q %v", tc.format, back, err)
		}
	}

	if _, err := callAES(t, "aes_ecb_encrypt", AESECBEncryptBuiltin, "not-hex-not-hex-not-hex-not-hex!", pt, "hex"); err == nil {
		t.Fatal("key_format=hex accepted a non-hex key")
	}
	if _, err := callAES(t, "aes_ecb_encrypt", AESECBEncryptBuiltin, key, pt, "base64"); err == nil || !strings.Contains(err.Error(), "key_format") {
		t.Fatalf("unknown key_format: %v", err)
	}
}
