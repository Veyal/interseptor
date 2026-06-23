package codec

import "testing"

func TestRoundTrips(t *testing.T) {
	cases := []struct{ enc, dec, in string }{
		{"base64encode", "base64decode", "hello world"},
		{"urlencode", "urldecode", "a b&c=d/e?f"},
		{"hexencode", "hexdecode", "héllo"},
		{"htmlencode", "htmldecode", `<a href="x">&'</a>`},
	}
	for _, c := range cases {
		enc, err := Apply(c.enc, c.in)
		if err != nil {
			t.Fatalf("%s(%q): %v", c.enc, c.in, err)
		}
		dec, err := Apply(c.dec, enc)
		if err != nil {
			t.Fatalf("%s(%q): %v", c.dec, enc, err)
		}
		if dec != c.in {
			t.Fatalf("%s/%s round-trip: got %q want %q", c.enc, c.dec, dec, c.in)
		}
	}
}

func TestBase64DecodeAcceptsVariants(t *testing.T) {
	// URL-safe, no padding — should still decode.
	out, err := Apply("base64decode", "aGVsbG8")
	if err != nil || out != "hello" {
		t.Fatalf("rawurl b64: %q %v", out, err)
	}
}

func TestJWTDecode(t *testing.T) {
	// {"alg":"HS256","typ":"JWT"} . {"sub":"123","admin":true} . sig
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjMiLCJhZG1pbiI6dHJ1ZX0.sig"
	out, err := Apply("jwtdecode", jwt)
	if err != nil {
		t.Fatalf("jwtdecode: %v", err)
	}
	for _, want := range []string{"HS256", `"admin": true`, "── payload ──", "not verified"} {
		if !contains(out, want) {
			t.Fatalf("jwt output missing %q:\n%s", want, out)
		}
	}
	if _, err := Apply("jwtdecode", "notajwt"); err == nil {
		t.Fatal("expected error for non-JWT")
	}
}

func TestSmart(t *testing.T) {
	// base64 of "hello world"
	if out := must(t, "smart", "aGVsbG8gd29ybGQ="); out != "hello world" {
		t.Fatalf("smart base64: %q", out)
	}
	// percent-encoding
	if out := must(t, "smart", "a%20b%26c"); out != "a b&c" {
		t.Fatalf("smart url: %q", out)
	}
	// a plain word stays as-is (not falsely "decoded")
	if out := must(t, "smart", "interceptor"); out != "interceptor" {
		t.Fatalf("smart plain should be unchanged: %q", out)
	}
}

func TestUnknownOp(t *testing.T) {
	if _, err := Apply("nope", "x"); err == nil {
		t.Fatal("expected error for unknown op")
	}
}

func must(t *testing.T, op, in string) string {
	t.Helper()
	out, err := Apply(op, in)
	if err != nil {
		t.Fatalf("%s(%q): %v", op, in, err)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
