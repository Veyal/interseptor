package capture

import (
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// TestIsAuthPath exercises the segment-aware path classifier.
func TestIsAuthPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// -- true cases (auth endpoints) --
		{"/login", true},
		{"/Login", true},     // case-insensitive
		{"/LOGIN", true},     // fully uppercase
		{"/logout", true},
		{"/signin", true},
		{"/signout", true},
		{"/signup", true},
		{"/register", true},
		{"/auth", true},
		{"/oauth", true},
		{"/oauth2/authorize", true},
		{"/oauth2", true},
		{"/oauth/callback", true},
		{"/token", true},
		{"/sso", true},
		{"/saml", true},
		{"/password", true},
		{"/reset", true},
		{"/mfa", true},
		{"/2fa", true},
		{"/totp", true},
		{"/verify", true},
		{"/api/v1/auth/token", true},
		{"/api/v2/login", true},
		{"/api/v1/oauth2/authorize", true},
		{"/user/password/reset", true},
		{"/account/mfa/setup", true},
		{"/api/sso/init", true},
		{"/login?redirect=/dashboard", true},  // query string after segment
		{"/auth/refresh", true},
		{"/v1/register", true},

		// -- false cases (must NOT match) --
		{"/blog", false},
		{"/blog/post", false},
		{"/catalog", false},
		{"/dialog", false},
		{"/dialog/open", false},
		{"/users", false},
		{"/profile", false},
		{"/api/v1/users", false},
		{"/search", false},
		{"/settings", false},
		{"/admin", false},
		{"/logview", false},        // "log" is a segment but not "login"/"logout"
		{"/blogpost", false},       // no auth segment
		{"/catalog/item", false},
		{"", false},
		{"/", false},
		{"/v2/products", false},
		{"/newsletter/signup-confirm", false}, // "signup-confirm" != "signup"
		{"/tokens", false},                    // "tokens" != "token"
		{"/authentication", false},            // longer word, not an exact segment match
		{"/authorized", false},                // not "auth" as a segment
	}

	for _, tc := range cases {
		got := isAuthPath(tc.path)
		if got != tc.want {
			t.Errorf("isAuthPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestTagIfAuth confirms that TagIfAuth writes the "auth" tag to the store
// exactly once per auth-path flow, and is a no-op for non-auth paths.
func TestTagIfAuth(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	c := New(s)

	// Insert two flows so we have real IDs.
	authFlow := &store.Flow{Method: "POST", Scheme: "https", Host: "app.test", Port: 443, Path: "/login"}
	id1, err := s.InsertFlow(authFlow)
	if err != nil {
		t.Fatalf("InsertFlow(auth): %v", err)
	}

	plainFlow := &store.Flow{Method: "GET", Scheme: "https", Host: "app.test", Port: 443, Path: "/dashboard"}
	id2, err := s.InsertFlow(plainFlow)
	if err != nil {
		t.Fatalf("InsertFlow(plain): %v", err)
	}

	// TagIfAuth on the auth-path flow should add the "auth" tag.
	c.TagIfAuth(id1, "/login")
	tags, err := s.FlowTags(id1)
	if err != nil {
		t.Fatalf("FlowTags(auth): %v", err)
	}
	if len(tags) != 1 || tags[0] != "auth" {
		t.Errorf("tags for auth flow = %v, want [auth]", tags)
	}

	// Calling TagIfAuth a second time must be idempotent (INSERT OR IGNORE).
	c.TagIfAuth(id1, "/login")
	tags2, _ := s.FlowTags(id1)
	if len(tags2) != 1 {
		t.Errorf("after second tag call, tags = %v, want [auth] (idempotent)", tags2)
	}

	// TagIfAuth on a non-auth path must add no tags.
	c.TagIfAuth(id2, "/dashboard")
	plainTags, err := s.FlowTags(id2)
	if err != nil {
		t.Fatalf("FlowTags(plain): %v", err)
	}
	if len(plainTags) != 0 {
		t.Errorf("tags for plain flow = %v, want []", plainTags)
	}

	// TagIfAuth with id=0 must be a no-op (no panic, no insert).
	c.TagIfAuth(0, "/login")
}

// TestTagIfAuth_DeepAPIPath confirms that nested auth segments are detected.
func TestTagIfAuth_DeepAPIPath(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	c := New(s)

	flow := &store.Flow{Method: "POST", Scheme: "https", Host: "api.test", Port: 443, Path: "/api/v1/auth/token"}
	id, err := s.InsertFlow(flow)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	c.TagIfAuth(id, "/api/v1/auth/token")
	tags, _ := s.FlowTags(id)
	if len(tags) != 1 || tags[0] != "auth" {
		t.Errorf("expected [auth] for /api/v1/auth/token, got %v", tags)
	}
}
