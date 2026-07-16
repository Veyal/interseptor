package starx

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// runBuiltin is a tiny harness: call a named builtin with the given starlark
// args and return its result (or the error). It lets each test below stay short.
func runBuiltin(t *testing.T, name string, args ...starlark.Value) (starlark.Value, error) {
	t.Helper()
	d := Predeclared()
	fn, ok := d[name]
	if !ok {
		t.Fatalf("builtin %q not registered", name)
	}
	return starlark.Call(&starlark.Thread{}, fn, starlark.Tuple(args), nil)
}

func str(v starlark.Value) string {
	s, ok := starlark.AsString(v)
	if !ok {
		return ""
	}
	return s
}

func TestJSONRoundTrip(t *testing.T) {
	// decode a JSON object and read a nested field back through starlark access
	got, err := runBuiltin(t, "json_decode", starlark.String(`{"user":"admin","roles":["a","b"],"n":3,"ok":true}`))
	if err != nil {
		t.Fatalf("json_decode: %v", err)
	}
	d, ok := got.(*starlark.Dict)
	if !ok {
		t.Fatalf("expected dict, got %s", got.Type())
	}
	v, ok, _ := d.Get(starlark.String("user"))
	if !ok || str(v) != "admin" {
		t.Fatalf("user field wrong: %v", v)
	}
	roles, ok, _ := d.Get(starlark.String("roles"))
	if !ok {
		t.Fatal("missing roles")
	}
	if _, ok := roles.(*starlark.List); !ok {
		t.Fatalf("roles should be a list, got %s", roles.Type())
	}

	// encode the decoded value back to JSON and check it round-trips the user
	out, err := runBuiltin(t, "json_encode", got)
	if err != nil {
		t.Fatalf("json_encode: %v", err)
	}
	if !strings.Contains(str(out), `"admin"`) {
		t.Fatalf("json_encode output missing admin: %s", str(out))
	}
}

func TestJSONDecodeInvalid(t *testing.T) {
	if _, err := runBuiltin(t, "json_decode", starlark.String(`{not json`)); err == nil {
		t.Fatal("expected error decoding invalid JSON")
	}
}

func TestBase64(t *testing.T) {
	enc, err := runBuiltin(t, "b64encode", starlark.String("hello"))
	if err != nil {
		t.Fatalf("b64encode: %v", err)
	}
	if str(enc) != "aGVsbG8=" {
		t.Fatalf("b64encode(hello) = %q want aGVsbG8=", str(enc))
	}
	dec, err := runBuiltin(t, "b64decode", starlark.String("aGVsbG8="))
	if err != nil {
		t.Fatalf("b64decode: %v", err)
	}
	if str(dec) != "hello" {
		t.Fatalf("b64decode = %q want hello", str(dec))
	}
	if _, err := runBuiltin(t, "b64decode", starlark.String("%%%notb64")); err == nil {
		t.Fatal("expected error decoding invalid base64")
	}
}

func TestURLEncodeDecode(t *testing.T) {
	enc, err := runBuiltin(t, "url_encode", starlark.String("a b&c=d"))
	if err != nil {
		t.Fatalf("url_encode: %v", err)
	}
	if str(enc) != "a+b%26c%3Dd" {
		t.Fatalf("url_encode = %q", str(enc))
	}
	dec, err := runBuiltin(t, "url_decode", starlark.String("a+b%26c%3Dd"))
	if err != nil {
		t.Fatalf("url_decode: %v", err)
	}
	if str(dec) != "a b&c=d" {
		t.Fatalf("url_decode = %q", str(dec))
	}
}

func TestHash(t *testing.T) {
	out, err := runBuiltin(t, "hash", starlark.String("sha256"), starlark.String("abc"))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if str(out) != want {
		t.Fatalf("hash(sha256,abc) = %q want %q", str(out), want)
	}
	// unknown algorithm is a clear error, not a silent empty string
	if _, err := runBuiltin(t, "hash", starlark.String("rot13"), starlark.String("abc")); err == nil {
		t.Fatal("expected error for unknown hash algorithm")
	}
}

func TestHMAC(t *testing.T) {
	out, err := runBuiltin(t, "hmac", starlark.String("sha256"), starlark.String("key"), starlark.String("msg"))
	if err != nil {
		t.Fatalf("hmac: %v", err)
	}
	// 32 bytes → 64 hex chars
	if len(str(out)) != 64 {
		t.Fatalf("hmac output length = %d want 64", len(str(out)))
	}
	if _, err := runBuiltin(t, "hmac", starlark.String("nope"), starlark.String("k"), starlark.String("m")); err == nil {
		t.Fatal("expected error for unknown hmac algorithm")
	}
}

// re_search and finding still live here after consolidation; sanity-check them.
func TestFindingAndReSearchStillPresent(t *testing.T) {
	f, err := runBuiltin(t, "finding", starlark.String("medium"), starlark.String("t"))
	if err != nil {
		t.Fatalf("finding: %v", err)
	}
	if _, ok := f.(*starlark.Dict); !ok {
		t.Fatalf("finding should return a dict, got %s", f.Type())
	}
	m, err := runBuiltin(t, "re_search", starlark.String("[0-9]+"), starlark.String("abc123"))
	if err != nil {
		t.Fatalf("re_search: %v", err)
	}
	if str(m) != "123" {
		t.Fatalf("re_search = %q want 123", str(m))
	}
}
