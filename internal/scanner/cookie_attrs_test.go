package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A Set-Cookie check must read the cookie's *attributes*, not search the whole
// header for a word. A cookie named "insecure_pref", or one whose value happens
// to contain "secure"/"httponly"/"samesite", carries none of those attributes:
// the old substring test reported it clean, a false negative on the session
// cookie the check exists to catch.
func TestCookieAttributeDetectionIgnoresNameAndValueSubstrings(t *testing.T) {
	flow := func(cookie string) *store.Flow {
		return &store.Flow{
			Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200,
			Mime: "application/json",
			ResHeaders: map[string][]string(http.Header{
				"Content-Type":              {"application/json"},
				"Strict-Transport-Security": {"max-age=63072000"},
				"Set-Cookie":                {cookie},
			}),
		}
	}
	for _, tc := range []struct {
		name, cookie string
		insecure     bool // missing Secure and/or HttpOnly
		noSameSite   bool // missing the SameSite attribute
		sameSiteNone bool // SameSite=None without Secure
	}{
		{"secure word in the cookie name", "insecure_pref=1; HttpOnly", true, true, false},
		{"secure word in the cookie value", "pref=secure-ish; HttpOnly", true, true, false},
		{"httponly word in the value", "pref=nothttponly; Secure", true, true, false},
		{"samesite word in the value", "pref=samesite; Secure; HttpOnly", false, true, false},
		{"all attributes present", "sid=abc; Secure; HttpOnly; SameSite=Lax", false, false, false},
		{"samesite=None has Secure", "sid=abc; Secure; HttpOnly; SameSite=None", false, false, false},
		{"secure word in name, SameSite=None", "secure_sid=abc; HttpOnly; SameSite=None", true, false, true},
		{"spaces around the attribute value", "sid=abc; Secure; HttpOnly; SameSite = Strict", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(flow(tc.cookie), nil, []byte(`{"ok":true}`))
			if has(got, "Cookie set without Secure and HttpOnly") != tc.insecure {
				t.Errorf("insecure-cookie = %v, want %v: %s",
					!tc.insecure, tc.insecure, titles(got))
			}
			if has(got, "Cookie missing SameSite attribute") != tc.noSameSite {
				t.Errorf("missing-SameSite = %v, want %v: %s",
					!tc.noSameSite, tc.noSameSite, titles(got))
			}
			if has(got, "Cookie SameSite=None without Secure") != tc.sameSiteNone {
				t.Errorf("SameSite=None-without-Secure = %v, want %v: %s",
					!tc.sameSiteNone, tc.sameSiteNone, titles(got))
			}
		})
	}
}
