package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// The sidecar keeps a verbatim copy of the imported document so Postman export
// can be byte-exact. Once a user import stopped blanking credentials, that copy
// began carrying live secrets into every scrubbed path: archive, vault, bundle,
// peer merge and the AI read. scrubItemSecrets walked Auth/Headers/Params/URL/
// Body but never Sidecar or Examples, and scrubCollectionSecrets only walked
// Auth.
func TestCanaryScrubWalksSidecarAndExamples(t *testing.T) {
	const canary = "hunter2-SIDECAR-CANARY"

	it := &Item{
		Sidecar: json.RawMessage(`{"raw":{"auth":{"type":"bearer","bearer":[{"key":"token","value":"` + canary + `"}]},` +
			`"header":[{"key":"Authorization","value":"Bearer ` + canary + `"}]}}`),
		Examples: json.RawMessage(`[{"name":"ok","request":{"header":[{"key":"X-Api-Key","value":"` + canary + `"}]}}]`),
	}
	scrubItemSecrets(it)
	if strings.Contains(string(it.Sidecar), canary) {
		t.Errorf("item sidecar still carries the credential: %s", it.Sidecar)
	}
	if strings.Contains(string(it.Examples), canary) {
		t.Errorf("item examples still carry the credential: %s", it.Examples)
	}

	c := &Collection{
		Sidecar: json.RawMessage(`{"raw":{"auth":{"type":"apikey","apikey":[{"key":"value","value":"` + canary + `"}]}}}`),
	}
	scrubCollectionSecrets(c)
	if strings.Contains(string(c.Sidecar), canary) {
		t.Errorf("collection sidecar still carries the credential: %s", c.Sidecar)
	}
}
