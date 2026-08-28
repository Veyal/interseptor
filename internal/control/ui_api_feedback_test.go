package control

import (
	"strings"
	"testing"
)

func TestUIAPIKeyCreationIsSingleFlight(t *testing.T) {
	apiPanel := executableJS(readUIAsset(t, "js/apipanel.js"))
	for _, contract := range []string{
		"let apiKeyCreatePending=false",
		"if(apiKeyCreatePending)return",
		"button.setAttribute('aria-busy','true')",
		"apiKeyCreatePending=false",
		"button.removeAttribute('aria-busy')",
	} {
		if !strings.Contains(apiPanel, contract) {
			t.Errorf("API-key single-flight contract missing %q", contract)
		}
	}
}

func TestUIAPIKeyListFailureIsRetryable(t *testing.T) {
	apiPanel := executableJS(readUIAsset(t, "js/apipanel.js"))
	for _, contract := range []string{
		"data-key-list-retry",
		"retry.onclick=loadApiKeys",
		"Keys unavailable:",
	} {
		if !strings.Contains(apiPanel, contract) {
			t.Errorf("API-key load feedback contract missing %q", contract)
		}
	}
}
