package store

import (
	"encoding/json"
	"testing"
)

// The name heuristic matches any key that contains "token", "secret", …. A few
// OAuth settings contain those words but hold a choice, not a credential:
// addTokenTo says where the token goes (header or query), tokenType is its
// scheme, client_authentication is how the client credentials are sent. The
// export scrub blanked them, so an exported collection that placed its token in
// the query came back placing it in the header; and the importer reported them
// as literal credentials.
func TestR2SecretNameLeavesOAuthSettingsAlone(t *testing.T) {
	for _, name := range []string{"addTokenTo", "add_token_to", "tokenType", "token_type", "client_authentication", "clientAuthentication", "challengeAlgorithm"} {
		if IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = true; it names a setting, not a secret", name)
		}
	}
	// The real credentials keep matching.
	for _, name := range []string{"accessToken", "access_token", "refreshToken", "clientSecret", "client_secret", "password", "X-Api-Key", "Authorization", "Cookie", "session", "sessionId", "csrf_token", "idToken"} {
		if !IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = false; it names a secret", name)
		}
	}
}

func TestR2ExportScrubKeepsTheTokenPlacement(t *testing.T) {
	const auth = `{"type":"oauth2","oauth2":[` +
		`{"key":"accessToken","value":"live-token-value-1234567890"},` +
		`{"key":"addTokenTo","value":"queryParams"},` +
		`{"key":"tokenType","value":"Bearer"},` +
		`{"key":"client_authentication","value":"body"},` +
		`{"key":"clientSecret","value":"s3cr3t-value"}]}`
	out, blanked := scrubJSON(auth, true)
	var got struct {
		OAuth2 []struct{ Key, Value string } `json:"oauth2"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"accessToken": "", "addTokenTo": "queryParams", "tokenType": "Bearer", "client_authentication": "body", "clientSecret": ""}
	for _, p := range got.OAuth2 {
		if p.Value != want[p.Key] {
			t.Errorf("%s = %q after scrub, want %q", p.Key, p.Value, want[p.Key])
		}
	}
	if blanked != 2 {
		t.Errorf("blanked %d values, want exactly the token and the client secret", blanked)
	}
}
