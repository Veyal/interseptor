package control

import (
	"strings"
	"testing"
)

func TestUICheckAndCodecReferenceErrorsCanRetry(t *testing.T) {
	cases := []struct {
		asset, marker, handler string
	}{
		{"js/scanner.js", "data-check-docs-retry", "retry.onclick=loadCheckDocs"},
		{"js/codecs.js", "data-codec-docs-retry", "retry.onclick=loadCodecDocs"},
	}
	for _, tc := range cases {
		body := executableJS(readUIAsset(t, tc.asset))
		for _, contract := range []string{tc.marker, tc.handler, "Retry"} {
			if !strings.Contains(body, contract) {
				t.Errorf("%s reference-retry contract missing %q", tc.asset, contract)
			}
		}
	}
}
