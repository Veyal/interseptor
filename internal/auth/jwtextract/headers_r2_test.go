package jwtextract

import "testing"

const r2JWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln"

// Header names are case-insensitive: an imported or HTTP/2-style capture
// stores them lower-case, and its token must still be found.
func TestExtractFindsTokensUnderAnyHeaderCase(t *testing.T) {
	for _, name := range []string{"Authorization", "authorization", "AUTHORIZATION"} {
		tok, src := Extract(Input{ReqHeaders: map[string][]string{name: {"Bearer " + r2JWT}}})
		if tok != r2JWT || src != "bearer" {
			t.Errorf("%s header: token=%q source=%q", name, tok, src)
		}
	}
	for _, name := range []string{"Cookie", "cookie", "COOKIE"} {
		tok, src := Extract(Input{ReqHeaders: map[string][]string{name: {"a=b; jwt=" + r2JWT}}})
		if tok != r2JWT || src != "cookie:jwt" {
			t.Errorf("%s header: token=%q source=%q", name, tok, src)
		}
	}
}
