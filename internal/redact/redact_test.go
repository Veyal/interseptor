package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Fixtures are synthetic and non-functional; none is a real credential.
const (
	fixtureJWT    = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJlZXhhbXBsZQ"
	fixtureAIza   = "AIzaSyA1234567890abcdefghijklmnopqrstuv"
	fixtureBcrypt = "$2b$12$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"
	fixtureBearer = "Bearer abcdefghijklmnopqrstuvwxyz012345"
)

func TestDescribeReturnsLengthDigestPrefixAndKind(t *testing.T) {
	got := Describe(fixtureJWT)
	sum := sha256.Sum256([]byte(fixtureJWT))
	if got.Len != len(fixtureJWT) || got.SHA256Prefix != hex.EncodeToString(sum[:])[:PrefixLen] || got.Kind != "jwt" {
		t.Fatalf("Describe=%+v", got)
	}
	if strings.Contains(got.Placeholder(), fixtureJWT) {
		t.Fatal("placeholder leaked the value")
	}
	if Describe("same").SHA256Prefix != Describe("same").SHA256Prefix || Describe("same").SHA256Prefix == Describe("other").SHA256Prefix {
		t.Fatal("digest prefix must establish equality deterministically")
	}
}

func TestKinds(t *testing.T) {
	for value, want := range map[string]string{
		fixtureJWT: "jwt", fixtureAIza: "google_api_key", fixtureBcrypt: "bcrypt_hash", fixtureBearer: "bearer_token", "plain value": "secret",
	} {
		if got := Describe(value).Kind; got != want {
			t.Errorf("Kind(%q)=%s, want %s", value, got, want)
		}
	}
}

func TestScanFindsSecretsWithoutReturningThem(t *testing.T) {
	text := "Token " + fixtureJWT + " was sent; key " + fixtureAIza + "; hash " + fixtureBcrypt + "; header Authorization: " + fixtureBearer
	hits := Scan(text)
	kinds := map[string]bool{}
	for _, h := range hits {
		kinds[h.Kind] = true
		for _, raw := range []string{fixtureJWT, fixtureAIza, fixtureBcrypt, fixtureBearer} {
			if strings.Contains(h.Suggest, raw) {
				t.Fatalf("suggestion leaked a value: %s", h.Suggest)
			}
		}
		if !strings.HasPrefix(h.Suggest, "[redacted ") {
			t.Fatalf("suggestion should be the redacted form: %s", h.Suggest)
		}
	}
	for _, k := range []string{"jwt", "google_api_key", "bcrypt_hash", "bearer_token"} {
		if !kinds[k] {
			t.Errorf("missed %s in %+v", k, hits)
		}
	}
}

func TestScanIgnoresRedactedPlaceholdersAndProse(t *testing.T) {
	text := "The session token is [redacted jwt len=71 sha256=0123456789ab]. Use a Bearer scheme; the bearer is required."
	if hits := Scan(text); len(hits) != 0 {
		t.Fatalf("false positives: %+v", hits)
	}
}
